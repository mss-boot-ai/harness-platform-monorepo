import {
  ControlType, decodeWireMessage, SessionKeyPackageSchema, WirePacketSchema,
  type EndpointIdentity, type ReadyGatewayConnection,
} from '@harness/hc-core';
import { HcApiError, type ABAEndpointSummary, type EndpointSessionSummary, type SessionCreationState } from '../api';
import { ConversationController, ConversationTransportInterrupted, type ConversationTransport, type ConversationView } from './conversation-controller';
import { ConversationStore, hex, isTerminal, MAX_CONVERSATIONS, newConversation,
  type ConversationWorkspace, type CreationIntent, type StoredConversation, type StoredWorkspace } from './conversation-store';
import type { ConfigOption, RpcId } from './runtime-state';
import { executionTargetState, type ExecutionTarget } from './execution-target';
import { ConversationIndex, type ConversationEntry, type StoredConversationEntry, type LocalRecordIssue } from './conversation-index';

export interface ConversationAPI {
  abas(): Promise<readonly ABAEndpointSummary[]>;
  sessions(): Promise<readonly EndpointSessionSummary[]>;
  create(intent: CreationIntent): Promise<EndpointSessionSummary>;
  close(sessionId: string, operationId: string): Promise<EndpointSessionSummary>;
  status(sessionId: string): Promise<EndpointSessionSummary>;
  statuses(sessionIds: readonly string[]): Promise<readonly EndpointSessionSummary[]>;
  creation(operationId: string, cancel: boolean): Promise<SessionCreationState>;
}
export interface ConversationEntryView extends ConversationEntry { readonly fault: string | null }
export interface CreatedRun { readonly conversationId: string; readonly runId: string }
export interface WorkspaceOccupant { readonly conversationId: string | null; readonly runId: string | null; readonly title: string; readonly status: string }
export class ConversationActionCancelled extends Error {}
export interface ManagerView {
  readonly status: 'loading' | 'ready' | 'failed'; readonly online: boolean; readonly creating: boolean;
  readonly selectedId: string | null;
  readonly workspace: ConversationWorkspace | null; readonly conversations: readonly ConversationEntryView[]; readonly runs: readonly ConversationView[];
  readonly endpoints: readonly ABAEndpointSummary[]; readonly unrecoverable: readonly EndpointSessionSummary[];
  readonly recovery: Readonly<Record<string, string>>; readonly error: string | null; readonly pendingWrites: number;
  readonly storageIssues: readonly LocalRecordIssue[];
}
const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 200));
const MAX_BUFFER_BYTES = 4 * 1024 * 1024;

