export type MessageState = 'streaming' | 'complete' | 'failed' | 'uncertain' | 'cancelled';
export interface ChatMessage {
  readonly id: string;
  readonly role: 'user' | 'assistant' | 'system';
  readonly text: string;
  readonly state: MessageState;
  readonly error?: string;
}
export interface ConversationItem {
  readonly id: string;
  readonly title: string;
  readonly detail: string;
  readonly archived?: boolean;
  readonly canArchive?: boolean;
}
export interface SavedConversation extends ConversationItem {
  readonly agent: string;
  readonly messages: readonly ChatMessage[];
}
export const MAX_REPLY_CHARACTERS = 262_144;
export const MAX_MESSAGES = 200;
export const MAX_SAVED_CONVERSATIONS = 20;

export function conversationTitle(prompt: string): string {
  const characters = Array.from(prompt.replace(/\s+/gu, ' ').trim());
  return characters.length > 32 ? `${characters.slice(0, 32).join('')}…` : characters.join('') || '新对话';
}
export function startTurn(messages: readonly ChatMessage[], id: string, text: string): readonly ChatMessage[] {
  return [...messages,
    { id: `user-${id}`, role: 'user', text, state: 'complete' },
    { id: `assistant-${id}`, role: 'assistant', text: '', state: 'streaming' },
  ];
}
export function settleTurn(messages: readonly ChatMessage[], id: string | null, state: MessageState): readonly ChatMessage[] {
  if (id === null) return messages;
  return messages.map((message) => message.id === `assistant-${id}` ? { ...message, state } : message);
}
function record(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
}
export function turnFailureMessage(payload: unknown): string {
  const error = record(record(payload)?.error);
  const data = record(error?.data);
  if (data?.executionState === 'unknown') return '执行结果不确定，请检查项目状态后结束这次运行。';
  if (data?.status === 504 || error?.message === 'PROVIDER_TIMEOUT') return '模型服务超时（504），请稍后继续。';
  if (data?.status === 429) return '模型服务暂时限流，请稍后继续。';
  if (data?.status === 401 || data?.status === 403) return '模型服务认证失败，请检查执行端的模型设置。';
  if (data?.runtimeStopped === true) return 'Agent 运行环境已停止，请结束旧运行后继续。';
  return '这一轮未能完成，历史已保留，可以继续发送新的消息。';
}
/** Presentation only: call after the existing signed frame verification and durable inbox ACK. */
export function receiveAcp(
  messages: readonly ChatMessage[], payload: unknown, requestId: string | null,
): { readonly messages: readonly ChatMessage[]; readonly completed: boolean; readonly failed: boolean } {
  if (Array.isArray(payload)) {
    let result = { messages, completed: false, failed: false };
    for (const item of payload) {
      const next = receiveAcp(result.messages, item, result.completed ? null : requestId);
      result = { messages: next.messages, completed: result.completed || next.completed, failed: result.failed || next.failed };
    }
    return result;
  }
  const value = record(payload);
  if (value === null || requestId === null) return { messages, completed: false, failed: false };
  if (value.id === requestId && ('result' in value || 'error' in value)) {
    const failed = 'error' in value;
    const uncertain = record(record(value.error)?.data)?.executionState === 'unknown';
    const settled = settleTurn(messages, requestId, failed ? uncertain ? 'uncertain' : 'failed' : record(value.result)?.stopReason === 'cancelled' ? 'cancelled' : 'complete');
    return { messages: failed ? settled.map((message) => message.id === `assistant-${requestId}` ? { ...message, error: turnFailureMessage(value) } : message) : settled, completed: true, failed };
  }
  const params = record(value.params);
  const update = record(params?.update);
  const content = record(update?.content);
  // Thought/tool payloads are not ordinary assistant text. Do not leak raw protocol JSON into the UI.
  if (value.method !== 'session/update' || update?.sessionUpdate !== 'agent_message_chunk' ||
    content?.type !== 'text' || typeof content.text !== 'string') {
    return { messages, completed: false, failed: false };
  }
  const text = content.text;
  return {
    messages: messages.map((message) => message.id === `assistant-${requestId}`
      ? { ...message, text: (message.text + text).slice(0, MAX_REPLY_CHARACTERS) } : message),
    completed: false, failed: false,
  };
}
export function shouldSendOnEnter(input: {
  readonly key: string; readonly shiftKey: boolean; readonly altKey: boolean;
  readonly isComposing: boolean; readonly keyCode: number; readonly repeat: boolean;
}): boolean {
  return input.key === 'Enter' && !input.shiftKey && !input.altKey && !input.isComposing && input.keyCode !== 229 && !input.repeat;
}
export function safeLink(value: string): string | null {
  try {
    const url = new URL(value);
    return ['https:', 'http:', 'mailto:'].includes(url.protocol) ? url.href : null;
  } catch { return null; }
}

/** Polls and key-package processing may finish out of order. Never regress a settled phase. */
export function mergeSessionObservation<T extends { readonly sessionId: string; readonly status: string }>(
  current: T | null, observed: T | null,
): T | null {
  if (current === null || observed === null || current.sessionId !== observed.sessionId) return observed;
  const early = ['CREATING', 'WAITING_KEY'];
  const terminal = ['CLOSED', 'FAILED', 'ABA_REVOKED'];
  if (terminal.includes(current.status)) return current;
  if (current.status === 'WAITING_KEY' && observed.status === 'CREATING') return current;
  if (current.status === 'ACTIVE' && early.includes(observed.status)) return current;
  if (current.status === 'REKEY_REQUIRED' && early.includes(observed.status)) return current;
  if (current.status === 'DRAINING' && !terminal.includes(observed.status)) return current;
  if (current.status === 'UNCERTAIN' && observed.status !== 'DRAINING' && !terminal.includes(observed.status)) return current;
  return observed;
}
