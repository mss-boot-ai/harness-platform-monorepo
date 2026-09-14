import { base64UrlDecode, base64UrlEncode, deriveSessionDirectionKeys, EncryptedLocalVault,
  parseP256PublicJwk, publicJwkThumbprint, type EndpointIdentity, type OpenedSessionKeyPackage, type SessionKeyMaterial } from '@harness/hc-core';
import type { ABAEndpointSummary, EndpointSessionSummary } from '../api';
import type { ChatMessage } from '../chat/model';
import { newRuntimeState, record, type ConfigOption, type RuntimeState } from './runtime-state';

export const MAX_CONVERSATIONS = 32;
export const MAX_OUTBOX = 16;
export const MAX_CONVERSATION_BYTES = 4 * 1024 * 1024;
export interface RpcContext { readonly id: string; readonly kind: 'describe' | 'config'; readonly option?: ConfigOption; readonly requestedValue?: string }
export interface OutboundPacket {
  readonly sequence: string; readonly messageId: string; readonly encoded: string;
  readonly createdAt: number; readonly operationId: string | null;
}
export interface Conversation {
  readonly version: 1; readonly session: EndpointSessionSummary; readonly aba: ABAEndpointSummary;
  readonly signingJkt: string; readonly kemJkt: string; readonly draft: string;
  readonly keys: OpenedSessionKeyPackage | null; readonly messages: readonly ChatMessage[];
  readonly runtime: RuntimeState; readonly awaiting: string | null; readonly blocked: string | null;
  readonly inbound: string; readonly outbound: string; readonly outboundAck: string;
  readonly reservation: string | null; readonly outbox: readonly OutboundPacket[];
  readonly requests: readonly RpcContext[]; readonly cancelPending: boolean;
  readonly sentIds: readonly { readonly sequence: string; readonly messageId: string; readonly operationId: string | null }[];
  readonly inboundHashes: readonly { readonly sequence: string; readonly hash: string }[];
  readonly closeKey: string | null; readonly updatedAt: number;
}
export interface StoredConversation { readonly revision: number; readonly value: Conversation }
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });
const statuses = ['CREATING', 'WAITING_KEY', 'ACTIVE', 'REKEY_REQUIRED', 'DRAINING', 'UNCERTAIN', 'FAILED', 'CLOSED', 'ABA_REVOKED'];
export function isTerminal(status: string): boolean { return ['CLOSED', 'FAILED', 'ABA_REVOKED'].includes(status); }
export function hex(bytes: Uint8Array): string { return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(''); }
export function idBytes(id: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(id) || /^0+$/u.test(id)) throw new Error('Invalid conversation identity');
  return Uint8Array.from(id.match(/../gu) ?? [], (byte) => Number.parseInt(byte, 16));
}
export function sequence(value: unknown): bigint {
  if (typeof value !== 'string' || !/^(0|[1-9][0-9]{0,19})$/u.test(value) || BigInt(value) > 0xffff_ffff_ffff_ffffn) throw new Error('Invalid conversation cursor');
  return BigInt(value);
}
function boundedText(value: unknown, max: number): value is string { return typeof value === 'string' && value.length <= max; }
function decodeBytes(value: unknown, length: number): Uint8Array {
  if (typeof value !== 'string' || value.length > length * 2) throw new Error('Invalid sealed session material');
  const bytes = base64UrlDecode(value);
  if (bytes.length !== length) throw new Error('Invalid sealed session material');
  return bytes;
}
function encodeKeys(keys: OpenedSessionKeyPackage | null): unknown {
  if (keys === null) return null;
  const m = keys.material;
  return { controlSequence: keys.controlSequence.toString(), keyPackageId: base64UrlEncode(keys.keyPackageId),
    material: { ...m, generation: m.generation.toString(), expiresAtMs: m.expiresAtMs.toString(), notBeforeMs: m.notBeforeMs.toString(),
      sessionId: base64UrlEncode(m.sessionId), keyId: base64UrlEncode(m.keyId), srk: base64UrlEncode(m.srk), sessionNonce: base64UrlEncode(m.sessionNonce),
      abaToHcNoncePrefix: base64UrlEncode(m.abaToHcNoncePrefix), hcToAbaNoncePrefix: base64UrlEncode(m.hcToAbaNoncePrefix) } };
}
async function decodeKeys(raw: unknown, session: EndpointSessionSummary): Promise<OpenedSessionKeyPackage | null> {
  if (raw === null) return null;
  const value = record(raw); const m = record(value?.material);
  if (value === null || m === null) throw new Error('Invalid sealed key envelope');
  const material: SessionKeyMaterial = { sessionId: decodeBytes(m.sessionId, 16), generation: sequence(m.generation), keyId: decodeBytes(m.keyId, 16),
    srk: decodeBytes(m.srk, 32), sessionNonce: decodeBytes(m.sessionNonce, 32), hcToAbaNoncePrefix: decodeBytes(m.hcToAbaNoncePrefix, 4),
    abaToHcNoncePrefix: decodeBytes(m.abaToHcNoncePrefix, 4), notBeforeMs: sequence(m.notBeforeMs), expiresAtMs: sequence(m.expiresAtMs) };
  if (hex(material.sessionId) !== session.sessionId || material.generation !== 1n || material.expiresAtMs <= material.notBeforeMs) throw new Error('Sealed session key binding mismatch');
  const directions = await deriveSessionDirectionKeys(material, idBytes(session.hcEndpointId));
  return { material, abaToHcKey: directions.abaToHc, hcToAbaKey: directions.hcToAba, controlSequence: sequence(value.controlSequence), keyPackageId: decodeBytes(value.keyPackageId, 16) };
}
function validatePublicState(raw: unknown, endpoint: string, identity: EndpointIdentity): asserts raw is Conversation {
  const value = record(raw); const session = record(value?.session); const aba = record(value?.aba);
  if (value === null || session === null || aba === null) throw new Error('Invalid conversation shape');
  if (value.version !== 1 || session?.hcEndpointId !== endpoint || session?.abaEndpointId !== aba?.id || aba?.type !== 'ABA' ||
      value?.signingJkt !== identity.signing.thumbprint || value?.kemJkt !== identity.kem.thumbprint ||
      typeof session.sessionId !== 'string' || typeof aba.id !== 'string' ||
      !boundedText(aba.name, 120) || !boundedText(aba.signingJkt, 128) ||
      !statuses.includes(String(session.status)) || !Array.isArray(session.requestedCapabilities) || session.requestedCapabilities.length > 16 ||
      !boundedText(session.runtimeProfileId, 128) || !boundedText(session.workspaceId, 128) || !boundedText(value.draft, 16_000) ||
      !Array.isArray(value.messages) || value.messages.length > 500 || !Array.isArray(value.outbox) || value.outbox.length > MAX_OUTBOX ||
      !Array.isArray(value.sentIds) || value.sentIds.length > 256 || !Array.isArray(value.requests) || value.requests.length > 32 || !Array.isArray(value.inboundHashes) || value.inboundHashes.length > 256 ||
      typeof value.updatedAt !== 'number' || !Number.isSafeInteger(value.updatedAt) || typeof value.cancelPending !== 'boolean') throw new Error('Conversation snapshot binding or bounds invalid');
  idBytes(endpoint); idBytes(session.sessionId); idBytes(aba.id); if (record(aba.signingPublicJwk) === null) throw new Error('Invalid ABA key');
  parseP256PublicJwk(aba.signingPublicJwk as JsonWebKey);
  for (const cursor of [value.inbound, value.outbound, value.outboundAck]) sequence(cursor);
  if (sequence(value.outboundAck) > sequence(value.outbound)) throw new Error('Acknowledgment exceeds reserved sequence');
  if (value.reservation !== null && sequence(value.reservation) !== sequence(value.outbound)) throw new Error('Invalid interrupted reservation');
  for (const message of value.messages) {
    const item = record(message);
    if (item === null || !boundedText(item.id, 300) || !boundedText(item.text, 262_144) || !['user', 'assistant', 'system'].includes(String(item.role)) || !['streaming', 'complete', 'uncertain', 'cancelled'].includes(String(item.state))) throw new Error('Invalid local message');
  }
  let last = sequence(value.outboundAck);
  for (const packet of value.outbox) {
    const item = record(packet);
    if (item === null || sequence(item.sequence) <= last || sequence(item.sequence) > sequence(value.outbound) || !boundedText(item.encoded, 200_000) ||
        !boundedText(item.messageId, 32) || !/^[A-Za-z0-9_-]+$/u.test(item.encoded) ||
        (item.operationId !== null && !boundedText(item.operationId, 256)) || typeof item.createdAt !== 'number' || !Number.isSafeInteger(item.createdAt)) throw new Error('Invalid durable outbox');
    idBytes(item.messageId); last = sequence(item.sequence);
  }
  let lastSent = 0n;
  const sentMessages = new Set<string>();
  for (const sent of value.sentIds) {
    const item = record(sent);
    if (item === null || sequence(item.sequence) <= lastSent || sequence(item.sequence) > sequence(value.outbound) ||
        !boundedText(item.messageId, 32) || sentMessages.has(item.messageId) ||
        (item.operationId !== null && !boundedText(item.operationId, 256))) throw new Error('Invalid sent message journal');
    idBytes(item.messageId); lastSent = sequence(item.sequence); sentMessages.add(item.messageId);
  }
  for (const request of value.requests) {
    const item = record(request);
    if (item === null || !boundedText(item.id, 256) || !['describe', 'config'].includes(String(item.kind))) throw new Error('Invalid pending request');
  }
  for (const cursor of value.inboundHashes) {
    const item = record(cursor); if (item === null || sequence(item.sequence) > sequence(value.inbound) || !boundedText(item.hash, 64) || !/^[0-9a-f]{64}$/u.test(item.hash)) throw new Error('Invalid receive journal');
  }
  if (value.awaiting !== null && !boundedText(value.awaiting, 256)) throw new Error('Invalid pending turn');
  if (value.blocked !== null && !boundedText(value.blocked, 1024)) throw new Error('Invalid recovery status');
  if (value.closeKey !== null && !boundedText(value.closeKey, 128)) throw new Error('Invalid close operation');
  const runtime = record(value.runtime);
  if (runtime === null || !['pending', 'ready', 'unsupported', 'failed'].includes(String(runtime.status)) ||
      !Array.isArray(runtime.config) || runtime.config.length > 32 || !Array.isArray(runtime.permissions) || runtime.permissions.length > 64 ||
      !Array.isArray(runtime.tools) || runtime.tools.length > 100 || !Array.isArray(runtime.plan) || runtime.plan.length > 64 || !Array.isArray(runtime.diagnostics)) throw new Error('Invalid runtime snapshot');
}
export function newConversation(session: EndpointSessionSummary, aba: ABAEndpointSummary, identity: EndpointIdentity, draft = ''): Conversation {
  return { version: 1, session, aba, signingJkt: identity.signing.thumbprint, kemJkt: identity.kem.thumbprint,
    draft, keys: null, messages: [], runtime: newRuntimeState(), awaiting: null, blocked: null,
    inbound: '0', outbound: '0', outboundAck: '0', reservation: null, outbox: [], requests: [],
    inboundHashes: [], sentIds: [], cancelPending: false, closeKey: null, updatedAt: Date.now() };
}
/** The vault encrypts the entire record, including keys, titles, prompts and operation metadata. */
export class ConversationStore {
  private readonly prefix: string;
  public constructor(private readonly vault: EncryptedLocalVault, private readonly endpoint: string, private readonly identity: EndpointIdentity) {
    idBytes(endpoint); this.prefix = `remote-v1/${endpoint}/`;
  }
  public async list(): Promise<readonly StoredConversation[]> {
    const records = await this.vault.list(this.prefix);
    if (records.length > MAX_CONVERSATIONS) throw new Error('Local conversation capacity exceeded');
    const values: StoredConversation[] = [];
    for (const record of records) { const item = await this.read(record.id.slice(this.prefix.length)); if (item !== null) values.push(item); }
    return values.sort((a, b) => b.value.updatedAt - a.value.updatedAt);
  }
  public async read(sessionId: string): Promise<StoredConversation | null> {
    idBytes(sessionId);
    const stored = await this.vault.read(this.prefix + sessionId);
    if (stored === null) return null;
    try {
      if (stored.bytes.length > MAX_CONVERSATION_BYTES) throw new Error('Conversation exceeds bound');
      const raw: unknown = JSON.parse(decoder.decode(stored.bytes));
      validatePublicState(raw, this.endpoint, this.identity);
      if (raw.session.sessionId !== sessionId) throw new Error('Conversation scope mismatch');
      if (await publicJwkThumbprint(raw.aba.signingPublicJwk) !== raw.aba.signingJkt) throw new Error('ABA key fingerprint mismatch');
      const keys = await decodeKeys(raw.keys, raw.session);
      return { revision: stored.revision, value: { ...raw, keys,
        blocked: raw.reservation !== null ? '上次加密发送在保存完整消息前中断。已停止新发送，避免重用加密序号；请检查执行状态。' : raw.blocked } };
    } finally { stored.bytes.fill(0); }
  }
  public async write(value: Conversation, revision: number | null): Promise<number> {
    const raw = { ...value, keys: encodeKeys(value.keys) };
    validatePublicState(raw, this.endpoint, this.identity);
    const bytes = encoder.encode(JSON.stringify(raw));
    try {
      if (bytes.length > MAX_CONVERSATION_BYTES) throw new Error('Local conversation capacity exceeded');
      if (revision === null && (await this.vault.list(this.prefix)).length >= MAX_CONVERSATIONS) throw new Error('Local conversation capacity exceeded');
      return await this.vault.write(this.prefix + value.session.sessionId, bytes, revision);
    } finally { bytes.fill(0); }
  }
  public async delete(value: StoredConversation): Promise<void> {
    if (!isTerminal(value.value.session.status) || value.value.outbox.length !== 0) throw new Error('Active or unconfirmed work cannot be deleted');
    await this.vault.delete(this.prefix + value.value.session.sessionId, value.revision);
  }
}
