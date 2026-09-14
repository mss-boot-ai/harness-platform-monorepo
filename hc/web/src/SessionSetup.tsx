import { type IndexedDbSecureStore, type ReadyGatewayConnection } from '@harness/hc-core';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { closeEndpointSession, createEndpointSession, listABAEndpoints, listEndpointSessions, type RegistrationSession } from './api';
import { ChatWorkspace } from './chat/ChatWorkspace';
import { conversationTitle, MAX_MESSAGES } from './chat/model';
import { ConversationManager, type ManagerView } from './remote/conversation-manager';
import { ConversationStore, isTerminal } from './remote/conversation-store';
import type { EndpointAccess } from './remote/endpoint-access';
import type { RegisterEndpointShutdown } from './remote/endpoint-owner';
import { RuntimeActivity, RuntimeConfiguration, type PendingConfig } from './remote/RuntimeControls';
import { editDraft, finishDraft, type DraftEdits } from './remote/draft-edits';
import { TargetPicker } from './remote/TargetPicker';
import { executionTargetState, firstExecutionTarget } from './remote/execution-target';

const loading: ManagerView = { status: 'loading', online: false, creating: false, workspace: null, selectedId: null, conversations: [],
  endpoints: [], unrecoverable: [], recovery: {}, error: null, pendingWrites: 0 };
const noSubscription = () => () => undefined;
const loadingSnapshot = () => loading;

