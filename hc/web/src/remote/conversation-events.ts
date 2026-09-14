import { receiveAcp } from '../chat/model';
import type { Conversation } from './conversation-store';
import { closeTurnPermissions, confirmConfiguration, readDescriptor, receiveRuntime, record } from './runtime-state';
/** Called only for a verified, decrypted frame; persistence and transport ACK follow this projection. */
export function applyConversationEvent(initial: Conversation, payload: unknown, now = Date.now()): Conversation {
  const items: readonly unknown[] = Array.isArray(payload) ? payload : [payload];
  if (items.length === 0 || items.length > 128) throw new Error('Invalid bounded ACP batch');
  let value = initial;
  for (const item of items) {
    const message = record(item);
    if (message?.jsonrpc !== '2.0') throw new Error('Invalid ACP envelope');
    const params = record(message.params);
    if (typeof message.method === 'string' && (message.method.startsWith('session/') || message.method.startsWith('_mss/permission/')) && params?.sessionId !== value.session.sessionId) throw new Error('ACP session binding mismatch');
    const request = typeof message.id === 'string' ? value.requests.find((candidate) => candidate.id === message.id) : undefined;
    if (request !== undefined && ('result' in message || 'error' in message)) {
      let runtime = value.runtime;
      let blocked = value.blocked;
      if ('error' in message) {
        runtime = { ...runtime, status: request.kind === 'describe' ? 'unsupported' : runtime.status,
          configError: request.kind === 'describe' ? '执行端尚不支持此会话控制契约。' : '执行端拒绝了配置修改，原配置保持不变。' };
      } else if (request.kind === 'describe') {
        const next = readDescriptor(runtime, message.result, value.session.sessionId);
        if (runtime.processEpoch !== '' && next.processEpoch !== '' && runtime.processEpoch !== next.processEpoch) {
          blocked = '执行进程已经变化。无法证明原执行结果，不会自动重发任务。';
        }
        runtime = next;
      } else if (request.option?.method === 'session/set_config_option') {
        runtime = confirmConfiguration(runtime, message.result);
      } else {
        runtime = { ...runtime, status: 'pending' };
      }
      value = { ...value, runtime, blocked, requests: value.requests.filter((candidate) => candidate.id !== request.id) };
      continue;
    }
    const turnId = value.awaiting;
    const runtime = receiveRuntime(value.runtime, item, value.session.sessionId, turnId, now);
    const response = receiveAcp(value.messages, item, turnId);
    value = { ...value, runtime: response.completed && turnId !== null ? closeTurnPermissions(runtime, turnId) : runtime,
      messages: response.messages, awaiting: response.completed ? null : turnId,
      cancelPending: response.completed ? false : value.cancelPending,
      blocked: response.failed ? 'Agent 未能确认本轮正常完成。请核对执行结果，不会自动重试。' : value.blocked };
  }
  return value;
}
