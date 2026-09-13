import {
  createSessionKeyPackageAckPacket, createHCToABAFramePacket, createHCAckFramePacket,
  createHCResumeStatePacket, Direction, IndexedDbSecureStore, openABAToHCFramePacket,
  openABAUncertainErrorPacket, openSessionKeyPackagePacket,
  type EndpointIdentity, type OpenedSessionKeyPackage, type ReadyGatewayConnection,
} from '@harness/hc-core';
import { useCallback, useEffect, useRef, useState } from 'react';
import {
  accessCredentialNeedsRefresh, closeEndpointSession, createEndpointSession, getEndpointSession,
  HcApiError, listABAEndpoints, listEndpointSessions, refreshEndpointSession,
  type ABAEndpointSummary, type EndpointSessionSummary, type RegistrationSession,
} from './api';
import { ChatWorkspace } from './chat/ChatWorkspace';
import {
  conversationTitle, MAX_MESSAGES, MAX_SAVED_CONVERSATIONS, receiveAcp, settleTurn, startTurn,
  type ChatMessage, type SavedConversation,
} from './chat/model';

const POLL_ATTEMPTS = 20;
const terminal = (status: EndpointSessionSummary['status']) => ['CLOSED', 'FAILED', 'ABA_REVOKED'].includes(status);
const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 250));
const textEncoder = new TextEncoder();

