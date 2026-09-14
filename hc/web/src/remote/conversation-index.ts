import type { EncryptedLocalVault, EndpointIdentity } from '@harness/hc-core';
import { hex, idBytes } from './conversation-id';
import type { Conversation, CreationIntent } from './conversation-store';
import { isExecutionTarget, type ExecutionTarget } from './execution-target';
import { record } from './runtime-state';

export const MAX_CONVERSATION_ENTRIES = 64;
export interface ConversationEntry {
  readonly version: 1;
  readonly id: string;
  readonly endpointId: string;
  readonly signingJkt: string;
  readonly kemJkt: string;
  readonly title: string | null;
  readonly notice: string | null;
  readonly draft: string;
  readonly draftVersion: number;
  readonly target: ExecutionTarget | null;
  readonly runIds: readonly string[];
  readonly activeRunId: string | null;
  readonly creation: CreationIntent | null;
  readonly closeOperation: { readonly runId: string; readonly id: string } | null;
  readonly archived: boolean;
  readonly createdAt: number;
  readonly updatedAt: number;
}
export interface StoredConversationEntry { readonly revision: number | null; readonly value: ConversationEntry }
export interface LocalRecordIssue { readonly id: string; readonly message: string }
const encoder = new TextEncoder();
const decoder = new TextDecoder('utf-8', { fatal: true });

