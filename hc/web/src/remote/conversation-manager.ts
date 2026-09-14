import {
  ControlType, decodeWireMessage, SessionKeyPackageSchema, WirePacketSchema,
  type EndpointIdentity, type ReadyGatewayConnection,
} from '@harness/hc-core';
import type { ABAEndpointSummary, EndpointSessionSummary } from '../api';
import { ConversationController, type ConversationTransport, type ConversationView } from './conversation-controller';
import { ConversationStore, hex, isTerminal, MAX_CONVERSATIONS, newConversation,
  type ConversationWorkspace, type CreationIntent, type StoredConversation, type StoredWorkspace } from './conversation-store';
import type { ConfigOption, RpcId } from './runtime-state';

export interface ConversationAPI {
  abas(): Promise<readonly ABAEndpointSummary[]>;
  sessions(): Promise<readonly EndpointSessionSummary[]>;
  create(intent: CreationIntent): Promise<EndpointSessionSummary>;
  close(sessionId: string, operationId: string): Promise<EndpointSessionSummary>;
}
export interface ManagerView {
  readonly status: 'loading' | 'ready' | 'failed'; readonly online: boolean; readonly creating: boolean;
  readonly workspace: ConversationWorkspace | null; readonly conversations: readonly ConversationView[];
  readonly endpoints: readonly ABAEndpointSummary[]; readonly unrecoverable: readonly EndpointSessionSummary[];
  readonly recovery: Readonly<Record<string, string>>; readonly error: string | null; readonly pendingWrites: number;
}
const delay = () => new Promise<void>((resolve) => setTimeout(resolve, 200));
const MAX_BUFFER_BYTES = 4 * 1024 * 1024;