export function SessionSetup({ connection, identity, onRegistration, registration, secureStore,
  draft, onDraftChange, onOpenSettings, online,
}: {
  readonly connection: ReadyGatewayConnection; readonly identity: EndpointIdentity;
  readonly onRegistration: (registration: RegistrationSession) => void;
  readonly registration: RegistrationSession; readonly secureStore: IndexedDbSecureStore | null;
  readonly draft: string; readonly onDraftChange: (value: string) => void;
  readonly onOpenSettings: () => void; readonly online: boolean;
}) {
  const [endpoints, setEndpoints] = useState<readonly ABAEndpointSummary[]>([]);
  const [selectedABA, setSelectedABA] = useState('');
  const [runtimeProfileId, setRuntimeProfileId] = useState(import.meta.env.VITE_HARNESS_DEFAULT_RUNTIME_PROFILE_ID?.trim() || 'test-agent');
  const [workspaceId, setWorkspaceId] = useState(import.meta.env.VITE_HARNESS_DEFAULT_WORKSPACE_ID?.trim() || 'harness-platform');
  const [session, setSession] = useState<EndpointSessionSummary | null>(null);
  const [busy, setBusy] = useState(false);
  const [responding, setResponding] = useState(false);
  const [blocked, setBlocked] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [keyReady, setKeyReady] = useState(false);
  const [messages, setMessages] = useState<readonly ChatMessage[]>([]);
  const [saved, setSaved] = useState<readonly SavedConversation[]>([]);
  const [viewId, setViewId] = useState<string | null>(null);
  const [unrecoverable, setUnrecoverable] = useState<readonly EndpointSessionSummary[]>([]);
  const [reload, setReload] = useState(0);
  const sessionRef = useRef<EndpointSessionSummary | null>(null);
  const endpointsRef = useRef(endpoints);
  const messagesRef = useRef(messages);
  const draftRef = useRef(draft);
  const enqueuePacket = useRef<(encoded: Uint8Array) => void>(() => undefined);
  const pendingPackets = useRef<Uint8Array[]>([]);
  const processing = useRef<Promise<void>>(Promise.resolve());
  const openedPackage = useRef<OpenedSessionKeyPackage | null>(null);
  const lastPackageSequence = useRef(0n);
  const outboundControlSequence = useRef(0n);
  const outboundFrameSequence = useRef(0n);
  const inboundFrameSequence = useRef(0n);
  const connectionGeneration = useRef(connection.connectionGeneration);
  const awaiting = useRef<string | null>(null);
  const queuedPrompt = useRef<string | null>(null);
  const actionBusy = useRef(false);
  const epoch = useRef(0);
  const closeKeys = useRef(new Map<string, string>());

  useEffect(() => { endpointsRef.current = endpoints; }, [endpoints]);
  useEffect(() => { draftRef.current = draft; }, [draft]);
  const updateMessages = useCallback((change: (previous: readonly ChatMessage[]) => readonly ChatMessage[]) => {
    const next = change(messagesRef.current); messagesRef.current = next; setMessages(next);
  }, []);
  const updateSession = useCallback((value: EndpointSessionSummary | null) => {
    sessionRef.current = value; setSession(value);
  }, []);
  const markUncertain = useCallback((description: string) => {
    queuedPrompt.current = null;
    updateMessages((current) => settleTurn(current, awaiting.current, 'uncertain'));
    awaiting.current = null; setResponding(false); setBlocked(true); setError(description);
  }, [updateMessages]);

  useEffect(() => {
    let active = true;
    const processPacket = async (encoded: Uint8Array, packetEpoch: number) => {
      const stillCurrent = () => active && packetEpoch === epoch.current;
      if (!stillCurrent()) return;
      const currentSession = sessionRef.current;
      if (currentSession === null) {
        // Only retain a bounded keying window while an explicitly requested session is being created.
        if (queuedPrompt.current !== null) {
          if (pendingPackets.current.length >= 8) throw new Error('Keying queue exceeded');
          pendingPackets.current.push(encoded);
        }
        return;
      }
      if (terminal(currentSession.status)) return;
      const aba = endpointsRef.current.find((endpoint) => endpoint.id === currentSession.abaEndpointId);
      if (aba === undefined) throw new Error('Session ABA identity is unavailable');
      const uncertain = await openABAUncertainErrorPacket(aba.signingPublicJwk, encoded);
      if (!stillCurrent()) return;
      if (uncertain !== null) {
        zeroOpenedPackage(openedPackage.current); openedPackage.current = null; setKeyReady(false);
        updateSession({ ...currentSession, status: 'UNCERTAIN' });
        markUncertain('执行结果不确定。请检查本地工作区后结束会话，不要直接重发操作。');
        return;
      }
      const opened = await openSessionKeyPackagePacket(encoded, {
        abaEndpointId: aba.id, abaSigningPublicJwk: aba.signingPublicJwk,
        hcEndpointId: registration.endpointId, identity, sessionId: currentSession.sessionId,
      });
      if (!stillCurrent()) { zeroOpenedPackage(opened); return; }
      if (opened !== null) {
        if (opened.controlSequence <= lastPackageSequence.current) {
          zeroOpenedPackage(opened); throw new Error('SessionKeyPackage control sequence replayed');
        }
        zeroOpenedPackage(openedPackage.current); openedPackage.current = opened;
        lastPackageSequence.current = opened.controlSequence; setKeyReady(true);
        outboundControlSequence.current += 1n;
        const packet = await createSessionKeyPackageAckPacket(identity, {
          abaEndpointId: aba.id, controlSequence: outboundControlSequence.current,
          hcEndpointId: registration.endpointId, keyPackageId: opened.keyPackageId, sessionId: currentSession.sessionId,
        });
        if (!stillCurrent()) return;
        if (connection.socket.readyState !== WebSocket.OPEN) throw new Error('Connection closed before key ACK');
        connection.socket.send(new Uint8Array(packet).buffer);
        for (let attempt = 0; attempt < POLL_ATTEMPTS && stillCurrent(); attempt += 1) {
          const latest = await getEndpointSession(currentSession.sessionId);
          if (!stillCurrent()) return;
          updateSession(latest);
          if (latest.status === 'ACTIVE' || terminal(latest.status)) break;
          await delay();
        }
        return;
      }
      if (openedPackage.current === null) return;
      const frame = await openABAToHCFramePacket(aba.signingPublicJwk, openedPackage.current,
        { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: currentSession.sessionId }, encoded);
      if (!stillCurrent() || frame === null) return;
      if (frame.sequence > inboundFrameSequence.current + 1n) throw new Error('Non-contiguous frame sequence');
      if (secureStore === null) throw new Error('Durable HC inbox is unavailable');
      const stored = await secureStore.putInboxFrame({
        contentHash: frame.contentHash, direction: Direction.ABA_TO_HC, messageId: frame.messageId,
        plaintext: frame.plaintext, receivedAt: new Date().toISOString(), sequence: frame.sequence, sessionId: currentSession.sessionId,
      });
      if (!stillCurrent()) return;
      if (frame.sequence === inboundFrameSequence.current + 1n) inboundFrameSequence.current = frame.sequence;
      else if (stored !== 'duplicate') throw new Error('HC inbox cursor is inconsistent');
      const acknowledgment = await createHCAckFramePacket(identity,
        { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: currentSession.sessionId },
        Direction.ABA_TO_HC, inboundFrameSequence.current);
      if (!stillCurrent()) return;
      if (connection.socket.readyState !== WebSocket.OPEN) throw new Error('Connection closed before frame ACK');
      connection.socket.send(new Uint8Array(acknowledgment).buffer);
      if (stored === 'duplicate') return;
      const payload: unknown = JSON.parse(new TextDecoder().decode(frame.plaintext));
      const result = receiveAcp(messagesRef.current, payload, awaiting.current);
      updateMessages(() => result.messages);
      if (result.completed) {
        awaiting.current = null; setResponding(false);
        if (result.failed) { setBlocked(true); setError('Agent 未能完成这次请求。请检查执行状态后结束会话。'); }
      }
    };
    const schedule = (encoded: Uint8Array) => {
      const packetEpoch = epoch.current;
      processing.current = processing.current.then(() => processPacket(encoded, packetEpoch)).catch(() => {
        if (active && packetEpoch === epoch.current) markUncertain('消息验证或接收失败，已暂停发送。请检查连接与执行状态后结束会话。');
      });
    };
    const message = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer && event.data.byteLength <= 1_048_576) schedule(new Uint8Array(event.data));
    };
    enqueuePacket.current = schedule;
    connection.socket.addEventListener('message', message);
    if (sessionRef.current !== null) for (const encoded of pendingPackets.current.splice(0)) schedule(encoded);
    return () => { active = false; connection.socket.removeEventListener('message', message); if (enqueuePacket.current === schedule) enqueuePacket.current = () => undefined; };
  }, [connection.socket, identity, registration.endpointId, secureStore, updateSession, updateMessages, markUncertain]);

  useEffect(() => {
    if (session?.sessionId !== undefined) for (const encoded of pendingPackets.current.splice(0)) enqueuePacket.current(encoded);
  }, [session?.sessionId]);

  useEffect(() => {
    if (connectionGeneration.current === connection.connectionGeneration) return;
    connectionGeneration.current = connection.connectionGeneration; outboundControlSequence.current = 0n;
    const current = sessionRef.current;
    if (current === null || openedPackage.current === null || terminal(current.status)) return;
    const resume = async () => {
      const binding = { abaEndpointId: current.abaEndpointId, hcEndpointId: registration.endpointId, sessionId: current.sessionId };
      outboundControlSequence.current += 1n;
      const packet = await createHCResumeStatePacket(identity, binding, outboundControlSequence.current,
        inboundFrameSequence.current, openedPackage.current?.material.generation);
      if (connection.socket.readyState !== WebSocket.OPEN) throw new Error('Connection closed before ResumeState');
      connection.socket.send(new Uint8Array(packet).buffer);
      if (inboundFrameSequence.current > 0n && openedPackage.current !== null) {
        const ack = await createHCAckFramePacket(identity, binding, Direction.ABA_TO_HC,
          inboundFrameSequence.current, openedPackage.current.material.generation);
        connection.socket.send(new Uint8Array(ack).buffer);
      }
    };
    void resume().catch(() => markUncertain('会话恢复失败。请检查执行状态，不会自动重新发送消息。'));
  }, [connection, identity, registration.endpointId, markUncertain]);
  useEffect(() => {
    if (!online && queuedPrompt.current !== null) {
      queuedPrompt.current = null; setError('连接已断开，未发送的草稿已保留。恢复连接后请再次发送。');
    }
  }, [online]);
  useEffect(() => () => { epoch.current += 1; zeroOpenedPackage(openedPackage.current); }, []);

  const withActiveRegistration = async <T,>(operation: (value: RegistrationSession) => Promise<T>): Promise<T> => {
    let current = registration; let refreshed = false;
    if (accessCredentialNeedsRefresh(current)) { current = await refreshEndpointSession(identity); refreshed = true; onRegistration(current); }
    try { return await operation(current); }
    catch (cause) {
      if (!(cause instanceof HcApiError) || cause.code !== 'ENDPOINT_CREDENTIAL_EXPIRED' || refreshed) throw cause;
      current = await refreshEndpointSession(identity); onRegistration(current); return operation(current);
    }
  };
  useEffect(() => {
    let active = true;
    void Promise.all([listABAEndpoints(identity, registration), listEndpointSessions()]).then(([values, sessions]) => {
      if (!active) return;
      endpointsRef.current = values; setEndpoints(values);
      setSelectedABA((previous) => values.some((value) => value.id === previous) ? previous : values[0]?.id ?? '');
      setUnrecoverable(sessions.filter((candidate) => candidate.hcEndpointId === registration.endpointId &&
        candidate.sessionId !== sessionRef.current?.sessionId && !terminal(candidate.status)));
    }).catch((cause: unknown) => { if (active) setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '无法读取执行设备，请检查连接或刷新设备列表。'); });
    return () => { active = false; };
  }, [identity, registration, reload]);

  const transmit = async (text: string) => {
    const current = sessionRef.current;
    if (actionBusy.current || awaiting.current !== null || blocked || current?.status !== 'ACTIVE' || openedPackage.current === null) return;
    const aba = endpointsRef.current.find((value) => value.id === current.abaEndpointId);
    if (aba === undefined || connection.socket.readyState !== WebSocket.OPEN) { setError('连接不可用，草稿未发送。'); return; }
    actionBusy.current = true; setBusy(true); setError(null);
    const requestId = crypto.randomUUID();
    try {
      outboundFrameSequence.current += 1n;
      const request = textEncoder.encode(JSON.stringify({ id: requestId, jsonrpc: '2.0', method: 'session/prompt',
        params: { prompt: [{ text, type: 'text' }], sessionId: current.sessionId } }));
      const packet = await createHCToABAFramePacket(identity, openedPackage.current,
        { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: current.sessionId }, request, outboundFrameSequence.current);
      if (connection.socket.readyState !== WebSocket.OPEN) throw new Error('Connection closed before dispatch');
      awaiting.current = requestId;
      connection.socket.send(new Uint8Array(packet).buffer);
      updateMessages((previous) => startTurn(previous, requestId, text)); setResponding(true); setViewId(null);
      if (draftRef.current.trim() === text) onDraftChange('');
    } catch { markUncertain('加密发送未能确认，草稿已保留。为避免重复执行，请检查执行端后结束会话。'); }
    finally { actionBusy.current = false; setBusy(false); }
  };
  const transmitRef = useRef(transmit);
  useEffect(() => { transmitRef.current = transmit; });
  useEffect(() => {
    if (busy || !online || blocked || !keyReady || session?.status !== 'ACTIVE' || queuedPrompt.current === null) return;
    const text = queuedPrompt.current; queuedPrompt.current = null; void transmitRef.current(text);
  }, [busy, online, blocked, keyReady, session?.status]);

  const createAndSend = async () => {
    if (actionBusy.current || awaiting.current !== null || !online || draft.trim() === '') return;
    if (sessionRef.current !== null) { await transmit(draft.trim()); return; }
    if (selectedABA === '' || runtimeProfileId.trim() === '' || workspaceId.trim() === '') { setError('请先在顶部选择执行设备、Agent 和工作区。'); return; }
    actionBusy.current = true; setBusy(true); setError(null); queuedPrompt.current = draft.trim();
    try {
      const idempotencyKey = crypto.randomUUID();
      let current = await withActiveRegistration((value) => createEndpointSession(identity, value, {
        abaEndpointId: selectedABA, idempotencyKey, runtimeProfileId: runtimeProfileId.trim(), workspaceId: workspaceId.trim(),
      }));
      updateSession(current); setViewId(null);
      for (let attempt = 0; attempt < POLL_ATTEMPTS && current.status === 'CREATING'; attempt += 1) {
        await delay(); current = await getEndpointSession(current.sessionId); updateSession(current);
      }
      if (terminal(current.status)) { queuedPrompt.current = null; setError('执行端没有建立会话。草稿已保留，请检查本地配置。'); }
    } catch (cause) {
      queuedPrompt.current = null; setBlocked(true);
      setError(cause instanceof HcApiError ? `会话创建未确认：${cause.message}（${cause.code}）。请检查连接设置中的旧会话。` : '会话创建结果未确认。草稿已保留；请检查旧会话后再试。');
      setReload((value) => value + 1);
    } finally { actionBusy.current = false; setBusy(false); }
  };
  const closeSession = async (candidate: EndpointSessionSummary): Promise<boolean> => {
    const idempotencyKey = closeKeys.current.get(candidate.sessionId) ?? crypto.randomUUID();
    closeKeys.current.set(candidate.sessionId, idempotencyKey);
    let current = await withActiveRegistration((active) => closeEndpointSession(identity, active, candidate.sessionId, idempotencyKey));
    for (let attempt = 0; attempt < POLL_ATTEMPTS && !terminal(current.status); attempt += 1) { await delay(); current = await getEndpointSession(candidate.sessionId); }
    if (sessionRef.current?.sessionId === candidate.sessionId) updateSession(current);
    if (!terminal(current.status)) { setError('关闭请求已提交，但执行端尚未确认结束。请稍后再次检查，不会提前创建新会话。'); return false; }
    await secureStore?.deleteSessionInbox(candidate.sessionId);
    setUnrecoverable((values) => values.filter((value) => value.sessionId !== candidate.sessionId));
    if (sessionRef.current?.sessionId === candidate.sessionId) {
      epoch.current += 1; pendingPackets.current = [];
      queuedPrompt.current = null; updateMessages((values) => settleTurn(values, awaiting.current, 'uncertain'));
      awaiting.current = null; setResponding(false); zeroOpenedPackage(openedPackage.current); openedPackage.current = null; setKeyReady(false);
    }
    return true;
  };
  const endOrNew = async (startNew: boolean) => {
    if (actionBusy.current) return;
    actionBusy.current = true; setBusy(true); setError(null); queuedPrompt.current = null;
    try {
      const current = sessionRef.current;
      if (current !== null && !terminal(current.status) && !(await closeSession(current))) return;
      if (!startNew) return;
      if (current !== null && messagesRef.current.length > 0) {
        const snapshot: SavedConversation = { id: current.sessionId, title: conversationTitle(messagesRef.current.find((value) => value.role === 'user')?.text ?? ''), detail: '已结束 · 本页只读', agent: current.runtimeProfileId, messages: messagesRef.current };
        setSaved((values) => {
          const result = [snapshot, ...values].slice(0, MAX_SAVED_CONVERSATIONS);
          while (result.length > 1 && result.reduce((sum, item) => sum + item.messages.reduce((size, value) => size + value.text.length, 0), 0) > 2_097_152) result.pop();
          return result;
        });
      }
      epoch.current += 1; pendingPackets.current = []; zeroOpenedPackage(openedPackage.current); openedPackage.current = null;
      lastPackageSequence.current = 0n; outboundFrameSequence.current = 0n; inboundFrameSequence.current = 0n;
      updateSession(null); updateMessages(() => []); setKeyReady(false); setBlocked(false); setResponding(false); awaiting.current = null; setViewId(null); onDraftChange('');
    } catch (cause) { setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '会话关闭未确认，请检查执行端后再试。'); }
    finally { actionBusy.current = false; setBusy(false); }
  };
  const closeOld = async (candidate: EndpointSessionSummary) => {
    if (actionBusy.current) return;
    actionBusy.current = true; setBusy(true); setError(null);
    try { if (await closeSession(candidate)) { if (sessionRef.current === null) setBlocked(false); } }
    catch (cause) { setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '旧会话关闭未确认。'); }
    finally { actionBusy.current = false; setBusy(false); }
  };
  const active = session !== null && !terminal(session.status);
  const archive = saved.find((value) => value.id === viewId);
  const title = conversationTitle(messages.find((value) => value.role === 'user')?.text ?? '');
  const full = messages.length >= MAX_MESSAGES || messages.reduce((sum, value) => sum + value.text.length, 0) >= 1_048_576;
  const readOnly = archive !== undefined || (session !== null && terminal(session.status));
  const conversations = [...(session === null ? [] : [{ id: session.sessionId, title, detail: responding ? '正在回复' : terminal(session.status) ? '已结束 · 本页只读' : blocked ? '结果待确认' : '当前会话' }]), ...saved];
  const targetSettings = <div className="target-form"><h2>执行环境</h2><p>这里只选择本地已授权的配置，不下发命令或路径。</p>
    <label>执行设备<select value={selectedABA} disabled={active || busy} onChange={(event) => setSelectedABA(event.target.value)}>{endpoints.length === 0 ? <option value="">暂无在线设备</option> : endpoints.map((value) => <option key={value.id} value={value.id}>{value.name}</option>)}</select></label>
    <label>Agent（Runtime ID）<input value={runtimeProfileId} disabled={active || busy} onChange={(event) => setRuntimeProfileId(event.target.value)} /></label>
    <label>工作区 ID<input value={workspaceId} disabled={active || busy} onChange={(event) => setWorkspaceId(event.target.value)} /></label>
    {active ? <p>新建对话后可以更换执行环境。</p> : <button className="secondary-button" type="button" disabled={busy} onClick={() => setReload((value) => value + 1)}>刷新设备列表</button>}
    {session !== null ? <details className="technical-details"><summary>会话诊断</summary><p>会话：{session.sessionId}<br />状态：{session.status}<br />密钥：{keyReady ? '已就绪' : '未就绪'}</p>{active ? <button type="button" className="secondary-button" disabled={busy} onClick={() => void endOrNew(false)}>请求关闭当前会话</button> : null}</details> : null}
    {unrecoverable.length > 0 ? <details className="technical-details"><summary>检查 {unrecoverable.length} 个旧会话</summary><p>这些会话的页面密钥不可用，不能恢复对话内容。请确认后关闭。</p>{unrecoverable.map((value) => <div className="old-session" key={value.sessionId}><span>{value.sessionId.slice(0, 10)}… · {value.status}</span><button type="button" className="secondary-button" disabled={busy} onClick={() => void closeOld(value)}>关闭旧会话</button></div>)}</details> : null}
  </div>;
  return <ChatWorkspace draft={draft} onDraftChange={onDraftChange} onSubmit={() => void createAndSend()}
    onNewChat={() => void endOrNew(true)} onEndChat={active ? () => void endOrNew(false) : null}
    onOpenSettings={onOpenSettings} onSelectConversation={(id) => setViewId(id === session?.sessionId ? null : id)}
    conversations={conversations} selectedConversationId={viewId ?? session?.sessionId ?? null}
    messages={archive?.messages ?? messages} title={archive?.title ?? title} agent={archive?.agent ?? session?.runtimeProfileId ?? runtimeProfileId}
    online={online} connected busy={busy} responding={responding && archive === undefined}
    canSubmit={online && !blocked && !full && !readOnly && (session === null ? selectedABA !== '' && runtimeProfileId.trim() !== '' && workspaceId.trim() !== '' : keyReady && session.status === 'ACTIVE')}
    readOnly={readOnly} hasActiveSession={active} targetSettings={targetSettings} error={error}
    notice={!online ? '连接已断开。会话与草稿保留在本页；请打开连接设置重新连接，不会自动重发请求。' : full ? '本页会话达到显示上限，请新建对话。' : endpoints.length === 0 ? '还没有在线执行设备。请在顶部的执行环境中检查或刷新设备列表。' : session !== null && !keyReady && active ? '正在准备安全会话，请稍候。未发送的内容仍保留在草稿中。' : null} />;
}
function zeroOpenedPackage(value: OpenedSessionKeyPackage | null): void {
  if (value === null) return;
  value.hcToAbaKey.fill(0); value.abaToHcKey.fill(0); value.material.srk.fill(0); value.material.sessionNonce.fill(0);
}
