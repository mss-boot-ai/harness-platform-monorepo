import { type IndexedDbSecureStore, type ReadyGatewayConnection } from '@harness/hc-core';
import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { closeEndpointSession, createEndpointSession, getEndpointSession, getEndpointSessionStatuses, inspectSessionCreation, listABAEndpoints, listEndpointSessions, type RegistrationSession } from './api';
import { ChatWorkspace } from './chat/ChatWorkspace';
import { conversationTitle, MAX_MESSAGES } from './chat/model';
import { ConversationActionCancelled, ConversationManager, type ManagerView } from './remote/conversation-manager';
import { ConversationStore, isTerminal } from './remote/conversation-store';
import type { EndpointAccess } from './remote/endpoint-access';
import type { RegisterEndpointShutdown } from './remote/endpoint-owner';
import { RuntimeActivity, RuntimeConfiguration, type PendingConfig } from './remote/RuntimeControls';
import { editDraft, finishDraft, type DraftEdits } from './remote/draft-edits';
import { TargetPicker } from './remote/TargetPicker';
import { executionTargetState, firstExecutionTarget } from './remote/execution-target';
import { RecoveryActions } from './remote/RecoveryActions';

const loading: ManagerView = { status: 'loading', online: false, creating: false, workspace: null, selectedId: null, conversations: [],
  runs: [], storageIssues: [], endpoints: [], unrecoverable: [], recovery: {}, error: null, pendingWrites: 0 };
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
        status: (id) => access.authorized((value) => getEndpointSession(access.identity, value, id)),
        statuses: (ids) => access.authorized((value) => getEndpointSessionStatuses(access.identity, value, ids)),
        creation: (id, cancel) => access.authorized((value) => inspectSessionCreation(access.identity, value, id, cancel)),
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
  const creating = view.creating;
  const selectedId = view.selectedId;
  const conversation = view.conversations.find((item) => item.id === selectedId);
  const selectedRunId = conversation?.activeRunId ?? null;
  const selected = view.runs.find((item) => item.data.session.sessionId === selectedRunId);
  const value = selected?.data;
  const session = value?.session;
  const composeTarget = conversation?.target ?? view.workspace?.target ?? null;
  const target = composeTarget;
  const targetState = executionTargetState(view.endpoints, target);
  const actualABA = composeTarget?.abaEndpointId ?? '';
  const runtimeProfileId = composeTarget?.runtimeProfileId ?? '';
  const workspaceId = composeTarget?.workspaceId ?? '';
  useEffect(() => {
    if (manager === null || view.status !== 'ready' || target !== null || selectedRunId !== null || view.pendingWrites !== 0 || manager.snapshot().selectedId !== selectedId) return;
    const first = firstExecutionTarget(view.endpoints);
    if (first !== null) void manager.target(first).catch(() => { if (mounted.current) setError('项目选择尚未保存，请重新选择。'); });
  }, [manager, view.status, target, selectedId, selectedRunId, view.pendingWrites, view.endpoints]);
  const edit = edits.get(selectedId);
  const draft = edit?.text ?? conversation?.draft ?? view.workspace?.draft ?? initialDraft;
  const terminal = selectedRunId !== null && manager?.executionClosed(selectedRunId) === true;
  const recovering = selectedRunId === null ? null : view.recovery[selectedRunId] ?? null;
  const expired = value?.keys !== null && value?.keys !== undefined && value.keys.material.expiresAtMs <= BigInt(Date.now());
  const missingKey = value !== undefined && value.keys === null && session?.status === 'ACTIVE';
  const missingRun = selectedRunId !== null && value === undefined;
  const unsupportedRuntime = value !== undefined && value.session.requestedCapabilities.includes('remote-session-v1') && ['unsupported', 'failed'].includes(value.runtime.status);
  const blocked = missingRun || conversation?.fault !== null && conversation?.fault !== undefined || recovering !== null || selected?.fault !== null && selected?.fault !== undefined || value?.blocked !== null && value?.blocked !== undefined ||
    expired || missingKey || unsupportedRuntime || session?.status === 'UNCERTAIN' || session?.status === 'REKEY_REQUIRED' || session?.status === 'DRAINING';
  const readOnly = terminal || blocked || conversation?.archived === true;
  const responding = value?.awaiting !== null && value?.awaiting !== undefined;
  const deliveryPending = responding && (value?.outbox.length ?? 0) > 0;
  const pendingRequest = value?.requests.find((item) => item.kind === 'config');
  const pendingConfig: PendingConfig | null = pendingRequest?.option === undefined || pendingRequest.requestedValue === undefined ? null : {
    id: pendingRequest.id, option: pendingRequest.option, value: pendingRequest.requestedValue };
  const full = value !== undefined && (value.messages.length >= MAX_MESSAGES || value.messages.reduce((sum, message) => sum + message.text.length, 0) >= 1_048_576);
  const workspaceBusy = value === undefined ? manager?.workspaceConflict(actualABA, workspaceId.trim()) === true
    : manager?.workspaceConflict(value.aba.id, value.session.workspaceId, selectedId ?? undefined) === true;
  const canSubmit = view.status === 'ready' && view.pendingWrites === 0 && view.online && !readOnly && !responding && !full && !workspaceBusy && !creating &&
    (value === undefined ? (conversation?.creation ?? null) === null && targetState.available
      : value.keys !== null && session?.status === 'ACTIVE' && value.requests.length === 0 &&
        (!session.requestedCapabilities.includes('remote-session-v1') || value.runtime.status === 'ready'));
  const actionBusy = [...actions.current].some((key) => key.startsWith(`${selectedId ?? 'compose'}:`));
  const recoveryBusy = actions.current.has(`${selectedId ?? 'compose'}:recovery`);
  const perform = (operation: () => Promise<unknown>, message: string, id = selectedId, kind = 'action') => {
    const key = `${id ?? 'compose'}:${kind}`;
    if (actions.current.has(key)) return;
    actions.current.add(key); refreshActions((count) => count + 1);
    setError(null);
    void Promise.resolve().then(operation).catch((cause: unknown) => { if (!(cause instanceof ConversationActionCancelled) && mounted.current && (manager?.snapshot().selectedId ?? null) === id) setError(message); }).finally(() => {
      actions.current.delete(key); if (mounted.current) refreshActions((count) => count + 1);
    });
  };
  const changeDraft = (text: string) => {
    const version = ++editVersion.current; let id = selectedId;
    if (id === null && manager !== null && view.status === 'ready') {
      const creating = manager.newConversation(target, text);
      id = manager.snapshot().selectedId;
      void creating.catch(() => { if (mounted.current) setError('新对话还未保存，请保留当前草稿。'); });
    }
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
  const newChat = () => {
    if (manager === null) return;
    setError(null);
    void manager.newConversation(targetState.available ? target : null).catch(() => { if (mounted.current) setError('新对话尚未保存，请检查浏览器存储。'); });
  };
  const submit = async () => {
    if (manager === null || !canSubmit || responding || draft.trim() === '') return;
    const text = draft.trim(); const submittedEdit = edit; const editedId = selectedId; let id = selectedId;
    if (id === null) {
      const starting = manager.newConversation(target, draft); id = manager.snapshot().selectedId;
      if (id === null) throw new Error('Local conversation was not created');
      await starting;
    } else await manager.draft(id, draft);
    const sendKey = `${id}:send`; actions.current.add(sendKey); if (mounted.current) refreshActions((count) => count + 1);
    try {
      if (selectedRunId === null) {
        const started = await manager.create({ abaEndpointId: actualABA, runtimeProfileId, workspaceId, draft }, false, id);
        await manager.waitReady(id, started.runId); await manager.prompt(id, text, started.runId);
      } else await manager.prompt(id, text, selectedRunId);
    } finally {
      actions.current.delete(sendKey); if (mounted.current) refreshActions((count) => count + 1);
    }
    if (mounted.current && submittedEdit !== undefined) setEdits((current) => finishDraft(current, editedId, submittedEdit.version, true));
  };
  const conversations = view.conversations.map((item) => {
    const run = view.runs.find((run) => run.data.session.sessionId === item.activeRunId);
    const first = item.runIds.flatMap((id) => view.runs.find((run) => run.data.session.sessionId === id)?.data.messages ?? []).find((message) => message.role === 'user');
    return { id: item.id, title: item.title ?? conversationTitle(first?.text ?? item.draft), archived: item.archived, canArchive: item.creation === null,
      detail: item.creation !== null ? '创建待确认' : run === undefined ? item.activeRunId === null ? '草稿' : '需要检查'
        : isTerminal(run.data.session.status) ? '运行已结束' : run.fault !== null || run.data.blocked !== null || view.recovery[run.data.session.sessionId] !== undefined ? '需要检查'
          : run.data.runtime.permissions.some((permission) => permission.status === 'pending') ? '等待授权' : run.data.awaiting !== null ? '正在回复' : '已保存' };
  });
  const conversationRuns = conversation?.runIds.flatMap((id) => { const run = view.runs.find((item) => item.data.session.sessionId === id); return run === undefined ? [] : [run]; }) ?? [];
  const messages = conversationRuns.flatMap((run, index) => index === 0 ? run.data.messages : [
    { id: `run-${run.data.session.sessionId}`, role: 'system' as const, state: 'complete' as const, text: '新的运行 · 原有历史已保留' }, ...run.data.messages]);
  const targetSettings = <div className="target-form">
    <TargetPicker endpoints={view.endpoints} target={target} disabled={selectedRunId !== null || creating || !view.online || actionBusy || conversation?.archived === true}
      onChange={(next) => perform(() => manager!.target(next), '项目选择未能保存，请重新选择。')}
      onRefresh={() => perform(() => manager?.refresh() ?? Promise.resolve(), '无法刷新项目，请检查连接。')} />
    {session === undefined ? null : <details className="technical-details"><summary>会话诊断</summary><p>会话：{session.sessionId}<br />状态：{session.status}<br />密钥：{value?.keys === null ? '不可用' : '已保存'}</p></details>}
    {view.unrecoverable.length === 0 ? null : <details className="technical-details"><summary>检查 {view.unrecoverable.length} 个无法恢复的会话</summary>
      <p>这些运行缺少本地恢复记录。可以核对状态并结束运行，再开始新的工作。</p>
      {view.unrecoverable.map((item) => <p key={item.sessionId}>{item.sessionId.slice(0, 10)}… · {item.status} <button className="secondary-button" type="button"
        onClick={() => perform(() => manager!.closeUnrecoverable(item.sessionId), '旧运行尚未确认结束，请稍后核对。', selectedId, 'recovery')}>结束此旧运行</button></p>)}</details>}
    {view.storageIssues.length === 0 ? null : <details className="technical-details"><summary>有 {view.storageIssues.length} 份记录需要恢复</summary>
      <p>原始数据已保留。可以继续使用未受影响的对话；请保留浏览器数据。</p></details>}
    {value === undefined || !value.session.requestedCapabilities.includes('remote-session-v1') ? null : <RuntimeConfiguration state={value.runtime} pending={pendingConfig}
      disabled={!view.online || readOnly || responding || actionBusy} onChange={(option, requested) => perform(() => manager!.configure(selectedId!, option, requested, value.session.sessionId), '配置修改未确认，请检查当前会话。')}
      onRefresh={() => perform(() => manager!.describeNow(selectedId!), '无法读取执行端配置。')} />}
  </div>;
  const notice = edit?.failed === true ? '本对话草稿尚未保存。请保留此页，不要刷新；切换会话后仍可回来复制。'
    : view.status === 'loading' ? '正在恢复本地加密记录…' : view.pendingWrites > 0 || edit !== undefined ? '正在保存草稿与会话选择，请勿清除浏览器数据。'
    : recovering ?? conversation?.fault ?? selected?.fault ?? value?.blocked ?? (unsupportedRuntime ? value?.runtime.configError ?? 'Agent 能力尚未就绪，可以检查状态或结束旧运行后继续。' : conversation?.archived ? '此对话已归档，可从对话操作中恢复。' : missingRun ? '这次运行的本地记录不可用，可以检查状态或结束旧运行后继续。' : expired ? '会话密钥已过期，当前只读。需要新的授权密钥才能继续。'
      : missingKey ? '本地缺少此会话的恢复密钥，当前只读。请核对执行端状态。'
        : !view.online ? '连接已断开。历史和草稿在此浏览器中加密保留；重新连接后会先核对授权，再恢复原始消息。'
          : workspaceBusy ? '此工作区有另一个会话正在执行或需要确认。草稿已保留，请等待或选择不同工作区。'
            : deliveryPending ? '消息已加密保存，等待送达确认。'
              : value?.awaiting !== null && value?.awaiting !== undefined && value.outbox.length === 0 ? '消息已安全送达，正在等待执行结果。'
              : full ? '会话达到本地显示上限，请新建对话。' : value !== undefined && value.keys === null && !terminal ? '正在准备安全会话，草稿尚未发送。' : null);
  return <ChatWorkspace draft={draft} onDraftChange={changeDraft} onSubmit={() => perform(submit, '本次发送尚未确认，草稿已保留。请检查会话状态后再操作。', selectedId, 'send')}
    onNewChat={newChat} newChatDisabled={view.status !== 'ready'}
    draftSaved={edit === undefined && view.pendingWrites === 0 && view.status === 'ready'} composerDisabled={view.status !== 'ready'}
    onEndChat={selectedRunId !== null && !terminal ? () => perform(() => manager!.close(selectedId!, selectedRunId), '关闭会话未确认，请核对执行端状态。', selectedId, 'recovery') : null}
    {...(value?.runtime.cancelSupported && !readOnly && view.online ? { onCancelTurn: () => perform(() => manager!.cancel(selectedId!, value.session.sessionId), '停止请求未确认，请检查当前轮次。', selectedId, 'recovery'), cancelPending: value.cancelPending } : {})}
    renderTurnActivity={(turnId) => {
      const run = conversationRuns.find((run) => run.data.messages.some((message) => message.id === `assistant-${turnId}`));
      return run === undefined ? null : <RuntimeActivity key={run.data.session.sessionId} state={run.data.runtime} turnId={turnId}
        disabled={!view.online || readOnly || run.data.session.sessionId !== selectedRunId || run.data.cancelPending || actionBusy}
        onDecision={(id, option) => perform(() => manager!.decide(selectedId!, id, option, run.data.session.sessionId), '权限请求已失效或提交未确认，请核对当前会话。')} />;
    }}
    recoveryActions={<RecoveryActions key={selectedId ?? 'compose'} conversation={conversation ?? null} blocked={blocked} terminal={terminal} online={view.online} busy={recoveryBusy}
      onCheckCreation={(cancel) => perform(() => manager!.checkCreation(selectedId!, cancel), '原创建尚未确认，请保留草稿并稍后再检查。', selectedId, 'recovery')}
      onInspect={() => perform(() => manager!.inspect(selectedId!), '无法核对当前运行，请稍后重试。', selectedId, 'recovery')}
      onClose={() => perform(() => manager!.close(selectedId!, selectedRunId ?? undefined), '旧运行尚未确认结束。', selectedId, 'recovery')}
      onContinue={() => perform(() => manager!.continueConversation(selectedId!), '旧运行尚未确认结束，暂时无法继续。', selectedId, 'recovery')}
      onReconnect={onOpenSettings} onNew={newChat} />}
    onRenameConversation={(id, title) => perform(() => manager!.rename(id, title), '对话名称未能保存。', id)}
    onArchiveConversation={(id, archived) => perform(() => manager!.archive(id, archived), '归档状态未能保存。', id)}
    onDeleteConversation={(id) => perform(() => manager!.remove(id), '请先结束这份对话的运行并确认投递状态，再删除记录。', id, 'recovery')}
    onOpenSettings={onOpenSettings} onSelectConversation={selectConversation}
    conversations={conversations} selectedConversationId={selectedId} messages={messages}
    title={conversation?.title ?? conversationTitle(messages.find((message) => message.role === 'user')?.text ?? '')}
    agent={targetState.runtime?.displayName || value?.runtime.agentName || '选择 Agent'} project={targetState.workspace?.displayName ?? '选择项目'}
    online={view.online} connected busy={creating || actionBusy} responding={responding && !deliveryPending} deliveryPending={deliveryPending}
    lifecycleBusy={recoveryBusy}
    canSubmit={canSubmit} readOnly={readOnly} hasActiveSession={selectedRunId !== null && !terminal} targetSettings={targetSettings}
    error={error ?? view.error} notice={notice} />;
}