export function SessionSetup({ connection, access, registration, secureStore, initialDraft, onOpenSettings, registerShutdown }: {
  readonly connection: ReadyGatewayConnection | null; readonly access: EndpointAccess;
  readonly registration: RegistrationSession; readonly secureStore: IndexedDbSecureStore;
  readonly initialDraft: string; readonly onOpenSettings: () => void; readonly registerShutdown: RegisterEndpointShutdown;
}) {
  const [manager, setManager] = useState<ConversationManager | null>(null);
  const initial = useRef(initialDraft);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [edits, setEdits] = useState<DraftEdits>(() => new Map());
  const editVersion = useRef(0);
  const actions = useRef(new Set<string>());
  const [, refreshActions] = useState(0);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  useEffect(() => {
    let active = true;
    const next = new ConversationManager(new ConversationStore(secureStore.localVault(), registration.endpointId, access.identity),
      access.identity, registration.endpointId, {
        abas: () => access.authorized((value) => listABAEndpoints(access.identity, value)),
        sessions: () => access.authorized(() => listEndpointSessions()),
        create: (intent) => access.authorized((value) => createEndpointSession(access.identity, value, {
          abaEndpointId: intent.abaEndpointId, idempotencyKey: intent.id, runtimeProfileId: intent.runtimeProfileId, workspaceId: intent.workspaceId })),
        close: (id, operation) => access.authorized((value) => closeEndpointSession(access.identity, value, id, operation)),
      });
    const unregister = registerShutdown(() => next.dispose());
    setManager(next);
    void next.load().then(async () => {
      if (!active) return;
      if (initial.current !== '' && next.snapshot().workspace?.draft === '') {
        await next.draft(null, initial.current); initial.current = '';
      }
    }).catch(() => { if (active) setError('无法恢复本地加密记录。请保留浏览器数据并检查存储。'); });
    return () => { active = false; unregister(); void next.dispose(); };
  }, [access, registration.endpointId, secureStore, registerShutdown]);
  useEffect(() => {
    if (manager === null) return;
    if (connection === null) { manager.disconnect(); return; }
    let active = true;
    void manager.bind(connection).catch(() => { if (active) setError('会话授权或恢复未确认，请检查连接设置。'); });
    return () => { active = false; manager.disconnect(); };
  }, [manager, connection]);
  const view = useSyncExternalStore(manager?.subscribe ?? noSubscription, manager?.snapshot ?? loadingSnapshot);
  const selectedId = view.selectedId;
  const selected = view.conversations.find((item) => item.data.session.sessionId === selectedId);
  const value = selected?.data;
  const session = value?.session;
  const composeTarget = view.workspace?.target ?? null;
  const target = session === undefined ? composeTarget : { abaEndpointId: session.abaEndpointId, workspaceId: session.workspaceId, runtimeProfileId: session.runtimeProfileId };
  const targetState = executionTargetState(view.endpoints, target);
  const actualABA = composeTarget?.abaEndpointId ?? '';
  const runtimeProfileId = composeTarget?.runtimeProfileId ?? '';
  const workspaceId = composeTarget?.workspaceId ?? '';
  useEffect(() => {
    if (manager === null || view.status !== 'ready' || view.workspace?.target !== null || view.pendingWrites !== 0) return;
    const first = firstExecutionTarget(view.endpoints);
    if (first !== null) void manager.target(first).catch(() => { if (mounted.current) setError('项目选择尚未保存，请重新选择。'); });
  }, [manager, view.status, view.workspace?.target, view.pendingWrites, view.endpoints]);
  const edit = edits.get(selectedId);
  const draft = edit?.text ?? value?.draft ?? view.workspace?.draft ?? initialDraft;
  const terminal = session !== undefined && isTerminal(session.status);
  const recovering = selectedId === null ? null : view.recovery[selectedId] ?? null;
  const expired = value?.keys !== null && value?.keys !== undefined && value.keys.material.expiresAtMs <= BigInt(Date.now());
  const missingKey = value !== undefined && value.keys === null && session?.status === 'ACTIVE';
  const blocked = recovering !== null || selected?.fault !== null && selected?.fault !== undefined || value?.blocked !== null && value?.blocked !== undefined ||
    expired || missingKey || session?.status === 'UNCERTAIN' || session?.status === 'REKEY_REQUIRED' || session?.status === 'DRAINING';
  const readOnly = terminal || blocked;
  const responding = value?.awaiting !== null && value?.awaiting !== undefined;
  const deliveryPending = responding && (value?.outbox.length ?? 0) > 0;
  const pendingRequest = value?.requests.find((item) => item.kind === 'config');
  const pendingConfig: PendingConfig | null = pendingRequest?.option === undefined || pendingRequest.requestedValue === undefined ? null : {
    id: pendingRequest.id, option: pendingRequest.option, value: pendingRequest.requestedValue };
  const full = value !== undefined && (value.messages.length >= MAX_MESSAGES || value.messages.reduce((sum, message) => sum + message.text.length, 0) >= 1_048_576);
  const workspaceBusy = value === undefined ? manager?.workspaceConflict(actualABA, workspaceId.trim()) === true
    : manager?.workspaceConflict(value.aba.id, value.session.workspaceId, value.session.sessionId) === true;
  const canSubmit = view.status === 'ready' && view.pendingWrites === 0 && view.online && !readOnly && !responding && !full && !workspaceBusy && !creating &&
    (value === undefined ? view.workspace?.creation === null && targetState.available
      : value.keys !== null && session?.status === 'ACTIVE' && value.requests.length === 0 &&
        (!session.requestedCapabilities.includes('remote-session-v1') || value.runtime.status === 'ready'));
  const actionBusy = actions.current.has(selectedId ?? 'compose');
  const perform = (operation: () => Promise<unknown>, message: string) => {
    const key = selectedId ?? 'compose';
    if (actions.current.has(key)) return;
    actions.current.add(key); refreshActions((count) => count + 1);
    setError(null);
    void Promise.resolve().then(operation).catch(() => { if (mounted.current) setError(message); }).finally(() => {
      actions.current.delete(key); if (mounted.current) refreshActions((count) => count + 1);
    });
  };
  const changeDraft = (text: string) => {
    const version = ++editVersion.current; const id = selectedId;
    setEdits((current) => editDraft(current, id, text, version));
    if (manager === null) return;
    void Promise.resolve().then(() => manager.draft(id, text)).then(() => { if (mounted.current) setEdits((current) => finishDraft(current, id, version, true)); })
      .catch(() => { if (mounted.current) setEdits((current) => finishDraft(current, id, version, false)); });
  };
  const selectConversation = (id: string | null) => {
    if (manager === null) return;
    setError(null);
    // Navigation takes effect before another input event; persistence cannot change its destination later.
    void manager.select(id).catch(() => { if (mounted.current) setError('会话选择尚未保存，请保留此页并检查存储。'); });
  };
  const submit = async () => {
    if (manager === null || !canSubmit || responding || draft.trim() === '') return;
    const text = draft.trim(); const submittedEdit = edit; const editedId = selectedId; let id = selectedId;
    await manager.draft(id, draft);
    if (id === null) {
      setCreating(true);
      try { id = await manager.create({ abaEndpointId: actualABA, runtimeProfileId: runtimeProfileId.trim(), workspaceId: workspaceId.trim(), draft }); }
      finally { if (mounted.current) setCreating(false); }
      actions.current.add(id); if (mounted.current) refreshActions((count) => count + 1);
      // This continuation belongs to this explicit send action; restored creation intents never take this path.
      try { await manager.waitReady(id); await manager.prompt(id, text); }
      finally { actions.current.delete(id); if (mounted.current) refreshActions((count) => count + 1); }
    } else {
      await manager.prompt(id, text);
    }
    if (mounted.current && submittedEdit !== undefined) setEdits((current) => finishDraft(current, editedId, submittedEdit.version, true));
  };
  const conversations = view.conversations.map((item) => ({ id: item.data.session.sessionId,
    title: conversationTitle(item.data.messages.find((message) => message.role === 'user')?.text ?? item.data.draft),
    detail: isTerminal(item.data.session.status) ? '已结束 · 只读' : item.fault !== null || item.data.blocked !== null || view.recovery[item.data.session.sessionId] !== undefined ? '需要检查'
      : item.data.runtime.permissions.some((permission) => permission.status === 'pending') ? '等待授权' : item.data.awaiting !== null ? '正在回复' : '已保存' }));
  const targetSettings = <div className="target-form">
    <TargetPicker endpoints={view.endpoints} target={target} disabled={value !== undefined || creating || !view.online || actionBusy}
      onChange={(next) => perform(() => manager!.target(next), '项目选择未能保存，请重新选择。')}
      onRefresh={() => perform(() => manager?.refresh() ?? Promise.resolve(), '无法刷新项目，请检查连接。')} />
    {view.workspace?.creation === null || view.workspace?.creation === undefined ? null : <section role="status"><p>上次创建尚未确认。可以使用原编号再次核对结果；不会自动发送草稿。</p>
      <button className="secondary-button" type="button" disabled={!view.online || view.creating} onClick={() => perform(async () => {
        const intent = manager?.snapshot().workspace?.creation;
        if (intent !== null && intent !== undefined) await manager?.create(intent, true);
      }, '原创建请求仍未确认，请保留草稿并检查服务。')}>确认原创建请求</button></section>}
    {session === undefined ? null : <details className="technical-details"><summary>会话诊断</summary><p>会话：{session.sessionId}<br />状态：{session.status}<br />密钥：{value?.keys === null ? '不可用' : '已保存'}</p></details>}
    {view.unrecoverable.length === 0 ? null : <details className="technical-details"><summary>检查 {view.unrecoverable.length} 个无法恢复的会话</summary>
      <p>当前浏览器没有这些会话的密钥。请在管理后台核对执行状态后处理，不会新建任务来代替它们。</p>
      {view.unrecoverable.map((item) => <p key={item.sessionId}>{item.sessionId.slice(0, 10)}… · {item.status}</p>)}</details>}
    {value === undefined || !value.session.requestedCapabilities.includes('remote-session-v1') ? null : <RuntimeConfiguration state={value.runtime} pending={pendingConfig}
      disabled={!view.online || readOnly || responding || actionBusy} onChange={(option, requested) => perform(() => manager!.configure(value.session.sessionId, option, requested), '配置修改未确认，请检查当前会话。')}
      onRefresh={() => perform(() => manager!.describeNow(value.session.sessionId), '无法读取执行端配置。')} />}
  </div>;
  const notice = edit?.failed === true ? '本对话草稿尚未保存。请保留此页，不要刷新；切换会话后仍可回来复制。'
    : view.status === 'loading' ? '正在恢复本地加密记录…' : view.pendingWrites > 0 || edit !== undefined ? '正在保存草稿与会话选择，请勿清除浏览器数据。'
    : recovering ?? selected?.fault ?? value?.blocked ?? (expired ? '会话密钥已过期，当前只读。需要新的授权密钥才能继续。'
      : missingKey ? '本地缺少此会话的恢复密钥，当前只读。请核对执行端状态。'
        : !view.online ? '连接已断开。历史和草稿在此浏览器中加密保留；重新连接后会先核对授权，再恢复原始消息。'
          : workspaceBusy ? '此工作区有另一个会话正在执行或需要确认。草稿已保留，请等待或选择不同工作区。'
            : deliveryPending ? '消息已加密保存，等待送达确认。'
              : value?.awaiting !== null && value?.awaiting !== undefined && value.outbox.length === 0 ? '消息已安全送达，正在等待执行结果。'
              : full ? '会话达到本地显示上限，请新建对话。' : value !== undefined && value.keys === null && !terminal ? '正在准备安全会话，草稿尚未发送。' : null);
  return <ChatWorkspace draft={draft} onDraftChange={changeDraft} onSubmit={() => perform(submit, '本次发送尚未确认，草稿已保留。请检查会话状态后再操作。')}
    onNewChat={() => selectConversation(null)} newChatDisabled={creating || view.creating}
    draftSaved={edit === undefined && view.pendingWrites === 0 && view.status === 'ready'} composerDisabled={creating || view.creating}
    onEndChat={session !== undefined && !terminal ? () => perform(() => manager!.close(session.sessionId), '关闭会话未确认，请核对执行端状态。') : null}
    {...(value?.runtime.cancelSupported && !readOnly && view.online ? { onCancelTurn: () => perform(() => manager!.cancel(value.session.sessionId), '停止请求未确认，请检查当前轮次。'), cancelPending: value.cancelPending } : {})}
    renderTurnActivity={(turnId) => value === undefined ? null : <RuntimeActivity key={value.session.sessionId} state={value.runtime} turnId={turnId}
      disabled={!view.online || readOnly || value.cancelPending || actionBusy}
      onDecision={(id, option) => perform(() => manager!.decide(value.session.sessionId, id, option), '权限请求已失效或提交未确认，请核对当前会话。')} />}
    onOpenSettings={onOpenSettings} onSelectConversation={selectConversation}
    conversations={conversations} selectedConversationId={selectedId} messages={value?.messages ?? []}
    title={conversationTitle(value?.messages.find((message) => message.role === 'user')?.text ?? '')}
    agent={targetState.runtime?.displayName || value?.runtime.agentName || '选择 Agent'} project={targetState.workspace?.displayName ?? '选择项目'}
    online={view.online} connected busy={creating || actionBusy} responding={responding && !deliveryPending} deliveryPending={deliveryPending}
    canSubmit={canSubmit} readOnly={readOnly} hasActiveSession={session !== undefined && !terminal} targetSettings={targetSettings}
    error={error ?? view.error} notice={notice} />;
}