/** A page selects a view; this owner keeps all conversation lifecycles alive. */
export class ConversationManager {
  private readonly controllers = new Map<string, ConversationController>();
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
  private creating = false;
  private pendingWrites = 0;
  private buffer: Uint8Array[] = [];
  private bufferBytes = 0;
  private controlSequence = 0n;
  private controlQueue: Promise<void> = Promise.resolve();
  private checking: Promise<void> | null = null;
  private readonly allowed = new Set<string>();
  private recovery: Record<string, string> = {};
  private endpoints: readonly ABAEndpointSummary[] = [];
  private unrecoverable: readonly EndpointSessionSummary[] = [];
  private error: string | null = null;
  private status: ManagerView['status'] = 'loading';
  private view: ManagerView = { status: 'loading', online: false, creating: false, workspace: null,
    conversations: [], endpoints: [], unrecoverable: [], recovery: {}, error: null, pendingWrites: 0 };
  public constructor(private readonly store: ConversationStore, private readonly identity: EndpointIdentity,
    private readonly endpointId: string, private readonly api: ConversationAPI, private readonly now = Date.now) {}
  public readonly subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };
  public readonly snapshot = (): ManagerView => this.view;
  private emit(): void {
    this.view = { status: this.status, online: this.connection?.socket.readyState === 1 && !this.restoring,
      creating: this.creating, workspace: this.workspace?.value ?? null,
      conversations: [...this.controllers.values()].map((controller) => controller.snapshot()), endpoints: this.endpoints,
      unrecoverable: this.unrecoverable, recovery: { ...this.recovery }, error: this.error, pendingWrites: this.pendingWrites };
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
  private transport(): ConversationTransport {
    return { now: this.now, send: (bytes) => {
      const connection = this.connection;
      if (this.closed || connection === null || connection.socket.readyState !== 1 || connection.socket.bufferedAmount > MAX_BUFFER_BYTES) return false;
      connection.socket.send(new Uint8Array(bytes).buffer); return true;
    }, control: (encode) => {
      const epoch = this.epoch; const connection = this.connection;
      const operation = this.controlQueue.then(async () => {
        if (!this.current(epoch) || connection === null || connection.socket.readyState !== 1) throw new Error('Connection changed before control');
        if (this.controlSequence >= 0xffff_ffff_ffff_ffffn) throw new Error('Control sequence exhausted');
        const bytes = await encode(++this.controlSequence);
        if (!this.current(epoch) || connection !== this.connection || connection.socket.readyState !== 1) throw new Error('Connection changed during control');
        if (connection.socket.bufferedAmount > MAX_BUFFER_BYTES) throw new Error('Connection backpressure');
        connection.socket.send(new Uint8Array(bytes).buffer);
      });
      this.controlQueue = operation.catch(() => undefined); return operation;
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
        const [conversations, workspace] = await Promise.all([this.store.list(), this.store.readWorkspace()]);
        this.assertOpen(); this.workspace = workspace;
        for (const item of conversations) this.add(item);
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
        this.restoring = false; this.flush();
        await Promise.all([...this.controllers.entries()].filter(([id]) => this.allowed.has(id)).map(async ([id, controller]) => {
          try { await controller.resume(); await this.describe(id); }
          catch { this.recovery[id] = '会话恢复未确认，已暂停此对话的操作。请检查执行状态。'; }
        }));
        this.emit();
      } catch (cause) {
        if (this.current(epoch)) { this.restoring = false; this.disconnect(); this.fail('无法验证当前账户、端点或会话授权，已保留加密记录并暂停恢复。'); }
        throw cause;
      }
    })());
  }
  private async reconcile(epoch: number): Promise<void> {
    const [endpoints, sessions] = await Promise.all([this.api.abas(), this.api.sessions()]);
    if (!this.current(epoch)) return;
    this.endpoints = endpoints; const verified = new Set<string>();
    this.unrecoverable = sessions.filter((session) => session.hcEndpointId === this.endpointId && !this.controllers.has(session.sessionId) && !isTerminal(session.status));
    await Promise.all([...this.controllers.entries()].map(async ([id, controller]) => {
      const saved = controller.snapshot().data;
      const session = sessions.find((item) => item.sessionId === id && item.hcEndpointId === this.endpointId);
      const aba = endpoints.find((item) => item.id === saved.aba.id && item.status === 'ACTIVE');
      if (session === undefined) { this.recovery[id] = '当前账户无法确认这份会话的授权，仅保留本地记录。'; return; }
      if (!isTerminal(session.status) && (aba === undefined || aba.signingJkt !== saved.aba.signingJkt)) { this.recovery[id] = '执行设备已失效或密钥指纹变化，无法安全恢复此会话。'; return; }
      try {
        await controller.observe(session);
        if (!this.current(epoch)) return;
        delete this.recovery[id]; verified.add(id);
      } catch { this.recovery[id] = '会话身份或本地保存状态不一致，已暂停操作。'; }
    }));
    if (!this.current(epoch)) return;
    this.allowed.clear(); for (const id of verified) this.allowed.add(id);
    this.emit();
  }
  public refresh(): Promise<void> {
    if (this.checking !== null) return this.checking;
    const epoch = this.epoch;
    const operation = this.track((async () => {
      this.assertOpen(); if (this.connection?.socket.readyState !== 1) return;
      try {
        await this.reconcile(epoch);
        if (this.current(epoch)) await Promise.all([...this.controllers.keys()].map((id) => this.describe(id)));
      } catch (cause) { if (this.current(epoch)) { this.allowed.clear(); this.fail('当前会话授权复核失败，已暂停新操作。请检查连接或重新登录。'); } throw cause; }
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
    if (this.closed) return;
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
      if (controller === undefined) { if (this.creating) this.retain(bytes); return; }
      if (!this.allowed.has(id) || this.recovery[id] !== undefined) return;
      const routedId = id;
      // Core validates signatures, channel, generation and endpoint after this untrusted routing hint.
      void controller.receive(bytes).then(() => this.describe(routedId)).catch(() => { this.recovery[routedId] = '消息验证或保存失败，此对话已暂停。'; this.emit(); });
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
    if (id !== null && !this.controllers.has(id)) return Promise.reject(new Error('Unknown conversation'));
    return this.metadata((value) => ({ ...value, selectedId: id }));
  }
  public draft(id: string | null, text: string): Promise<void> {
    this.assertOpen();
    if (text.length > 16_000) return Promise.reject(new Error('Draft exceeds limit'));
    if (id === null) return this.metadata((value) => ({ ...value, draft: text }));
    const controller = this.controllers.get(id);
    if (controller === undefined) return Promise.reject(new Error('Unknown draft conversation'));
    return controller.setDraft(text);
  }
  private actionable(id: string): ConversationController {
    this.assertOpen();
    const controller = this.controllers.get(id);
    if (this.restoring || this.connection?.socket.readyState !== 1 || !this.allowed.has(id) || this.recovery[id] !== undefined || controller === undefined) throw new Error('Conversation authorization is unavailable');
    return controller;
  }
  public workspaceConflict(abaId: string, workspaceId: string, except?: string): boolean {
    return [...this.controllers.entries()].some(([id, controller]) => {
      const value = controller.snapshot().data;
      return id !== except && value.aba.id === abaId && value.session.workspaceId === workspaceId && !isTerminal(value.session.status) &&
        (value.awaiting !== null || value.blocked !== null || value.session.status === 'UNCERTAIN' || value.requests.length > 0);
    }) || this.unrecoverable.some((value) => value.abaEndpointId === abaId && value.workspaceId === workspaceId);
  }
  public prompt(id: string, text: string): Promise<void> {
    const controller = this.actionable(id); const value = controller.snapshot().data;
    if (this.workspaceConflict(value.aba.id, value.session.workspaceId, id)) return Promise.reject(new Error('Another conversation is using this workspace'));
    return controller.prompt(text);
  }
  public configure(id: string, option: ConfigOption, value: string): Promise<void> { return this.actionable(id).configure(option, value); }
  public cancel(id: string): Promise<void> { return this.actionable(id).cancel(); }
  public decide(id: string, request: RpcId, option: string | null): Promise<void> { return this.actionable(id).decide(request, option); }
  public describeNow(id: string): Promise<void> { return this.actionable(id).describe(); }
  public create(input: Omit<CreationIntent, 'id'>, retry = false): Promise<string> {
    return this.track((async () => {
      this.assertOpen();
      if (this.workspace === null || this.restoring || this.connection?.socket.readyState !== 1 || this.creating) throw new Error('Cannot create a conversation now');
      if (this.controllers.size >= MAX_CONVERSATIONS) throw new Error('Local conversation capacity reached');
      if (this.workspace.value.creation !== null && !retry) throw new Error('Previous creation needs reconciliation');
      const intent: CreationIntent = this.workspace.value.creation ?? { ...input, id: crypto.randomUUID() };
      const aba = this.endpoints.find((item) => item.id === intent.abaEndpointId && item.status === 'ACTIVE');
      if (aba === undefined || this.workspaceConflict(aba.id, intent.workspaceId)) throw new Error('Execution workspace is unavailable or busy');
      this.creating = true; this.error = null; this.emit(); const epoch = this.epoch;
      try {
        await this.metadata((value) => ({ ...value, creation: intent }));
        if (!this.current(epoch)) throw new Error('Connection changed before create');
        const session = await this.api.create(intent);
        if (!this.current(epoch)) throw new Error('Connection changed during create');
        if (session.hcEndpointId !== this.endpointId || session.abaEndpointId !== intent.abaEndpointId || session.workspaceId !== intent.workspaceId || session.runtimeProfileId !== intent.runtimeProfileId) throw new Error('Created session binding mismatch');
        const existing = this.controllers.get(session.sessionId);
        if (existing === undefined) {
          const value = newConversation(session, aba, this.identity, intent.draft);
          const revision = await this.store.write(value, null); this.add({ revision, value });
        }
        if (!this.current(epoch)) throw new Error('Connection changed while saving session');
        this.allowed.add(session.sessionId);
        await this.metadata((value) => ({ ...value, creation: null, selectedId: session.sessionId,
          draft: value.draft === intent.draft ? '' : value.draft }));
        this.unrecoverable = this.unrecoverable.filter((item) => item.sessionId !== session.sessionId);
        this.flush(); this.emit(); return session.sessionId;
      } catch (cause) { this.fail('会话创建结果尚未确认。草稿和原创建编号已保留，请先确认原请求，避免创建重复会话。'); throw cause; }
      finally { this.creating = false; this.buffer = []; this.bufferBytes = 0; this.emit(); }
    })());
  }
  public waitReady(id: string): Promise<void> {
    return this.track((async () => {
      const epoch = this.epoch;
      for (let attempt = 0; attempt < 100 && this.current(epoch); attempt += 1) {
        const view = this.controllers.get(id)?.snapshot();
        if (view === undefined || isTerminal(view.data.session.status) || view.fault !== null || view.data.blocked !== null) throw new Error('Session cannot become ready');
        if (view.data.session.status === 'ACTIVE' && view.data.keys !== null && (view.data.runtime.status === 'ready' || !view.data.session.requestedCapabilities.includes('remote-session-v1'))) return;
        await delay();
      }
      throw new Error('Session readiness not confirmed; draft retained');
    })());
  }
  public close(id: string): Promise<void> {
    return this.track((async () => {
      const controller = this.actionable(id); const epoch = this.epoch;
      const operation = await controller.closeOperation();
      if (!this.current(epoch)) throw new Error('Connection changed before close');
      const session = await this.api.close(id, operation);
      if (this.current(epoch)) await controller.observe(session);
    })());
  }
  public dispose(): Promise<void> {
    if (this.disposal !== null) return this.disposal;
    this.closed = true; this.disconnect();
    this.disposal = (async () => {
      await Promise.allSettled([...this.operations]); await this.metadataQueue; await this.controlQueue;
      await Promise.allSettled([...this.controllers.values()].map((controller) => controller.dispose()));
      this.listeners.clear();
    })();
    return this.disposal;
  }
}
