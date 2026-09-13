/** Runtime-owned configuration and activity. Consume only authenticated, decrypted ACP messages. */
export type RpcId = string | number;
export type RuntimeStatus = 'pending' | 'ready' | 'unsupported' | 'failed';
export interface ConfigChoice { readonly value: string; readonly name: string; readonly group: string | null }
export interface ConfigOption {
  readonly id: string; readonly name: string; readonly category: string; readonly description: string;
  readonly value: string; readonly options: readonly ConfigChoice[];
  readonly method: 'session/set_config_option' | 'session/set_model' | 'session/set_mode';
}
export interface Usage { readonly used: number; readonly size: number }
export type PermissionKind = 'allow_once' | 'allow_always' | 'reject_once' | 'reject_always';
export interface PermissionOption { readonly id: string; readonly name: string; readonly kind: PermissionKind }
export interface Permission {
  readonly id: RpcId; readonly turnId: string; readonly title: string; readonly detail: string;
  readonly options: readonly PermissionOption[]; readonly receivedAt: number;
  readonly status: 'pending' | 'submitted' | 'closed'; readonly complete: boolean;
}
export interface ToolItem {
  readonly id: string; readonly turnId: string; readonly title: string; readonly kind: string;
  readonly status: string; readonly content: string; readonly input: string;
}
export interface PlanEntry { readonly content: string; readonly status: string; readonly priority: string }
export interface RuntimeState {
  readonly status: RuntimeStatus; readonly agentName: string; readonly processEpoch: string;
  readonly duplex: boolean; readonly cancelSupported: boolean; readonly config: readonly ConfigOption[]; readonly configRevision: number;
  readonly configError: string | null; readonly usage: Usage | null;
  readonly tools: readonly ToolItem[]; readonly permissions: readonly Permission[];
  readonly plan: readonly PlanEntry[]; readonly planTurnId: string | null;
  readonly diagnostics: readonly string[];
}
export function newRuntimeState(): RuntimeState {
  return { status: 'pending', agentName: '', processEpoch: '', duplex: false, cancelSupported: false, config: [], configRevision: 0,
    configError: null, usage: null, tools: [], permissions: [], plan: [], planTurnId: null, diagnostics: [] };
}
export function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
}
function text(value: unknown, maximum = 2048): string {
  return typeof value === 'string' ? value.slice(0, maximum) : '';
}
function validId(value: unknown): value is RpcId {
  return typeof value === 'string' ? value.length > 0 && value.length <= 256 : typeof value === 'number' && Number.isSafeInteger(value);
}
function jsonText(value: unknown, max = 16_384): { readonly text: string; readonly complete: boolean } {
  if (value === undefined) return { text: '', complete: true };
  const serialized = JSON.stringify(value, null, 2) ?? '';
  return { text: serialized.slice(0, max), complete: serialized.length <= max };
}
function appendDiagnostic(state: RuntimeState, message: string): RuntimeState {
  return { ...state, diagnostics: [...state.diagnostics.filter((value) => value !== message), message].slice(-4) };
}
/** Unknown option types are not editable. Invalid/duplicate options fail closed as a whole. */
export function parseConfigOptions(raw: unknown): readonly ConfigOption[] {
  if (!Array.isArray(raw) || raw.length > 32) throw new Error('Invalid runtime config list');
  const result: ConfigOption[] = [];
  const ids = new Set<string>();
  for (const value of raw) {
    const item = record(value);
    if (item === null || typeof item.id !== 'string' || item.id.length === 0 || item.id.length > 128 || ids.has(item.id)) throw new Error('Invalid runtime config identity');
    ids.add(item.id);
    // The current bridge only negotiates select. Boolean must not be fabricated as a select.
    if (item.type !== 'select') continue;
    if (typeof item.currentValue !== 'string' || !Array.isArray(item.options) || item.options.length > 128) throw new Error('Invalid runtime config value');
    const choices: ConfigChoice[] = [];
    const seen = new Set<string>();
    for (const rawOption of item.options) {
      const option = record(rawOption);
      if (option === null) throw new Error('Invalid runtime option');
      const entries = Array.isArray(option.options) ? option.options : [option];
      const group = Array.isArray(option.options) ? text(option.name, 128) || null : null;
      for (const entry of entries) {
        const choice = record(entry);
        if (choice === null || typeof choice.value !== 'string' || choice.value.length === 0 || choice.value.length > 256 || seen.has(choice.value) || choices.length >= 128) throw new Error('Invalid runtime choice');
        seen.add(choice.value);
        choices.push({ value: choice.value, name: text(choice.name, 128) || choice.value, group });
      }
    }
    if (!seen.has(item.currentValue)) throw new Error('Effective runtime value not announced');
    result.push({ id: item.id, name: text(item.name, 128) || item.id, category: text(item.category, 64),
      description: text(item.description), value: item.currentValue, options: choices, method: 'session/set_config_option' });
  }
  return result;
}
function legacyConfig(session: Record<string, unknown>): readonly ConfigOption[] {
  const result: ConfigOption[] = [];
  for (const [key, listKey, currentKey, optionKey, method, label] of [
    ['models', 'availableModels', 'currentModelId', 'modelId', 'session/set_model', '模型'],
    ['modes', 'availableModes', 'currentModeId', 'id', 'session/set_mode', '权限模式'],
  ] as const) {
    const group = record(session[key]);
    const entries = group?.[listKey];
    const value = group?.[currentKey];
    if (!Array.isArray(entries) || entries.length > 128 || typeof value !== 'string') continue;
    const options: ConfigChoice[] = [];
    const ids = new Set<string>();
    for (const raw of entries) {
      const item = record(raw);
      const id = item?.[optionKey];
      if (typeof id !== 'string' || id.length === 0 || id.length > 256 || ids.has(id)) throw new Error('Invalid legacy configuration');
      ids.add(id); options.push({ value: id, name: text(item?.name, 128) || id, group: null });
    }
    if (ids.has(value)) result.push({ id: key, name: label, category: key === 'models' ? 'model' : 'mode', description: '', value, options, method });
  }
  return result;
}
export function readDescriptor(state: RuntimeState, result: unknown, sessionId: string): RuntimeState {
  const root = record(result);
  const session = record(root?.session);
  const bridge = record(root?.bridge);
  const initialized = record(root?.initialize);
  if (session?.sessionId !== sessionId || initialized?.protocolVersion !== 1 || bridge?.protocolVersion !== 1 || bridge.duplex !== true || typeof bridge.processEpoch !== 'string') {
    return { ...state, status: 'unsupported', duplex: false, cancelSupported: false, configError: '执行端尚未提供受支持的会话能力，请升级连接器。' };
  }
  try {
    const config = session.configOptions === undefined ? legacyConfig(session) : parseConfigOptions(session.configOptions);
    const info = record(initialized.agentInfo);
    return { ...state, status: 'ready', duplex: true, cancelSupported: bridge.turnCancellation === true, agentName: text(info?.title, 128) || text(info?.name, 128),
      processEpoch: bridge.processEpoch, config, configRevision: state.configRevision + 1, configError: null };
  } catch {
    return { ...state, status: 'failed', duplex: false, cancelSupported: false, config: [], configError: '执行端配置不符合已协商的契约，未提供可修改选项。' };
  }
}
export function confirmConfiguration(state: RuntimeState, value: unknown): RuntimeState {
  try {
    return { ...state, config: parseConfigOptions(record(value)?.configOptions), configRevision: state.configRevision + 1, configError: null };
  } catch {
    return { ...state, configError: '配置响应未能确认。请重新读取执行端配置，不会把本地选择当作已生效。' };
  }
}
export function configRequest(option: ConfigOption, value: string, sessionId: string, id: string): Record<string, unknown> {
  if (!option.options.some((candidate) => candidate.value === value)) throw new Error('Unannounced config value');
  const params = option.method === 'session/set_config_option' ? { sessionId, configId: option.id, value }
    : option.method === 'session/set_model' ? { sessionId, modelId: value } : { sessionId, modeId: value };
  return { jsonrpc: '2.0', id, method: option.method, params };
}
export function permissionDecision(state: RuntimeState, id: RpcId, optionId: string | null): Record<string, unknown> {
  const permission = state.permissions.find((value) => value.id === id && value.status === 'pending');
  if (permission === undefined) throw new Error('Permission is no longer pending');
  const option = permission.options.find((value) => value.id === optionId);
  if (optionId !== null && (option === undefined || (!permission.complete && option.kind.startsWith('allow')))) throw new Error('Invalid permission choice');
  return { jsonrpc: '2.0', id, result: { outcome: optionId === null ? { outcome: 'cancelled' } : { outcome: 'selected', optionId } } };
}
export function markPermissionSubmitted(state: RuntimeState, id: RpcId): RuntimeState {
  return { ...state, permissions: state.permissions.map((item) => item.id === id ? { ...item, status: 'submitted' } : item) };
}
export function closeTurnPermissions(state: RuntimeState, turnId: string): RuntimeState {
  return { ...state, permissions: state.permissions.map((item) => item.turnId === turnId ? { ...item, status: 'closed' } : item) };
}
function contentText(raw: unknown): string {
  if (!Array.isArray(raw)) return '';
  const parts: string[] = [];
  for (const item of raw.slice(0, 16)) {
    const value = record(item);
    if (value?.type === 'content') {
      const content = record(value.content);
      if (content?.type === 'text') parts.push(text(content.text, 8192));
    } else if (value?.type === 'diff') {
      parts.push(`${text(value.path, 1024)}\n--- before\n${text(value.oldText, 8192)}\n+++ after\n${text(value.newText, 8192)}`);
    }
  }
  return parts.join('\n\n').slice(0, 32_768);
}
/** All notifications are session-bound; a selected sidebar conversation cannot accept another's events. */
export function receiveRuntime(state: RuntimeState, payload: unknown, sessionId: string, turnId: string | null, now = Date.now()): RuntimeState {
  if (Array.isArray(payload)) return payload.reduce<RuntimeState>((current, item) => receiveRuntime(current, item, sessionId, turnId, now), state);
  const message = record(payload);
  const params = record(message?.params);
  if (message === null || params?.sessionId !== sessionId) return state;
  if (message.method === '_mss/permission/closed' && validId(params.requestId)) {
    return { ...state, permissions: state.permissions.map((item) => item.id === params.requestId ? { ...item, status: 'closed' } : item) };
  }
  if (message.method === 'session/request_permission') {
    if (turnId === null || !validId(message.id) || state.permissions.some((item) => item.id === message.id)) return appendDiagnostic(state, '收到无法关联或重复的权限请求，未自动批准。');
    const rawOptions = params.options;
    const tool = record(params.toolCall);
    if (tool === null || !Array.isArray(rawOptions) || rawOptions.length === 0 || rawOptions.length > 16 || state.permissions.filter((value) => value.status !== 'closed').length >= 16) return appendDiagnostic(state, '权限请求超出安全显示范围，需检查执行端。');
    const options: PermissionOption[] = [];
    for (const raw of rawOptions) {
      const option = record(raw);
      if (typeof option?.optionId !== 'string' || option.optionId.length === 0 || option.optionId.length > 128 || typeof option.kind !== 'string' || !['allow_once', 'allow_always', 'reject_once', 'reject_always'].includes(option.kind) || options.some((value) => value.id === option.optionId)) return appendDiagnostic(state, '权限选项无效，未提供批准入口。');
      options.push({ id: option.optionId, name: text(option.name, 128) || option.optionId, kind: option.kind as PermissionKind });
    }
    // Full source action is shown as inert JSON, not a model-generated paraphrase. Truncation disables approval.
    const detail = jsonText(tool, 48 * 1024);
    const permission: Permission = { id: message.id, turnId, title: text(tool.title, 512) || 'Agent 请求工具权限', detail: detail.text,
      complete: detail.complete, options, receivedAt: now, status: 'pending' };
    return { ...state, permissions: [...state.permissions, permission].slice(-64) };
  }
  if (message.method !== 'session/update') return state;
  const update = record(params.update);
  if (update?.sessionUpdate === 'config_option_update') return confirmConfiguration(state, update);
  if (update?.sessionUpdate === 'current_mode_update' && typeof update.currentModeId === 'string') {
    return { ...state, config: state.config.map((item) => item.method === 'session/set_mode' && item.options.some((value) => value.value === update.currentModeId) ? { ...item, value: update.currentModeId as string } : item), configRevision: state.configRevision + 1 };
  }
  if (update?.sessionUpdate === 'usage_update') {
    const { used, size } = update;
    if (typeof used === 'number' && typeof size === 'number' && Number.isSafeInteger(used) && Number.isSafeInteger(size) && used >= 0 && size > 0) return { ...state, usage: { used, size } };
    return appendDiagnostic(state, '上下文用量数据无效，保留最后一次可信上报。');
  }
  if (turnId === null) return state;
  if (update?.sessionUpdate === 'plan' && Array.isArray(update.entries) && update.entries.length <= 64) {
    const plan: PlanEntry[] = update.entries.map((entry) => { const value = record(entry); return { content: text(value?.content), status: text(value?.status, 64), priority: text(value?.priority, 32) }; });
    return { ...state, plan, planTurnId: turnId };
  }
  if (update?.sessionUpdate === 'tool_call' || update?.sessionUpdate === 'tool_call_update') {
    const id = update.toolCallId;
    if (typeof id !== 'string' || id.length === 0 || id.length > 256) return state;
    const existing = state.tools.find((item) => item.id === id && item.turnId === turnId);
    const item: ToolItem = { id, turnId, title: typeof update.title === 'string' ? text(update.title, 512) : existing?.title ?? '工具调用',
      kind: text(update.kind, 64) || existing?.kind || 'other', status: text(update.status, 64) || existing?.status || 'pending',
      content: update.content === undefined ? existing?.content ?? '' : contentText(update.content),
      input: update.rawInput === undefined ? existing?.input ?? '' : jsonText(update.rawInput).text };
    return { ...state, tools: [...state.tools.filter((value) => value !== existing), item].slice(-100) };
  }
  return state;
}