/** User conversations outlive any individual execution and its cryptographic counters. */
export class ConversationIndex {
  private readonly prefix: string;
  public constructor(private readonly vault: EncryptedLocalVault, private readonly endpointId: string, private readonly identity: EndpointIdentity) {
    idBytes(endpointId); this.prefix = `conversations-v2/${endpointId}/`;
  }
  public create(target: ExecutionTarget | null, draft = '', now = Date.now(), id = crypto.randomUUID().replaceAll('-', '')): ConversationEntry {
    return this.validate({ version: 1, id, endpointId: this.endpointId, signingJkt: this.identity.signing.thumbprint, kemJkt: this.identity.kem.thumbprint,
      title: null, notice: null, draft, draftVersion: 0, target, runIds: [], activeRunId: null, creation: null, closeOperation: null, archived: false, createdAt: now, updatedAt: now });
  }
  public fromRun(run: Conversation): ConversationEntry {
    return this.validate({ ...this.create({ abaEndpointId: run.session.abaEndpointId, workspaceId: run.session.workspaceId, runtimeProfileId: run.session.runtimeProfileId },
      run.draft, run.updatedAt, run.session.sessionId), runIds: [run.session.sessionId], activeRunId: run.session.sessionId });
  }
  public async creationEntry(intent: CreationIntent, draft: string, now = Date.now()): Promise<ConversationEntry> {
    const digest = new Uint8Array(await crypto.subtle.digest('SHA-256', encoder.encode(`MSS-HC-CREATION-CONVERSATION-V2/${this.endpointId}/${intent.id}`)));
    const value = this.create({ abaEndpointId: intent.abaEndpointId, workspaceId: intent.workspaceId, runtimeProfileId: intent.runtimeProfileId }, draft, now, hex(digest.slice(0, 16)));
    return this.validate({ ...value, creation: intent });
  }
  private validate(raw: unknown): ConversationEntry {
    const value = record(raw);
    if (value === null || value.version !== 1 || typeof value.id !== 'string' || value.endpointId !== this.endpointId ||
      value.signingJkt !== this.identity.signing.thumbprint || value.kemJkt !== this.identity.kem.thumbprint ||
      (value.title !== null && (typeof value.title !== 'string' || value.title.trim() === '' || value.title.length > 120)) ||
      typeof value.draft !== 'string' || value.draft.length > 16_000 || (value.target !== null && !isExecutionTarget(value.target)) ||
      !Array.isArray(value.runIds) || value.runIds.length > 32 || new Set(value.runIds).size !== value.runIds.length ||
      (value.activeRunId !== null && !value.runIds.includes(value.activeRunId)) || typeof value.archived !== 'boolean' ||
      !Number.isSafeInteger(value.createdAt) || !Number.isSafeInteger(value.updatedAt)) throw new Error('Conversation identity or metadata is invalid');
    idBytes(value.id);
    if (value.draftVersion !== undefined && (!Number.isSafeInteger(value.draftVersion) || Number(value.draftVersion) < 0)) throw new Error('Invalid draft version');
    if (value.notice !== undefined && value.notice !== null && (typeof value.notice !== 'string' || value.notice.length > 1024)) throw new Error('Invalid conversation notice');
    for (const runId of value.runIds) { if (typeof runId !== 'string') throw new Error('Invalid execution reference'); idBytes(runId); }
    if (value.creation !== null) {
      const intent = record(value.creation);
      if (intent === null || typeof intent.id !== 'string' || !/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(intent.id) || !isExecutionTarget(intent) ||
        typeof intent.draft !== 'string' || intent.draft.length > 16_000) throw new Error('Invalid conversation creation intent');
      if (intent.cancelRequested !== undefined && typeof intent.cancelRequested !== 'boolean') throw new Error('Invalid creation cancellation marker');
    }
    if (value.closeOperation !== undefined && value.closeOperation !== null) {
      const close = record(value.closeOperation);
      if (close === null || typeof close.runId !== 'string' || !value.runIds.includes(close.runId) || typeof close.id !== 'string' || !/^[0-9a-f-]{36}$/u.test(close.id)) throw new Error('Invalid close operation');
    }
    return { ...value, draftVersion: value.draftVersion ?? 0, notice: value.notice ?? null, closeOperation: value.closeOperation ?? null } as unknown as ConversationEntry;
  }
  public async read(id: string): Promise<StoredConversationEntry | null> {
    idBytes(id);
    const saved = await this.vault.read(this.prefix + id);
    if (saved === null) return null;
    try {
      if (saved.bytes.length > 128 * 1024) throw new Error('Conversation metadata is too large');
      const value = this.validate(JSON.parse(decoder.decode(saved.bytes)));
      if (value.id !== id) throw new Error('Conversation metadata scope mismatch');
      return { revision: saved.revision, value };
    } finally { saved.bytes.fill(0); }
  }
  public async write(value: ConversationEntry, revision: number | null): Promise<number> {
    this.validate(value);
    if (revision === null && (await this.vault.list(this.prefix)).length >= MAX_CONVERSATION_ENTRIES) throw new Error('Conversation list capacity reached');
    const bytes = encoder.encode(JSON.stringify(value));
    try { if (bytes.length > 128 * 1024) throw new Error('Conversation metadata is too large'); return await this.vault.write(this.prefix + value.id, bytes, revision); }
    finally { bytes.fill(0); }
  }
  public async ensure(value: ConversationEntry): Promise<StoredConversationEntry> {
    const existing = await this.read(value.id);
    if (existing !== null) return existing;
    return { value, revision: await this.write(value, null) };
  }
  public async list(): Promise<{ readonly entries: readonly StoredConversationEntry[]; readonly issues: readonly LocalRecordIssue[] }> {
    const records = await this.vault.list(this.prefix);
    if (records.length > MAX_CONVERSATION_ENTRIES) throw new Error('Conversation list capacity exceeded');
    const entries: StoredConversationEntry[] = [];
    const issues: LocalRecordIssue[] = [];
    for (const row of records) {
      const id = row.id.slice(this.prefix.length);
      try { const value = await this.read(id); if (value !== null) entries.push(value); }
      catch { issues.push({ id, message: '这份对话记录无法读取，已保留原数据。' }); }
    }
    return { entries: entries.sort((a, b) => b.value.updatedAt - a.value.updatedAt), issues };
  }
  public async delete(value: StoredConversationEntry): Promise<void> {
    if (value.revision === null || value.value.creation !== null) throw new Error('Unconfirmed conversation cannot be deleted');
    await this.vault.delete(this.prefix + value.value.id, value.revision);
  }
}
