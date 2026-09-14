import { fromBinary } from '@bufbuild/protobuf';
import {
  base64UrlDecode, base64UrlEncode, createHCAckFramePacket, createHCResumeStatePacket, createHCToABAFramePacket,
  createSessionKeyPackageAckPacket, Direction, openABAAckFramePacket, openABAToHCFramePacket,
  openABAUncertainErrorPacket, openSessionKeyPackagePacket, WirePacketSchema,
  type EndpointIdentity, type OpenedSessionKeyPackage,
} from '@harness/hc-core';
import type { EndpointSessionSummary } from '../api';
import { MAX_MESSAGES, mergeSessionObservation, settleTurn, startTurn } from '../chat/model';
import { applyConversationEvent } from './conversation-events';
import { ConversationStore, hex, isTerminal, MAX_OUTBOX, sequence, type Conversation, type RpcContext, type StoredConversation } from './conversation-store';
import { configRequest, markPermissionSubmitted, permissionDecision, type ConfigOption, type RpcId } from './runtime-state';

export interface ConversationTransport {
  readonly now: () => number;
  readonly send: (packet: Uint8Array) => boolean;
  readonly control: (encode: (controlSequence: bigint) => Promise<Uint8Array>) => Promise<void>;
}
export interface ConversationView {
  readonly data: Conversation; readonly pending: number; readonly fault: string | null;
}
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const MAX_PENDING_BYTES = 4 * 1024 * 1024;
export class ConversationController {
  private stored: StoredConversation;
  private queue: Promise<void> = Promise.resolve();
  private queuedBytes = 0;
  private pending = 0;
  private closed = false;
  private fault: string | null = null;
  private view: ConversationView;
  public constructor(stored: StoredConversation, private readonly store: ConversationStore,
    private readonly identity: EndpointIdentity, private readonly transport: ConversationTransport,
    private readonly changed: () => void) {
    this.stored = stored; this.view = { data: stored.value, pending: 0, fault: null };
  }
  public snapshot(): ConversationView { return this.view; }
  private emit(): void { this.view = { data: this.stored.value, pending: this.pending, fault: this.fault }; this.changed(); }
  private enqueue<T>(action: () => Promise<T>, size = 1): Promise<T> {
    if (this.closed || this.queuedBytes + size > MAX_PENDING_BYTES || this.pending >= 128) return Promise.reject(new Error('Conversation queue is unavailable'));
    this.queuedBytes += size; this.pending += 1; this.emit();
    const operation = this.queue.then(async () => {
      if (this.closed) throw new Error('Conversation controller disposed');
      return action();
    });
    this.queue = operation.then(() => undefined, () => undefined).finally(() => {
      this.queuedBytes -= size; this.pending -= 1; this.emit();
    });
    return operation;
  }
  private async persist(value: Conversation): Promise<void> {
    const next = { ...value, updatedAt: this.transport.now() };
    try {
      const revision = await this.store.write(next, this.stored.revision);
      this.stored = { revision, value: next }; this.emit();
    } catch (cause) {
      this.fault = '本地加密记录保存失败，已暂停发送。请保留此页并检查存储空间；不会覆盖较新的记录。';
      this.emit(); throw cause;
    }
  }
  private writable(value: Conversation): void {
    if (this.fault !== null || value.blocked !== null || value.reservation !== null || value.session.status !== 'ACTIVE' || value.keys === null ||
        value.keys.material.notBeforeMs > BigInt(this.transport.now()) || value.keys.material.expiresAtMs <= BigInt(this.transport.now())) throw new Error('Session is not writable');
    if (value.outbox.length >= MAX_OUTBOX) throw new Error('Unacknowledged packet capacity reached');
  }
  private binding() { return { sessionId: this.stored.value.session.sessionId, abaEndpointId: this.stored.value.aba.id, hcEndpointId: this.stored.value.session.hcEndpointId }; }
  /** All new packets reserve a nonce before crypto, then save exact bytes before dispatch. */
  private async dispatch(payload: Record<string, unknown>, project: (value: Conversation) => Conversation, operationId: string | null): Promise<void> {
    const initial = this.stored.value; this.writable(initial);
    const nextSequence = sequence(initial.outbound) + 1n;
    if (nextSequence > 0xffff_ffff_ffff_ffffn || initial.keys === null) throw new Error('Session sequence requires rekey');
    const plaintext = encoder.encode(JSON.stringify(payload));
    if (plaintext.length > 64 * 1024) throw new Error('Request exceeds bounded ACP input');
    await this.persist({ ...initial, outbound: nextSequence.toString(), reservation: nextSequence.toString() });
    let encoded: Uint8Array;
    try {
      encoded = await createHCToABAFramePacket(this.identity, initial.keys, this.binding(), plaintext, nextSequence, new Date(this.transport.now()));
      const packet = fromBinary(WirePacketSchema, encoded);
      if (packet.body.case !== 'encrypted') throw new Error('Expected encrypted packet');
      const messageId = hex(packet.body.value.messageId);
      await this.persist({ ...project(this.stored.value), reservation: null,
        outbox: [...this.stored.value.outbox, { sequence: nextSequence.toString(), messageId,
          encoded: base64UrlEncode(encoded), createdAt: this.transport.now(), operationId }],
        sentIds: [...this.stored.value.sentIds, { sequence: nextSequence.toString(), messageId, operationId }].slice(-256) });
    } catch (cause) {
      this.fault = '发送记录在加密过程中中断，已阻止序号重用。请核对执行状态后恢复或结束会话。'; this.emit(); throw cause;
    } finally { plaintext.fill(0); }
    // A disconnect after persistence is recoverable by exact-byte replay, never by a new Prompt.
    this.transport.send(encoded);
  }
  public setDraft(draft: string): Promise<void> {
    if (draft.length > 16_000) return Promise.reject(new Error('Draft exceeds limit'));
    return this.enqueue(() => this.persist({ ...this.stored.value, draft }), draft.length * 2 + 1);
  }
  public prompt(text: string): Promise<void> {
    const id = crypto.randomUUID();
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (value.awaiting !== null || value.requests.length > 0 || value.runtime.status !== 'ready' || text.trim() === '' ||
          value.messages.length >= MAX_MESSAGES || text.length > 16_000 || value.messages.reduce((sum, item) => sum + item.text.length, 0) >= 1_048_576) throw new Error('Cannot begin another turn');
      await this.dispatch({ jsonrpc: '2.0', id, method: 'session/prompt', params: { sessionId: value.session.sessionId, prompt: [{ type: 'text', text }] } },
        (current) => ({ ...current, awaiting: id, cancelPending: false, messages: startTurn(current.messages, id, text), draft: current.draft.trim() === text.trim() ? '' : current.draft }), id);
    }, text.length * 2 + 1);
  }
  public describe(): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (value.requests.some((item) => item.kind === 'describe')) return;
      const id = crypto.randomUUID();
      await this.dispatch({ jsonrpc: '2.0', id, method: '_mss/session/describe', params: { sessionId: value.session.sessionId } },
        (current) => ({ ...current, requests: [...current.requests, { id, kind: 'describe' }] }), id);
    });
  }
  public configure(option: ConfigOption, requested: string): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      const current = value.runtime.config.find((item) => item.id === option.id);
      if (current === undefined || value.awaiting !== null || value.requests.length > 0 || current.value !== option.value) throw new Error('Configuration changed or a turn is active');
      const id = crypto.randomUUID(); const request: RpcContext = { id, kind: 'config', option: current, requestedValue: requested };
      await this.dispatch(configRequest(current, requested, value.session.sessionId, id),
        (data) => ({ ...data, requests: [...data.requests, request] }), id);
    });
  }
  public cancel(): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (value.awaiting === null || value.cancelPending || !value.runtime.cancelSupported) return;
      await this.dispatch({ jsonrpc: '2.0', method: 'session/cancel', params: { sessionId: value.session.sessionId } },
        (current) => ({ ...current, cancelPending: true }), value.awaiting);
    });
  }
  public decide(id: RpcId, option: string | null): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      const permission = value.runtime.permissions.find((item) => item.id === id && item.status === 'pending');
      if (value.cancelPending || permission === undefined || permission.turnId !== value.awaiting || permission.receivedAt + 300_000 <= this.transport.now()) throw new Error('Permission request is no longer actionable');
      await this.dispatch(permissionDecision(value.runtime, id, option), (current) => ({ ...current, runtime: markPermissionSubmitted(current.runtime, id) }), value.awaiting);
    });
  }
  public observe(session: EndpointSessionSummary): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (session.sessionId !== value.session.sessionId || session.hcEndpointId !== value.session.hcEndpointId || session.abaEndpointId !== value.session.abaEndpointId ||
          session.runtimeProfileId !== value.session.runtimeProfileId || session.workspaceId !== value.session.workspaceId) throw new Error('Session observation binding mismatch');
      const next = mergeSessionObservation(value.session, session);
      if (next === null) throw new Error('Missing session');
      if (isTerminal(next.status)) {
        await this.persist({ ...value, session: next, keys: null, awaiting: null, requests: [], outbox: [], reservation: null,
          cancelPending: false, messages: settleTurn(value.messages, value.awaiting, 'uncertain') });
        zeroKeys(value.keys); return;
      }
      await this.persist({ ...value, session: next });
    });
  }
  public closeOperation(): Promise<string> {
    return this.enqueue(async () => {
      if (this.stored.value.closeKey !== null) return this.stored.value.closeKey;
      const id = crypto.randomUUID(); await this.persist({ ...this.stored.value, closeKey: id }); return id;
    });
  }
  public ownsMessage(messageId: string): boolean { return this.stored.value.sentIds.some((item) => item.messageId === messageId); }
  public receive(encoded: Uint8Array): Promise<void> {
    if (encoded.length === 0 || encoded.length > 1_048_576) return Promise.reject(new Error('Packet exceeds receive limit'));
    const input = new Uint8Array(encoded);
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (isTerminal(value.session.status)) return;
      const packet = fromBinary(WirePacketSchema, input);
      if (packet.body.case === 'error') {
        const error = await openABAUncertainErrorPacket(value.aba.signingPublicJwk, input);
        if (error === null || !this.ownsMessage(hex(error.relatedMessageId))) return;
        await this.persist({ ...value, blocked: '执行结果不确定。请核对工作区状态，不会自动再次执行。',
          messages: settleTurn(value.messages, value.awaiting, 'uncertain'), awaiting: null, cancelPending: false }); return;
      }
      if (packet.body.case === 'ack') {
        const acknowledgment = await openABAAckFramePacket(value.aba.signingPublicJwk, this.binding(), input);
        if (acknowledgment === null) return;
        const highest = acknowledgment.highestContiguousSequence;
        if (highest > sequence(value.outbound) || (value.reservation !== null && highest >= sequence(value.reservation))) throw new Error('ACK exceeds dispatched sequence');
        if (highest <= sequence(value.outboundAck)) return;
        await this.persist({ ...value, outboundAck: highest.toString(), outbox: value.outbox.filter((item) => sequence(item.sequence) > highest) }); return;
      }
      if (packet.body.case === 'control') {
        const opened = await openSessionKeyPackagePacket(input, { ...this.binding(), abaSigningPublicJwk: value.aba.signingPublicJwk, identity: this.identity });
        if (opened === null) return;
        if (value.keys !== null && (!sameBytes(value.keys.material.keyId, opened.material.keyId) || !sameBytes(value.keys.material.srk, opened.material.srk))) { zeroKeys(opened); throw new Error('Conflicting key package for the same generation'); }
        if (value.keys === null) await this.persist({ ...value, keys: opened }); else zeroKeys(opened);
        await this.ackKey(); return;
      }
      if (packet.body.case !== 'encrypted' || value.keys === null) return;
      const frame = await openABAToHCFramePacket(value.aba.signingPublicJwk, value.keys, this.binding(), input);
      if (frame === null) return;
      try {
        const previous = sequence(value.inbound);
        if (frame.sequence > previous + 1n) {
          await this.transport.control((controlSequence) => createHCResumeStatePacket(this.identity, this.binding(), controlSequence, previous));
          return;
        }
        const known = value.inboundHashes.find((item) => sequence(item.sequence) === frame.sequence);
        if (frame.sequence <= previous) {
          if (known !== undefined && known.hash !== hex(frame.contentHash)) throw new Error('Conflicting duplicate frame');
        } else {
          const payload: unknown = JSON.parse(decoder.decode(frame.plaintext));
          const next = applyConversationEvent(value, payload, this.transport.now());
          await this.persist({ ...next, inbound: frame.sequence.toString(), inboundHashes: [...value.inboundHashes, { sequence: frame.sequence.toString(), hash: hex(frame.contentHash) }].slice(-256) });
        }
        // Semantic state/cursor is durable before ACK; replay cannot add another message.
        this.transport.send(await createHCAckFramePacket(this.identity, this.binding(), Direction.ABA_TO_HC, sequence(this.stored.value.inbound)));
      } finally { frame.plaintext.fill(0); }
    }, input.length).catch((cause: unknown) => {
      this.fault = '会话消息验证或保存失败，已暂停新操作。请检查连接与本地存储，不会自动重发为新任务。'; this.emit(); throw cause;
    });
  }
  private async ackKey(): Promise<void> {
    const keys = this.stored.value.keys;
    if (keys === null) return;
    await this.transport.control((controlSequence) => createSessionKeyPackageAckPacket(this.identity,
      { ...this.binding(), controlSequence, keyPackageId: keys.keyPackageId }));
  }
  /** Run after live authorization/status verification on a new connection. */
  public resume(): Promise<void> {
    return this.enqueue(async () => {
      const value = this.stored.value;
      if (isTerminal(value.session.status) || value.keys === null) return;
      if (value.session.status === 'WAITING_KEY') { await this.ackKey(); return; }
      if (value.session.status !== 'ACTIVE' && value.session.status !== 'UNCERTAIN') return;
      await this.transport.control((controlSequence) => createHCResumeStatePacket(this.identity, this.binding(), controlSequence, sequence(value.inbound), value.keys?.material.generation));
      if (sequence(value.inbound) > 0n) this.transport.send(await createHCAckFramePacket(this.identity, this.binding(), Direction.ABA_TO_HC, sequence(value.inbound)));
      if (value.reservation !== null || value.blocked !== null || this.fault !== null) return;
      if (value.keys.material.expiresAtMs <= BigInt(this.transport.now())) {
        await this.persist({ ...value, blocked: '会话密钥已到期；保留历史，只读显示。需要新的授权密钥才能继续。' }); return;
      }
      for (const entry of value.outbox) {
        if (entry.createdAt < this.transport.now() - 295_000 || entry.createdAt > this.transport.now() + 30_000) {
          await this.persist({ ...this.stored.value, blocked: '存在超过安全补传窗口的未确认请求。请核对执行状态，不会重新创建操作。' }); return;
        }
        const bytes = base64UrlDecode(entry.encoded); const packet = fromBinary(WirePacketSchema, bytes);
        if (packet.body.case !== 'encrypted' || hex(packet.body.value.sessionId) !== value.session.sessionId ||
            hex(packet.body.value.senderEndpointId) !== value.session.hcEndpointId || hex(packet.body.value.receiverEndpointId) !== value.aba.id ||
            packet.body.value.sequence !== sequence(entry.sequence) || hex(packet.body.value.messageId) !== entry.messageId ||
            !sameBytes(packet.body.value.keyId, value.keys.material.keyId)) throw new Error('Durable outbox binding mismatch');
        this.transport.send(bytes);
      }
    });
  }
  public idle(): Promise<void> { return this.queue; }
  public dispose(): Promise<void> {
    this.closed = true;
    return this.queue.finally(() => { zeroKeys(this.stored.value.keys); });
  }
}
function sameBytes(left: Uint8Array, right: Uint8Array): boolean { return left.length === right.length && left.every((byte, index) => byte === right[index]); }
function zeroKeys(value: OpenedSessionKeyPackage | null): void {
  if (value === null) return;
  value.hcToAbaKey.fill(0); value.abaToHcKey.fill(0); value.material.srk.fill(0); value.material.sessionNonce.fill(0);
}