/** A page selects a view; this owner keeps all conversation lifecycles alive. */
export class ConversationManager {
  private readonly controllers = new Map<string, ConversationController>();
  private readonly entries = new Map<string, StoredConversationEntry>();
  private readonly entryQueues = new Map<string, Promise<void>>();
  private readonly entryFaults = new Map<string, string>();
  private readonly knownClosed = new Set<string>();
  private readonly index: ConversationIndex;
  private storageIssues: readonly LocalRecordIssue[] = [];
  private readonly listeners = new Set<() => void>();
  private workspace: StoredWorkspace | null = null;
  private metadataQueue: Promise<void> = Promise.resolve();
  private readonly operations = new Set<Promise<unknown>>();
  private connection: ReadyGatewayConnection | null = null;
  private detach: (() => void) | null = null;
  private epoch = 0;
  private closed = false;
  private loaded = false;
  private loading: Promise<void> | null = null;
  private disposal: Promise<void> | null = null;
  private restoring = false;
  private readonly creating = new Set<string>();
  private readonly cancelledCreations = new Set<string>();
  private readonly adopting = new Map<string, Promise<void>>();
  private pendingWrites = 0;
  private selectionVersion = 0;
  private selection: { readonly id: string | null; readonly version: number } | null = null;
  private buffer: Uint8Array[] = [];
  private bufferBytes = 0;
  private controlSequence = 0n;
  private controlQueue: Promise<void> = Promise.resolve();
  private checking: Promise<void> | null = null;
  private readonly allowed = new Set<string>();
  private readonly creationGrants = new Map<string, number>();
  private grantRevision = 0;
  private recovery: Record<string, string> = {};
  private endpoints: readonly ABAEndpointSummary[] = [];
  private unrecoverable: readonly EndpointSessionSummary[] = [];
  private error: string | null = null;
  private status: ManagerView['status'] = 'loading';
  private view: ManagerView = { status: 'loading', online: false, creating: false, workspace: null, selectedId: null,
    conversations: [], runs: [], endpoints: [], unrecoverable: [], recovery: {}, error: null, pendingWrites: 0, storageIssues: [] };
  public constructor(private readonly store: ConversationStore, private readonly identity: EndpointIdentity,
    private readonly endpointId: string, private readonly api: ConversationAPI, private readonly now = Date.now) { this.index = store.conversationIndex(); }
  public readonly subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  public readonly snapshot = (): ManagerView => this.view;
  private emit(): void {
    const selectedId = this.selection === null ? this.workspace?.value.selectedId ?? null : this.selection.id;
    this.view = { status: this.status, online: this.connection?.socket.readyState === 1 && !this.restoring,
      creating: selectedId !== null && this.creating.has(selectedId), workspace: this.workspace?.value ?? null, selectedId,
      conversations: [...this.entries.values()].map(({ value }) => ({ ...value, fault: this.entryFaults.get(value.id) ?? null })).sort((a, b) => b.updatedAt - a.updatedAt),
      runs: [...this.controllers.values()].map((controller) => controller.snapshot()), endpoints: this.endpoints,
      unrecoverable: this.unrecoverable, recovery: { ...this.recovery }, error: this.error, pendingWrites: this.pendingWrites, storageIssues: this.storageIssues };
    if (!this.closed) for (const listener of this.listeners) listener();
  }
  private assertOpen(): void { if (this.closed || this.status === 'failed') throw new Error('Conversation workspace is unavailable'); }
  private current(epoch: number): boolean { return !this.closed && epoch === this.epoch; }
  private track<T>(promise: Promise<T>): Promise<T> {
    this.operations.add(promise);
    void promise.then(() => this.operations.delete(promise), () => this.operations.delete(promise));
    return promise;
  }
  private fail(message: string): void { this.error = message; this.emit(); }
  private fatal(): void {
    this.status = 'failed'; this.disconnect();
    this.fail('本地加密记录无法读取或保存。已暂停操作，请保留浏览器数据并检查存储；不会覆盖或重建旧记录。');
  }
  private metadata(change: (value: ConversationWorkspace) => ConversationWorkspace): Promise<void> {
    try { this.assertOpen(); } catch (cause) { return Promise.reject(cause); }
    this.pendingWrites += 1; this.emit();
    const operation = this.metadataQueue.then(async () => {
      if (this.status === 'failed' || this.workspace === null) throw new Error('Workspace has not loaded');
      const value = change(this.workspace.value);
      const revision = await this.store.writeWorkspace(value, this.workspace.revision);
      this.workspace = { revision, value }; this.emit();
    });
    this.metadataQueue = operation.catch(() => { this.fatal(); }).finally(() => { this.pendingWrites -= 1; this.emit(); });
    return operation;
  }
  private entry(id: string): ConversationEntry {
    const entry = this.entries.get(id);
    if (entry === undefined) throw new Error('Unknown conversation');
    return entry.value;
  }
  private changeEntry(id: string, change: (value: ConversationEntry) => ConversationEntry): Promise<void> {
    this.assertOpen(); this.pendingWrites += 1; this.emit();
    const operation = (this.entryQueues.get(id) ?? Promise.resolve()).then(async () => {
      const previous = this.entries.get(id);
      if (previous === undefined) throw new Error('Unknown conversation');
      const value = { ...change(previous.value), updatedAt: this.now() };
      const revision = await this.index.write(value, previous.revision);
      this.entries.set(id, { value, revision }); this.entryFaults.delete(id); this.emit();
    });
    this.entryQueues.set(id, operation.catch(() => {
      this.entryFaults.set(id, '本对话的记录尚未保存，请保留此页并复制需要的内容。'); this.emit();
    }).finally(() => { this.pendingWrites -= 1; this.emit(); }));
    return this.track(operation);
  }
  private transport(): ConversationTransport {
    return { now: this.now, send: (bytes) => {
      const connection = this.connection;
      if (this.closed || connection === null || connection.socket.readyState !== 1) return false;
      if (connection.socket.bufferedAmount > MAX_BUFFER_BYTES) {
        this.disconnect(); this.fail('连接发送积压，消息已加密保存。请重新连接以补传原始消息。'); return false;
      }
      try { connection.socket.send(new Uint8Array(bytes).buffer); return true; }
      catch { this.disconnect(); this.fail('连接发送中断，原始消息已保留。请重新连接以确认投递。'); return false; }
    }, control: (encode) => {
      const epoch = this.epoch; const connection = this.connection;
      const operation = this.controlQueue.then(async () => {
        if (!this.current(epoch) || connection === null || connection.socket.readyState !== 1) throw new ConversationTransportInterrupted('Connection changed before control');
        if (this.controlSequence >= 0xffff_ffff_ffff_ffffn) throw new Error('Control sequence exhausted');
        const bytes = await encode(++this.controlSequence);
        if (!this.current(epoch) || connection !== this.connection || connection.socket.readyState !== 1) throw new ConversationTransportInterrupted('Connection changed during control');
        if (connection.socket.bufferedAmount > MAX_BUFFER_BYTES) { this.disconnect(); this.fail('连接控制消息积压，请重新连接以恢复会话。'); throw new ConversationTransportInterrupted('Connection backpressure'); }
        try { connection.socket.send(new Uint8Array(bytes).buffer); }
        catch { this.disconnect(); this.fail('控制消息发送中断，请重新连接以恢复会话。'); throw new ConversationTransportInterrupted('Control transport interrupted'); }
      });
      this.controlQueue = operation.catch(() => {
        // A failed encoder may already have consumed a control sequence. Never skip it on a live connection.
        if (this.current(epoch) && this.connection === connection) { this.disconnect(); this.fail('连接控制状态已中断，请重新连接后恢复。'); }
      }); return operation;
    } };
  }
  private add(stored: StoredConversation): ConversationController {
    const id = stored.value.session.sessionId;
    const existing = this.controllers.get(id); if (existing !== undefined) return existing;
    const controller = new ConversationController(stored, this.store, this.identity, this.transport(), () => this.emit());
    this.controllers.set(id, controller); return controller;
  }
  public load(): Promise<void> {
    if (this.loading !== null) return this.loading;
    this.loading = this.track((async () => {
      this.assertOpen(); if (this.loaded) return;
      try {
        const [recovered, workspace] = await Promise.all([this.store.listRecoverable(), this.store.readWorkspace()]);
        this.assertOpen(); this.workspace = workspace;
        for (const item of recovered.runs) this.add(item);
        const indexed = await this.index.list();
        for (const item of indexed.entries) this.entries.set(item.value.id, item);
        this.storageIssues = [...recovered.issues, ...indexed.issues];
        if (!workspace.value.indexed) {
          for (const run of recovered.runs) {
            if (![...this.entries.values()].some((entry) => entry.value.runIds.includes(run.value.session.sessionId))) {
              const entry = await this.index.ensure(this.index.fromRun(run.value)); this.entries.set(entry.value.id, entry);
            }
          }
          let selectedId = workspace.value.selectedId;
          if (workspace.value.creation !== null) {
            const entry = await this.index.ensure(await this.index.creationEntry(workspace.value.creation, workspace.value.draft || workspace.value.creation.draft, this.now()));
            this.entries.set(entry.value.id, entry);
            if (selectedId === null) selectedId = entry.value.id;
          }
          await this.metadata((value) => ({ ...value, indexed: true, creation: null, selectedId: selectedId !== null && this.entries.has(selectedId) ? selectedId : null }));
        } else if (workspace.value.selectedId !== null && !this.entries.has(workspace.value.selectedId)) {
          await this.metadata((value) => ({ ...value, selectedId: null }));
        }
        this.loaded = true; this.status = 'ready'; this.emit();
      } catch (cause) { if (!this.closed) this.fatal(); throw cause; }
    })());
    return this.loading;
  }
  public disconnect(): void {
    this.epoch += 1; this.detach?.(); this.detach = null;
    const connection = this.connection; this.connection = null;
    this.allowed.clear(); this.buffer = []; this.bufferBytes = 0;
    connection?.socket.close(1000, 'HC transport detached'); this.emit();
  }
  public bind(connection: ReadyGatewayConnection): Promise<void> {
    return this.track((async () => {
      this.assertOpen(); this.disconnect();
      const epoch = this.epoch;
      // Old receive/crypto completions can persist, but cannot send through the new socket.
      await Promise.all([...this.controllers.values()].map((controller) => controller.idle()));
      await this.controlQueue;
      if (!this.current(epoch)) { connection.socket.close(1000, 'Superseded connection'); return; }
      this.connection = connection; this.controlSequence = 0n; this.restoring = true;
      const message = (event: MessageEvent) => {
        if (!this.current(epoch)) return;
        if (!(event.data instanceof ArrayBuffer) || event.data.byteLength === 0 || event.data.byteLength > 1_048_576) { this.disconnect(); this.fail('连接返回了不支持的消息，已停止接收。'); return; }
        this.receive(new Uint8Array(event.data));
      };
      const closed = () => { if (this.current(epoch)) { this.connection = null; this.allowed.clear(); this.emit(); } };
      connection.socket.addEventListener('message', message); connection.socket.addEventListener('close', closed);
      const timer = setInterval(() => { if (this.current(epoch) && !this.restoring && connection.socket.readyState === 1) void this.refresh().catch(() => undefined); }, 1000);
      this.detach = () => { clearInterval(timer); connection.socket.removeEventListener('message', message); connection.socket.removeEventListener('close', closed); };
      try {
        if (!this.loaded) await this.load();
        await this.reconcile(epoch);
        if (!this.current(epoch)) return;
        this.restoring = false; this.error = null; this.flush();
        await Promise.all([...this.controllers.entries()].filter(([id]) => this.allowed.has(id)).map(async ([id, controller]) => {
          try { await controller.resume(); await this.describe(id); }
          catch (cause) { if (!(cause instanceof ConversationTransportInterrupted) && this.current(epoch)) this.recovery[id] = '会话恢复未确认，已暂停此对话的操作。请检查执行状态。'; }
        }));
        this.emit();
      } catch (cause) {
        if (this.current(epoch)) { this.restoring = false; this.disconnect(); this.fail('无法验证当前账户、端点或会话授权，已保留加密记录并暂停恢复。'); }
        throw cause;
      }
    })());
  }
  private async reconcile(epoch: number): Promise<readonly string[]> {
    // A server response can only replace authorizations that existed when its request started.
    const subjects = [...this.controllers.entries()].map(([id, controller]) => ({ id, controller, grant: this.creationGrants.get(id) }));
    const [endpoints, sessions, observations] = await Promise.all([this.api.abas(), this.api.sessions(), this.api.statuses(subjects.map(({ id }) => id))]);
    if (!this.current(epoch)) return [];
    this.endpoints = endpoints; const restored: string[] = [];
    this.unrecoverable = sessions.filter((session) => session.hcEndpointId === this.endpointId && !this.controllers.has(session.sessionId) && !isTerminal(session.status));
    await Promise.all(subjects.map(async ({ id, controller, grant }) => {
      const applicable = () => this.current(epoch) && this.creationGrants.get(id) === grant;
      if (!applicable()) return;
      const saved = controller.snapshot().data;
      const session = observations.find((item) => item.sessionId === id);
      const aba = endpoints.find((item) => item.id === saved.aba.id && item.status === 'ACTIVE');
      if (session === undefined || session === null || session.hcEndpointId !== this.endpointId) { this.allowed.delete(id); this.recovery[id] = '当前账户无法确认这份会话的授权，仅保留本地记录。'; return; }
      if (isTerminal(session.status)) this.knownClosed.add(id);
      if (!isTerminal(session.status) && (aba === undefined || aba.signingJkt !== saved.aba.signingJkt)) { this.allowed.delete(id); this.recovery[id] = '执行设备已失效或密钥指纹变化，无法安全恢复此会话。'; return; }
      try {
        await controller.observe(session);
        if (!applicable()) return;
        if (session.status === 'CLOSED') await this.confirmClosureNotice(id);
        if (!this.allowed.has(id)) restored.push(id);
        delete this.recovery[id]; this.allowed.add(id);
      } catch { if (applicable()) { this.allowed.delete(id); this.recovery[id] = '会话身份或本地保存状态不一致，已暂停操作。'; } }
    }));
    if (!this.current(epoch)) return [];
    this.emit(); return restored;
  }
  public refresh(): Promise<void> {
    if (this.checking !== null) return this.checking;
    const epoch = this.epoch;
    const operation = this.track((async () => {
      this.assertOpen(); if (this.connection?.socket.readyState !== 1) return;
      try {
        const restored = new Set(await this.reconcile(epoch));
        if (this.current(epoch)) {
          this.flush();
          await Promise.all([...this.controllers.entries()].map(async ([id, controller]) => {
            try { if (restored.has(id)) await controller.resume(); await this.describe(id); }
            catch (cause) { if (!(cause instanceof ConversationTransportInterrupted) && this.current(epoch)) { this.recovery[id] = '会话恢复未确认，请检查连接与执行状态。'; this.emit(); } }
          }));
        }
      } catch (cause) { if (this.current(epoch)) { this.disconnect(); this.fail('当前会话授权复核失败，已暂停连接。请重新连接或登录以核对授权并恢复原始消息。'); } throw cause; }
    })());
    this.checking = operation;
    void operation.then(() => { if (this.checking === operation) this.checking = null; }, () => { if (this.checking === operation) this.checking = null; });
    return operation;
  }
  private retain(bytes: Uint8Array): void {
    if (this.buffer.length >= 32 || this.bufferBytes + bytes.length > MAX_BUFFER_BYTES) { this.disconnect(); this.fail('会话恢复消息超过缓存上限，请重新连接以恢复原始消息。'); return; }
    this.buffer.push(bytes.slice()); this.bufferBytes += bytes.length;
  }
  private receive(bytes: Uint8Array): void {
    if (this.closed || this.connection === null) return;
    if (this.restoring || !this.loaded) { this.retain(bytes); return; }
    try {
      const packet = decodeWireMessage(WirePacketSchema, bytes);
      if (packet.wireMajor !== 1 || packet.wireMinor !== 0 || packet.packetId.length !== 16) throw new Error('Invalid wire envelope');
      let id: string | undefined;
      if (packet.body.case === 'encrypted' || packet.body.case === 'ack') id = hex(packet.body.value.sessionId);
      else if (packet.body.case === 'control' && packet.body.value.type === ControlType.SESSION_KEY_PACKAGE) id = hex(decodeWireMessage(SessionKeyPackageSchema, packet.body.value.payload).sessionId);
      else if (packet.body.case === 'error') {
        const messageId = hex(packet.body.value.relatedMessageId);
        id = [...this.controllers.entries()].find(([, controller]) => controller.ownsMessage(messageId))?.[0];
      }
      if (id === undefined) return;
      const controller = this.controllers.get(id);
      if (controller === undefined) { if (this.creating.size > 0 || [...this.entries.values()].some((entry) => entry.value.creation !== null)) this.retain(bytes); return; }
      if (this.knownClosed.has(id) || isTerminal(controller.snapshot().data.session.status) || controller.snapshot().fault !== null || controller.snapshot().data.blocked !== null) return;
      if (!this.allowed.has(id) || this.recovery[id] !== undefined) { this.retain(bytes); return; }
      const routedId = id;
      // Core validates signatures, channel, generation and endpoint after this untrusted routing hint.
      const epoch = this.epoch;
      void controller.receive(bytes).then(() => this.describe(routedId)).catch((cause: unknown) => {
        if (cause instanceof ConversationTransportInterrupted || !this.current(epoch)) return;
        this.allowed.delete(routedId); this.recovery[routedId] = '消息验证或保存失败，此对话已暂停。'; this.emit();
      });
    } catch { this.disconnect(); this.fail('连接消息无法安全识别，已暂停恢复。'); }
  }
  private flush(): void { const buffered = this.buffer; this.buffer = []; this.bufferBytes = 0; for (const bytes of buffered) this.receive(bytes); }
  private async describe(id: string): Promise<void> {
    const controller = this.controllers.get(id); if (controller === undefined || !this.allowed.has(id) || this.restoring) return;
    const view = controller.snapshot(); const value = view.data;
    if (value.session.status === 'ACTIVE' && value.keys !== null && value.blocked === null && view.fault === null && value.awaiting === null &&
      value.runtime.status === 'pending' && value.requests.length === 0 && value.session.requestedCapabilities.includes('remote-session-v1')) {
      await controller.describe();
    }
  }
  public select(id: string | null): Promise<void> {
    if (this.closed) return Promise.reject(new Error('Conversation workspace is closed'));
    if (id !== null && !this.entries.has(id)) return Promise.reject(new Error('Unknown conversation'));
    const intent = { id, version: ++this.selectionVersion };
    this.selection = intent; this.emit();
    return this.metadata((value) => ({ ...value, selectedId: id })).then(() => {
      if (this.selection === intent) { this.selection = null; this.emit(); }
    });
  }
  public target(target: ExecutionTarget): Promise<void> {
    if (!executionTargetState(this.endpoints, target).available) return Promise.reject(new Error('Selected project is unavailable'));
    const id = this.snapshot().selectedId;
    if (id === null) return this.metadata((value) => ({ ...value, target }));
    const entry = this.entry(id);
    if (entry.activeRunId !== null && !this.knownClosed.has(entry.activeRunId) && !isTerminal(this.controllers.get(entry.activeRunId)?.snapshot().data.session.status ?? '')) {
      return Promise.reject(new Error('The active execution target cannot change'));
    }
    return this.changeEntry(id, (entry) => ({ ...entry, target, notice: null })).then(() => this.metadata((value) => ({ ...value, target })));
  }
  public draft(id: string | null, text: string): Promise<void> {
    this.assertOpen();
    if (text.length > 16_000) return Promise.reject(new Error('Draft exceeds limit'));
    if (id === null) return this.metadata((value) => ({ ...value, draft: text }));
    return this.changeEntry(id, (value) => ({ ...value, draft: text, draftVersion: value.draftVersion + 1 }));
  }
  private actionable(id: string, expectedRunId?: string): ConversationController {
    this.assertOpen();
    const entry = this.entry(id); const runId = entry.activeRunId;
    if (expectedRunId !== undefined && runId !== expectedRunId) throw new ConversationActionCancelled('Execution changed before action');
    if (runId !== null && this.knownClosed.has(runId)) throw new ConversationActionCancelled('Execution was closed');
    const controller = runId === null ? undefined : this.controllers.get(runId);
    if (this.restoring || this.connection?.socket.readyState !== 1 || runId === null || !this.allowed.has(runId) || this.recovery[runId] !== undefined || this.knownClosed.has(runId) || controller === undefined) throw new Error('Conversation authorization is unavailable');
    return controller;
  }
  public workspaceConflict(abaId: string, workspaceId: string, except?: string): boolean {
    return this.workspaceOccupants(abaId, workspaceId, except).length > 0;
  }
  public workspaceOccupants(abaId: string, workspaceId: string, except?: string): readonly WorkspaceOccupant[] {
    const exceptRun = except === undefined ? undefined : this.entries.get(except)?.value.activeRunId ?? except;
    const result: WorkspaceOccupant[] = [];
    const add = (session: EndpointSessionSummary) => {
      if (session.sessionId === exceptRun || this.knownClosed.has(session.sessionId) || isTerminal(session.status) || session.abaEndpointId !== abaId || session.workspaceId !== workspaceId) return;
      const entry = [...this.entries.values()].find(({ value }) => value.activeRunId === session.sessionId)?.value;
      const first = this.controllers.get(session.sessionId)?.snapshot().data.messages.find((message) => message.role === 'user')?.text;
      result.push({ conversationId: entry?.id ?? null, runId: session.sessionId, title: entry?.title ?? first?.slice(0, 60) ?? '已有运行', status: session.status });
    };
    for (const controller of this.controllers.values()) add(controller.snapshot().data.session);
    for (const session of this.unrecoverable) if (!this.controllers.has(session.sessionId)) add(session);
    for (const { value } of this.entries.values()) if (value.id !== except && value.creation?.abaEndpointId === abaId && value.creation.workspaceId === workspaceId) {
      result.push({ conversationId: value.id, runId: null, title: value.title ?? '创建待确认的对话', status: 'CREATING' });
    }
    return result;
  }
  public prompt(id: string, text: string, expectedRunId?: string): Promise<void> {
    const controller = this.actionable(id, expectedRunId); const value = controller.snapshot().data;
    if (this.workspaceConflict(value.aba.id, value.session.workspaceId, id)) return Promise.reject(new Error('Another conversation is using this workspace'));
    const originalDraft = this.entry(id).draft;
    const version = this.entry(id).draftVersion;
    return controller.prompt(text).then(() => this.changeEntry(id, (entry) => ({ ...entry, notice: null,
      draft: entry.draftVersion === version && originalDraft.trim() === text.trim() && entry.draft === originalDraft ? '' : entry.draft })));
  }
  public configure(id: string, option: ConfigOption, value: string, expectedRunId?: string): Promise<void> { return this.actionable(id, expectedRunId).configure(option, value); }
  public cancel(id: string, expectedRunId?: string): Promise<void> { return this.actionable(id, expectedRunId).cancel(); }
  public decide(id: string, request: RpcId, option: string | null, expectedRunId?: string): Promise<void> { return this.actionable(id, expectedRunId).decide(request, option); }
  public describeNow(id: string): Promise<void> { return this.actionable(id).describe(true); }
  public executionClosed(runId: string): boolean { return this.knownClosed.has(runId) || isTerminal(this.controllers.get(runId)?.snapshot().data.session.status ?? ''); }
  private async confirmClosureNotice(runId: string): Promise<void> {
    const notice = '本次运行已确认结束，历史已保留。可以继续此对话或新建对话。';
    for (const { value } of this.entries.values()) {
      if (value.activeRunId === runId && value.closeOperation?.runId === runId && value.notice !== notice) {
        await this.changeEntry(value.id, (entry) => entry.activeRunId === runId ? { ...entry, notice } : entry);
      }
    }
  }
  public async inspect(id: string): Promise<void> {
    const runId = this.entry(id).activeRunId;
    if (runId === null) return;
    const session = await this.api.status(runId);
    if (session.sessionId !== runId || session.hcEndpointId !== this.endpointId) throw new Error('Execution status binding changed');
    if (isTerminal(session.status)) this.knownClosed.add(runId);
    await this.controllers.get(runId)?.observe(session);
    if (session.status === 'CLOSED') await this.confirmClosureNotice(runId);
    this.emit();
  }
  public newConversation(target: ExecutionTarget | null = this.snapshot().workspace?.target ?? null, draft = ''): Promise<string> {
    this.assertOpen();
    const value = this.index.create(target, draft, this.now());
    this.entries.set(value.id, { value, revision: null });
    const saved = this.changeEntry(value.id, (entry) => entry);
    const selected = this.select(value.id);
    return this.track(Promise.all([saved, selected]).then(() => value.id));
  }
  private adoptRun(id: string, intent: CreationIntent, session: EndpointSessionSummary): Promise<void> {
    const previous = this.adopting.get(id) ?? Promise.resolve();
    const next = previous.catch(() => undefined).then(() => this.storeAdoptedRun(id, intent, session));
    this.adopting.set(id, next);
    void next.finally(() => { if (this.adopting.get(id) === next) this.adopting.delete(id); }).catch(() => undefined);
    return next;
  }
  private async storeAdoptedRun(id: string, intent: CreationIntent, session: EndpointSessionSummary): Promise<void> {
    const entry = this.entry(id);
    if (entry.activeRunId === session.sessionId) return;
    if (entry.creation?.id !== intent.id) throw new ConversationActionCancelled('Creation intent changed');
    if (session.hcEndpointId !== this.endpointId || session.abaEndpointId !== intent.abaEndpointId || session.workspaceId !== intent.workspaceId || session.runtimeProfileId !== intent.runtimeProfileId) throw new Error('Created session binding mismatch');
    const aba = this.endpoints.find((item) => item.id === intent.abaEndpointId);
    if (!this.controllers.has(session.sessionId)) {
      if (aba === undefined && !isTerminal(session.status)) throw new Error('Execution endpoint is unavailable');
      if (aba !== undefined) {
        const value = newConversation(session, aba, this.identity, intent.draft);
        const revision = await this.store.write(value, null); this.add({ revision, value });
      }
    }
    if (isTerminal(session.status)) this.knownClosed.add(session.sessionId);
    this.creationGrants.set(session.sessionId, ++this.grantRevision); this.allowed.add(session.sessionId);
    await this.changeEntry(id, (entry) => ({ ...entry, creation: null, notice: null, closeOperation: null,
      activeRunId: session.sessionId, runIds: entry.runIds.includes(session.sessionId) ? entry.runIds : [...entry.runIds, session.sessionId] }));
    this.unrecoverable = this.unrecoverable.filter((item) => item.sessionId !== session.sessionId);
    this.flush(); this.emit();
  }
  public create(input: Omit<CreationIntent, 'id'>, retry = false, conversationId: string | null = this.snapshot().selectedId): Promise<CreatedRun> {
    return this.track((async () => {
      this.assertOpen();
      if (this.workspace === null || this.restoring || this.connection?.socket.readyState !== 1) throw new Error('Cannot create an execution now');
      if (this.controllers.size >= MAX_CONVERSATIONS) throw new Error('Local conversation capacity reached');
      const id = conversationId ?? await this.newConversation({ abaEndpointId: input.abaEndpointId, workspaceId: input.workspaceId, runtimeProfileId: input.runtimeProfileId }, input.draft);
      const entry = this.entry(id);
      if (entry.archived || this.creating.has(id)) throw new Error('Conversation cannot start another execution now');
      if (entry.activeRunId !== null && !this.knownClosed.has(entry.activeRunId) && !isTerminal(this.controllers.get(entry.activeRunId)?.snapshot().data.session.status ?? '')) throw new Error('Close the existing execution before starting another');
      if (entry.creation !== null && !retry) throw new Error('Previous creation needs reconciliation');
      const intent: CreationIntent = entry.creation ?? { ...input, id: crypto.randomUUID() };
      if (intent.cancelRequested || this.cancelledCreations.has(intent.id)) throw new ConversationActionCancelled('Creation cancellation was requested');
      const aba = this.endpoints.find((item) => item.id === intent.abaEndpointId && item.status === 'ACTIVE');
      if (aba === undefined || (!retry && this.workspaceConflict(aba.id, intent.workspaceId, id))) throw new Error('Execution workspace is unavailable or busy');
      this.creating.add(id); this.emit(); const epoch = this.epoch;
      try {
        await this.changeEntry(id, (entry) => ({ ...entry, creation: intent, notice: null,
          target: { abaEndpointId: intent.abaEndpointId, workspaceId: intent.workspaceId, runtimeProfileId: intent.runtimeProfileId } }));
        if (!this.current(epoch)) throw new Error('Connection changed before create');
        const session = await this.api.create(intent);
        if (!this.current(epoch)) throw new Error('Connection changed during create');
        if (this.cancelledCreations.has(intent.id) || this.entry(id).creation?.id !== intent.id) throw new ConversationActionCancelled('Creation intent changed');
        await this.adoptRun(id, intent, session);
        if (this.cancelledCreations.has(intent.id)) throw new ConversationActionCancelled('Creation cancellation was requested');
        return { conversationId: id, runId: session.sessionId };
      } catch (cause) {
        if (cause instanceof ConversationActionCancelled) throw cause;
        const rejected = !retry && cause instanceof HcApiError && [400, 403, 409, 429].includes(cause.status) &&
          ['EXECUTION_TARGET_NOT_ALLOWED', 'CATALOG_UNAVAILABLE', 'CREATION_CANCELLED', 'HARNESS_INVALID_ARGUMENT', 'HARNESS_RESOURCE_LIMIT'].includes(cause.code);
        try { await this.changeEntry(id, (entry) => ({ ...entry, creation: rejected ? null : entry.creation,
          notice: rejected ? '创建未获接受，草稿已保留。请调整项目或 Agent 后再发送。' : '创建结果尚未确认。请检查原创建或取消创建；也可以新建另一份对话。' })); } catch { /* The original error and retained in-memory draft remain available. */ }
        throw cause;
      } finally { this.creating.delete(id); this.flush(); this.emit(); }
    })());
  }
  public waitReady(id: string, expectedRunId: string | null = this.entry(id).activeRunId): Promise<void> {
    return this.track((async () => {
      const epoch = this.epoch;
      for (let attempt = 0; attempt < 100 && this.current(epoch); attempt += 1) {
        const runId = this.entry(id).activeRunId;
        if (runId !== expectedRunId) throw new ConversationActionCancelled('Execution changed while becoming ready');
        if (runId !== null && this.knownClosed.has(runId)) throw new ConversationActionCancelled('Execution was closed while becoming ready');
        const view = runId === null ? undefined : this.controllers.get(runId)?.snapshot();
        if (view === undefined || isTerminal(view.data.session.status) || view.fault !== null || view.data.blocked !== null) throw new Error('Session cannot become ready');
        if (view.data.session.status === 'ACTIVE' && view.data.keys !== null && (view.data.runtime.status === 'ready' || !view.data.session.requestedCapabilities.includes('remote-session-v1'))) return;
        await delay();
      }
      throw new Error('Session readiness not confirmed; draft retained');
    })());
  }
  public close(id: string, expectedRunId?: string): Promise<void> {
    return this.track((async () => {
      const entry = this.entry(id); const runId = entry.activeRunId;
      if (expectedRunId !== undefined && runId !== expectedRunId) throw new ConversationActionCancelled('Execution changed before close');
      if (runId === null) return;
      const operation = entry.closeOperation?.runId === runId ? entry.closeOperation.id : crypto.randomUUID();
      await this.changeEntry(id, (value) => ({ ...value, closeOperation: { runId, id: operation } }));
      let session = await this.api.close(runId, operation);
      if (session.status === 'DRAINING') await this.changeEntry(id, (value) => ({ ...value, notice: '结束请求已保存，正在等待执行端停止并释放项目。尚未确认前不会开始替代运行。' }));
      for (let attempt = 0; session.status === 'DRAINING' && attempt < 60; attempt += 1) {
        if (session.sessionId !== runId || session.hcEndpointId !== this.endpointId) throw new Error('Closure binding changed');
        await this.controllers.get(runId)?.observe(session);
        await delay(); this.assertOpen();
        session = await this.api.status(runId);
      }
      if (session.sessionId !== runId || session.hcEndpointId !== this.endpointId || !isTerminal(session.status)) throw new Error('Execution closure is not confirmed');
      this.knownClosed.add(runId); this.allowed.delete(runId); delete this.recovery[runId];
      this.unrecoverable = this.unrecoverable.filter((item) => item.sessionId !== runId);
      try { await this.controllers.get(runId)?.observe(session); }
      catch { this.entryFaults.set(id, '运行已结束，但原记录未能同步；可以保留历史并开始新的运行。'); }
      await this.confirmClosureNotice(runId);
      this.emit();
    })());
  }
  public async continueConversation(id: string): Promise<void> {
    const entry = this.entry(id);
    if (entry.creation !== null) throw new Error('Resolve or cancel the pending creation first');
    if (entry.activeRunId !== null && !this.knownClosed.has(entry.activeRunId)) {
      const session = await this.api.status(entry.activeRunId);
      if (session.sessionId !== entry.activeRunId || session.hcEndpointId !== this.endpointId) throw new Error('Execution status binding changed');
      if (!isTerminal(session.status)) throw new Error('Close the old execution before continuing');
      this.knownClosed.add(session.sessionId);
    }
    await this.changeEntry(id, (entry) => ({ ...entry, activeRunId: null, closeOperation: null, archived: false,
      notice: '可以开始新的运行，原历史仍然保留；旧消息不会自动发送。' }));
  }
  public async closeUnrecoverable(runId: string): Promise<void> {
    const session = await this.api.status(runId);
    if (session.sessionId !== runId || session.hcEndpointId !== this.endpointId) throw new Error('Execution status binding changed');
    let entry = [...this.entries.values()].find((entry) => entry.value.activeRunId === runId);
    if (entry === undefined) {
      const initial = this.index.create({ abaEndpointId: session.abaEndpointId, workspaceId: session.workspaceId, runtimeProfileId: session.runtimeProfileId }, '', this.now());
      const value = { ...initial, title: '旧运行状态', runIds: [runId], activeRunId: runId, notice: '这里只恢复运行状态，原对话内容仍需要原有密钥。' };
      entry = { value, revision: null }; this.entries.set(value.id, entry); await this.changeEntry(value.id, (value) => value);
    }
    await this.close(entry.value.id, runId);
  }
  public async checkCreation(id: string, cancel = false): Promise<void> {
    const intent = this.entry(id).creation;
    if (intent === null) {
      if (cancel && this.entry(id).activeRunId !== null) { await this.close(id); await this.continueConversation(id); }
      return;
    }
    const cancelling = cancel || intent.cancelRequested === true;
    if (cancelling) {
      this.cancelledCreations.add(intent.id);
      await this.changeEntry(id, (entry) => ({ ...entry, creation: entry.creation?.id === intent.id ? { ...entry.creation, cancelRequested: true } : entry.creation }));
    }
    const state = await this.api.creation(intent.id, cancelling);
    if (state.state === 'created' && state.session !== null) {
      await this.adoptRun(id, intent, state.session);
      if (cancelling) { await this.close(id); await this.continueConversation(id); }
    } else if (state.state === 'cancelled') {
      await this.changeEntry(id, (entry) => ({ ...entry, creation: null, notice: '创建已取消，草稿保留，可以调整项目后继续。' }));
    } else {
      await this.changeEntry(id, (entry) => ({ ...entry, notice: '原创建仍未确认。可以稍后检查，或明确取消这份创建。' }));
    }
  }
  public rename(id: string, title: string): Promise<void> {
    const value = title.trim();
    if (value === '' || value.length > 120) return Promise.reject(new Error('Conversation title is invalid'));
    return this.changeEntry(id, (entry) => ({ ...entry, title: value }));
  }
  public async archive(id: string, archived: boolean): Promise<void> {
    await this.changeEntry(id, (entry) => ({ ...entry, archived }));
    if (archived && this.snapshot().selectedId === id) await this.select(null);
  }
  public async remove(id: string): Promise<void> {
    const entry = this.entry(id);
    if (entry.creation !== null || this.creating.has(id)) throw new Error('Resolve creation before deleting');
    for (const runId of entry.runIds) {
      const session = await this.api.status(runId);
      if (!isTerminal(session.status)) throw new Error('Close all executions before deleting a conversation');
    }
    for (const runId of entry.runIds) {
      const controller = this.controllers.get(runId);
      if (controller !== undefined) {
        const session = await this.api.status(runId); await controller.observe(session); await controller.dispose();
        const saved = await this.store.read(runId); if (saved !== null) await this.store.delete(saved);
        this.controllers.delete(runId); this.allowed.delete(runId);
      }
    }
    await this.index.delete(this.entries.get(id)!); this.entries.delete(id); this.entryFaults.delete(id);
    if (this.snapshot().selectedId === id) await this.select(null); else this.emit();
  }
  public dispose(): Promise<void> {
    if (this.disposal !== null) return this.disposal;
    this.closed = true; this.disconnect();
    this.disposal = (async () => {
      await Promise.allSettled(this.operations); await this.metadataQueue; await this.controlQueue;
      await Promise.allSettled(this.entryQueues.values());
      await Promise.allSettled(this.adopting.values());
      await Promise.allSettled([...this.controllers.values()].map((controller) => controller.dispose()));
      this.listeners.clear();
    })();
    return this.disposal;
  }
}
