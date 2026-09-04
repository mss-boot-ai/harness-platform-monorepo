import {
  createSessionKeyPackageAckPacket,
  createHCToABAFramePacket,
  createHCAckFramePacket,
  Direction,
  IndexedDbSecureStore,
  openABAToHCFramePacket,
  openSessionKeyPackagePacket,
  type EndpointIdentity,
  type OpenedSessionKeyPackage,
  type ReadyGatewayConnection,
} from '@harness/hc-core';
import { useEffect, useRef, useState } from 'react';
import {
  accessCredentialNeedsRefresh,
  closeEndpointSession,
  createEndpointSession,
  getEndpointSession,
  HcApiError,
  listABAEndpoints,
  listEndpointSessions,
  refreshEndpointSession,
  type ABAEndpointSummary,
  type EndpointSessionSummary,
  type RegistrationSession,
} from './api';

const POLL_ATTEMPTS = 20;

export function SessionSetup({
  connection,
  identity,
  onRegistration,
  registration,
  secureStore,
}: {
  readonly connection: ReadyGatewayConnection;
  readonly identity: EndpointIdentity;
  readonly onRegistration: (registration: RegistrationSession) => void;
  readonly registration: RegistrationSession;
  readonly secureStore: IndexedDbSecureStore | null;
}) {
  const [endpoints, setEndpoints] = useState<readonly ABAEndpointSummary[]>([]);
  const [selectedABA, setSelectedABA] = useState('');
  const [runtimeProfileId, setRuntimeProfileId] = useState('test-agent');
  const [workspaceId, setWorkspaceId] = useState('harness-platform');
  const [session, setSession] = useState<EndpointSessionSummary | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [keyReady, setKeyReady] = useState(false);
  const [prompt, setPrompt] = useState('请回复这条 Harness 加密测试消息。');
  const [messages, setMessages] = useState<readonly string[]>([]);
  const [unrecoverableSessions, setUnrecoverableSessions] = useState<readonly EndpointSessionSummary[]>([]);
  const pendingPackets = useRef<Uint8Array[]>([]);
  const processing = useRef<Promise<void>>(Promise.resolve());
  const openedPackage = useRef<OpenedSessionKeyPackage | null>(null);
  const lastPackageSequence = useRef(0n);
  const outboundControlSequence = useRef(0n);
  const outboundFrameSequence = useRef(0n);
  const inboundFrameSequence = useRef(0n);

  useEffect(() => {
    const processPacket = async (encoded: Uint8Array) => {
      if (session === null) {
        pendingPackets.current.push(encoded);
        return;
      }
      const aba = endpoints.find((endpoint) => endpoint.id === session.abaEndpointId);
      if (aba === undefined) {
        throw new Error('Session ABA identity is unavailable');
      }
      const opened = await openSessionKeyPackagePacket(encoded, {
        abaEndpointId: aba.id,
        abaSigningPublicJwk: aba.signingPublicJwk,
        hcEndpointId: registration.endpointId,
        identity,
        sessionId: session.sessionId,
      });
      if (opened !== null) {
        if (opened.controlSequence <= lastPackageSequence.current) {
          throw new Error('SessionKeyPackage control sequence replayed');
        }
        zeroOpenedPackage(openedPackage.current);
        openedPackage.current = opened;
        lastPackageSequence.current = opened.controlSequence;
        setKeyReady(true);
        outboundControlSequence.current += 1n;
        const acknowledgment = await createSessionKeyPackageAckPacket(identity, {
          abaEndpointId: aba.id,
          controlSequence: outboundControlSequence.current,
          hcEndpointId: registration.endpointId,
          keyPackageId: opened.keyPackageId,
          sessionId: session.sessionId,
        });
        if (connection.socket.readyState !== WebSocket.OPEN) {
          throw new Error('Gateway connection closed before key acknowledgment');
        }
        connection.socket.send(new Uint8Array(acknowledgment).buffer);
        for (let attempt = 0; attempt < POLL_ATTEMPTS; attempt += 1) {
          const current = await getEndpointSession(session.sessionId);
          setSession(current);
          if (current.status === 'ACTIVE' || current.status === 'FAILED') {
            break;
          }
          await new Promise((resolve) => setTimeout(resolve, 250));
        }
        return;
      }
      if (openedPackage.current === null) {
        return;
      }
      const frame = await openABAToHCFramePacket(
        aba.signingPublicJwk,
        openedPackage.current,
        { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: session.sessionId },
        encoded,
      );
      if (frame !== null) {
        if (frame.sequence > inboundFrameSequence.current + 1n) {
          throw new Error('ABA frame sequence is not contiguous');
        }
        const value = JSON.parse(new TextDecoder().decode(frame.plaintext)) as Record<string, unknown>;
        if (secureStore === null) {
          throw new Error('HC inbox is unavailable');
        }
        const stored = await secureStore.putInboxFrame({
          contentHash: frame.contentHash,
          direction: Direction.ABA_TO_HC,
          messageId: frame.messageId,
          plaintext: frame.plaintext,
          receivedAt: new Date().toISOString(),
          sequence: frame.sequence,
          sessionId: session.sessionId,
        });
        if (frame.sequence === inboundFrameSequence.current + 1n) {
          inboundFrameSequence.current = frame.sequence;
        } else if (stored !== 'duplicate') {
          throw new Error('HC inbox cursor is inconsistent');
        }
        const acknowledgment = await createHCAckFramePacket(
          identity,
          { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: session.sessionId },
          Direction.ABA_TO_HC,
          inboundFrameSequence.current,
        );
        if (connection.socket.readyState !== WebSocket.OPEN) {
          throw new Error('Gateway connection closed before frame acknowledgment');
        }
        connection.socket.send(new Uint8Array(acknowledgment).buffer);
        if (stored === 'duplicate') {
          return;
        }
        const update = value.params as { update?: { content?: { text?: unknown } } } | undefined;
        const text = update?.update?.content?.text;
        if (typeof text === 'string') {
          setMessages((current) => [...current, `ABA: ${text}`]);
        } else if (value.result !== undefined) {
          setMessages((current) => [...current, 'ABA: turn completed']);
        }
      }
    };
    const schedule = (encoded: Uint8Array) => {
      processing.current = processing.current
        .then(() => processPacket(encoded))
        .catch(() => {
          setError('Session Key Package 验证失败。');
        });
    };
    const message = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer && event.data.byteLength <= 1_048_576) {
        schedule(new Uint8Array(event.data));
      }
    };
    connection.socket.addEventListener('message', message);
    if (session !== null) {
      const queued = pendingPackets.current.splice(0);
      for (const encoded of queued) {
        schedule(encoded);
      }
    }
    return () => connection.socket.removeEventListener('message', message);
  }, [connection.socket, endpoints, identity, registration.endpointId, secureStore, session]);

  useEffect(() => () => zeroOpenedPackage(openedPackage.current), []);

  useEffect(() => {
    let active = true;
    void Promise.all([listABAEndpoints(identity, registration), listEndpointSessions()])
      .then(([values, sessions]) => {
        if (active) {
          setEndpoints(values);
          setSelectedABA(values[0]?.id ?? '');
          setUnrecoverableSessions(sessions.filter((candidate) =>
            candidate.hcEndpointId === registration.endpointId &&
            !['CLOSED', 'FAILED', 'ABA_REVOKED'].includes(candidate.status),
          ));
        }
      })
      .catch((cause: unknown) => {
        if (active) {
          setError(
            cause instanceof HcApiError
              ? `${cause.message}（${cause.code}）`
              : '无法读取当前用户的 ABA Endpoint。',
          );
        }
      });
    return () => {
      active = false;
    };
  }, [identity, registration]);

  const create = async () => {
    if (selectedABA === '') {
      return;
    }
    setBusy(true);
    setError(null);
    setKeyReady(false);
    zeroOpenedPackage(openedPackage.current);
    openedPackage.current = null;
    lastPackageSequence.current = 0n;
    outboundFrameSequence.current = 0n;
    inboundFrameSequence.current = 0n;
    setMessages([]);
    try {
      const idempotencyKey = crypto.randomUUID();
      let activeRegistration = registration;
      let refreshed = false;
      if (accessCredentialNeedsRefresh(activeRegistration)) {
        activeRegistration = await refreshEndpointSession(identity);
        refreshed = true;
        onRegistration(activeRegistration);
      }
      const submit = (candidate: RegistrationSession) => createEndpointSession(identity, candidate, {
        abaEndpointId: selectedABA,
        idempotencyKey,
        runtimeProfileId: runtimeProfileId.trim(),
        workspaceId: workspaceId.trim(),
      });
      let current: EndpointSessionSummary;
      try {
        current = await submit(activeRegistration);
      } catch (cause) {
        if (!(cause instanceof HcApiError) || cause.code !== 'ENDPOINT_CREDENTIAL_EXPIRED' || refreshed) {
          throw cause;
        }
        activeRegistration = await refreshEndpointSession(identity);
        onRegistration(activeRegistration);
        current = await submit(activeRegistration);
      }
      setSession(current);
      for (let attempt = 0; attempt < POLL_ATTEMPTS && current.status === 'CREATING'; attempt += 1) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        current = await getEndpointSession(current.sessionId);
        setSession(current);
      }
    } catch (cause) {
      setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : 'Session 创建失败。');
    } finally {
      setBusy(false);
    }
  };

  const sendPrompt = async () => {
    if (session === null || openedPackage.current === null || prompt.trim() === '') {
      return;
    }
    const aba = endpoints.find((endpoint) => endpoint.id === session.abaEndpointId);
    if (aba === undefined || connection.socket.readyState !== WebSocket.OPEN) {
      setError('ABA 或 Gateway 连接不可用。');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      outboundFrameSequence.current += 1n;
      const text = prompt.trim();
      const request = textEncoder.encode(JSON.stringify({
        id: crypto.randomUUID(),
        jsonrpc: '2.0',
        method: 'session/prompt',
        params: { prompt: [{ text, type: 'text' }], sessionId: session.sessionId },
      }));
      const packet = await createHCToABAFramePacket(
        identity,
        openedPackage.current,
        { abaEndpointId: aba.id, hcEndpointId: registration.endpointId, sessionId: session.sessionId },
        request,
        outboundFrameSequence.current,
      );
      connection.socket.send(new Uint8Array(packet).buffer);
      setMessages((current) => [...current, `HC: ${text}`]);
      setPrompt('');
    } catch {
      setError('加密 Prompt 发送失败。');
    } finally {
      setBusy(false);
    }
  };

  const close = async () => {
    if (session === null) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const closed = await closeEndpointSession(session.sessionId);
      await secureStore?.deleteSessionInbox(session.sessionId);
      zeroOpenedPackage(openedPackage.current);
      openedPackage.current = null;
      setKeyReady(false);
      setSession(closed);
      setUnrecoverableSessions((current) => current.filter((candidate) => candidate.sessionId !== closed.sessionId));
    } catch (cause) {
      setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : 'Session 关闭失败。');
    } finally {
      setBusy(false);
    }
  };

  const closeUnrecoverable = async (sessionId: string) => {
    setBusy(true);
    setError(null);
    try {
      await closeEndpointSession(sessionId);
      await secureStore?.deleteSessionInbox(sessionId);
      setUnrecoverableSessions((current) => current.filter((candidate) => candidate.sessionId !== sessionId));
    } catch (cause) {
      setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '遗留 Session 关闭失败。');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="session-card" aria-labelledby="session-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">STEP 4 OF 4</p>
          <h2 id="session-title">创建 ACP Session</h2>
        </div>
        <span className={`status-pill ${keyReady ? 'success' : 'pending'}`}>
          {keyReady ? 'KEY READY' : session?.status ?? '需要本地策略'}
        </span>
      </div>
      <p className="platform-copy">
        Platform 只发送 ABA、Runtime 和 Workspace 的稳定 ID；真实命令、参数和路径只由 ABA 本地配置决定。
      </p>
      <div className="session-form">
        <label>
          ABA Endpoint
          <select value={selectedABA} onChange={(event) => setSelectedABA(event.target.value)}>
            {endpoints.map((endpoint) => (
              <option key={endpoint.id} value={endpoint.id}>{endpoint.name} · {endpoint.id.slice(0, 8)}</option>
            ))}
          </select>
        </label>
        <label>
          Runtime ID
          <input value={runtimeProfileId} onChange={(event) => setRuntimeProfileId(event.target.value)} />
        </label>
        <label>
          Workspace ID
          <input value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)} />
        </label>
        <button
          className="primary-button"
          type="button"
          disabled={busy || selectedABA === '' || runtimeProfileId.trim() === '' || workspaceId.trim() === ''}
          onClick={() => void create()}
        >
          {busy ? '正在等待 ABA 本地策略…' : '创建 Session'}
        </button>
      </div>
      {endpoints.length === 0 && error === null ? <p className="fine-print">没有可用的 ACTIVE ABA Endpoint。</p> : null}
      {session === null && unrecoverableSessions.length > 0 ? (
        <div className="message-list" aria-label="无法恢复密钥的遗留 Session">
          <p>以下 Session 的页面内存密钥已不可用，请关闭后重新创建：</p>
          {unrecoverableSessions.map((candidate) => (
            <div key={candidate.sessionId}>
              <p>Session {candidate.sessionId.slice(0, 10)}… · {candidate.status}</p>
              <button
                className="secondary-button"
                type="button"
                disabled={busy}
                onClick={() => void closeUnrecoverable(candidate.sessionId)}
              >
                关闭遗留 Session
              </button>
            </div>
          ))}
        </div>
      ) : null}
      {session === null ? null : (
        <>
          <p className="session-result">
            Session {session.sessionId.slice(0, 10)}… · {session.status}
            {keyReady ? ' · HPKE KEY READY' : ''}
          </p>
          {['CLOSED', 'FAILED', 'ABA_REVOKED'].includes(session.status) ? null : (
            <button className="secondary-button" type="button" disabled={busy} onClick={() => void close()}>
              {busy ? '正在处理…' : '关闭当前 Session'}
            </button>
          )}
        </>
      )}
      {keyReady ? (
        <div className="prompt-panel">
          <label>
            加密 ACP Prompt
            <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} />
          </label>
          <button className="primary-button" type="button" disabled={busy || prompt.trim() === ''} onClick={() => void sendPrompt()}>
            {busy ? '正在发送…' : '发送加密 Prompt'}
          </button>
          {messages.length === 0 ? null : (
            <div className="message-list" aria-live="polite">
              {messages.map((message, index) => <p key={`${index}-${message}`}>{message}</p>)}
            </div>
          )}
        </div>
      ) : null}
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
    </section>
  );
}

const textEncoder = new TextEncoder();

function zeroOpenedPackage(value: OpenedSessionKeyPackage | null): void {
  if (value === null) {
    return;
  }
  value.hcToAbaKey.fill(0);
  value.abaToHcKey.fill(0);
  value.material.srk.fill(0);
  value.material.sessionNonce.fill(0);
}
