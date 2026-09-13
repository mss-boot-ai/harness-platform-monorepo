export type MessageState = 'streaming' | 'complete' | 'uncertain';
export interface ChatMessage {
  readonly id: string;
  readonly role: 'user' | 'assistant' | 'system';
  readonly text: string;
  readonly state: MessageState;
}
export interface ConversationItem {
  readonly id: string;
  readonly title: string;
  readonly detail: string;
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
    return { messages: settleTurn(messages, requestId, failed ? 'uncertain' : 'complete'), completed: true, failed };
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
