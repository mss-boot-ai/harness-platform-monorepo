import { ControlType, createWireMessage, decodeWireMessage, encodeWireMessage, SessionKeyPackageSchema, WirePacketSchema, type ReadyGatewayConnection } from '@harness/hc-core';
import { ConversationController } from './conversation-controller';
import { ConversationActionCancelled, ConversationManager, type ConversationAPI } from './conversation-manager';
import { controllerFixture, testEndpoint, testNow } from './conversation-fixture';
import { startTurn } from '../chat/model';
import { HcApiError, type EndpointSessionSummary } from '../api';
class Socket extends EventTarget {
  public readyState = 1; public bufferedAmount = 0; public sent: Uint8Array[] = [];
  public send(bytes: ArrayBuffer): void { this.sent.push(new Uint8Array(bytes).slice()); }
  public close(): void { this.readyState = 3; this.dispatchEvent(new Event('close')); }
  public receive(bytes: Uint8Array): void { this.dispatchEvent(new MessageEvent('message', { data: new Uint8Array(bytes).buffer })); }
}
function connection(socket: Socket, generation = 1n): ReadyGatewayConnection {
  return { socket: socket as unknown as WebSocket, connectionGeneration: generation, connectionId: new Uint8Array(16),
    fencingToken: new Uint8Array(32), heartbeatIntervalMs: 1000, rootJkt: 'fixture-root' };
}
const managers: ConversationManager[] = [];
afterEach(async () => { await Promise.all(managers.splice(0).map((manager) => manager.dispose())); });
async function fixture(released = false) {
  const a = await controllerFixture();
  const b = await controllerFixture({ identity: a.identity, sessionId: '04'.repeat(16), abaId: '05'.repeat(16), workspaceId: 'other' });
  if (released) {
    a.session = { ...a.session, status: 'CLOSED' }; a.value = { ...a.value, session: a.session, keys: null };
    b.session = { ...b.session, status: 'CLOSED' }; b.value = { ...b.value, session: b.session, keys: null };
    const previous = await a.store.read(a.session.sessionId); if (previous === null) throw new Error('Missing fixture');
    await a.store.write(a.value, previous.revision);
  }
  await a.store.write(b.value, null);
  let sessions: readonly EndpointSessionSummary[] = [a.session, b.session];
  let nextSession = 6;
  const created = new Map<string, EndpointSessionSummary>();
  const api: ConversationAPI = { abas: vi.fn(async () => [a.value.aba, b.value.aba]), sessions: vi.fn(async () => sessions),
    create: vi.fn(async (intent) => {
      const old = created.get(intent.id); if (old !== undefined) return old;
      const value = { ...a.session, sessionId: (nextSession++).toString(16).padStart(2, '0').repeat(16), abaEndpointId: intent.abaEndpointId,
        workspaceId: intent.workspaceId, runtimeProfileId: intent.runtimeProfileId, status: 'CREATING' as const };
      created.set(intent.id, value); sessions = [...sessions, value]; return value;
    }),
    status: vi.fn(async (id) => { const value = sessions.find((item) => item.sessionId === id); if (value === undefined) throw new HcApiError('Missing session', 'SESSION_NOT_FOUND', 404); return value; }),
    statuses: vi.fn(async (ids) => sessions.filter((item) => ids.includes(item.sessionId))),
    creation: vi.fn(async (id, cancel) => { const value = created.get(id); return value === undefined ? { state: cancel ? 'cancelled' as const : 'not-found' as const, session: null } : { state: 'created' as const, session: value }; }),
    close: vi.fn(async (id) => {
      const original = sessions.find((item) => item.sessionId === id); if (original === undefined) throw new Error('Missing session');
      const closed = { ...original, status: 'CLOSED' as const }; sessions = sessions.map((item) => item.sessionId === id ? closed : item); return closed;
    }) };
  const manager = new ConversationManager(a.store, a.identity, testEndpoint, api, () => testNow); managers.push(manager);
  const socket = new Socket();
  return { a, b, api, manager, socket, setSessions: (value: readonly EndpointSessionSummary[]) => { sessions = value; } };
}
const chunk = (sessionId: string, text: string) => ({ jsonrpc: '2.0', method: 'session/update', params: { sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } } } });
describe('endpoint conversation coordination', () => {
  it('exposes idle and archived workspace ownership until the original runtime is confirmed closed', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const target = { abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture' };
    const next = await f.manager.newConversation(target, 'new context');
    expect(f.manager.workspaceOccupants(target.abaEndpointId, target.workspaceId, next)[0]?.conversationId).toBe(f.a.session.sessionId);
    await f.manager.archive(f.a.session.sessionId, true);
    await expect(f.manager.create({ ...target, draft: 'new context' }, false, next)).rejects.toThrow('busy');
    expect(f.api.create).not.toHaveBeenCalled(); expect(f.api.close).not.toHaveBeenCalled();
    await f.manager.close(f.a.session.sessionId);
    expect(f.manager.workspaceConflict(target.abaEndpointId, target.workspaceId, next)).toBe(false);
    expect((await f.manager.create({ ...target, draft: 'new context' }, false, next)).conversationId).toBe(next);
  });
  it('waits for confirmed host shutdown and preserves the close operation while draining', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const id = f.a.session.sessionId;
    vi.mocked(f.api.close).mockResolvedValueOnce({ ...f.a.session, status: 'DRAINING' });
    let confirm: (value: EndpointSessionSummary) => void = () => undefined;
    vi.mocked(f.api.status).mockImplementationOnce(() => new Promise((resolve) => { confirm = resolve; }));
    const closing = f.manager.close(id);
    await vi.waitFor(() => expect(f.api.status).toHaveBeenCalled());
    expect(f.manager.executionClosed(id)).toBe(false);
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === id)?.closeOperation).not.toBeNull();
    expect(f.manager.snapshot().runs.find((run) => run.data.session.sessionId === id)?.data.session.status).toBe('DRAINING');
    const closed = { ...f.a.session, status: 'CLOSED' as const }; f.setSessions([closed, f.b.session]);
    confirm(closed); await closing;
    expect(f.manager.executionClosed(id)).toBe(true);
  });
  it('reuses the original close operation after a lost shutdown confirmation', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const id = f.a.session.sessionId;
    vi.mocked(f.api.close).mockResolvedValueOnce({ ...f.a.session, status: 'DRAINING' });
    vi.mocked(f.api.status).mockRejectedValueOnce(new Error('confirmation lost'));
    await expect(f.manager.close(id)).rejects.toThrow('confirmation lost');
    expect(f.manager.executionClosed(id)).toBe(false);
    await f.manager.close(id);
    expect(vi.mocked(f.api.close).mock.calls[0]?.[1]).toBe(vi.mocked(f.api.close).mock.calls[1]?.[1]);
    expect(f.manager.executionClosed(id)).toBe(true);
  });
  it('creates a local conversation independently of old runs and scopes failed creation to it', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const input = { abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'first draft' };
    vi.mocked(f.api.create).mockRejectedValueOnce(new Error('response lost'));
    await expect(f.manager.create(input)).rejects.toThrow('response lost');
    const firstId = f.manager.snapshot().selectedId!;
    const intent = f.manager.snapshot().conversations.find((entry) => entry.id === firstId)?.creation;
    expect(intent).not.toBeNull();
    expect(f.manager.workspaceConflict(input.abaEndpointId, input.workspaceId, firstId)).toBe(false);
    expect(f.manager.workspaceConflict(input.abaEndpointId, input.workspaceId)).toBe(true);
    const other = await f.manager.newConversation({ abaEndpointId: f.b.value.aba.id, workspaceId: 'other', runtimeProfileId: 'fixture' }, 'second draft');
    expect(other).not.toBe(firstId); expect(f.manager.snapshot().creating).toBe(false);
    const created = await f.manager.create({ abaEndpointId: f.b.value.aba.id, workspaceId: 'other', runtimeProfileId: 'fixture', draft: 'second draft' }, false, other);
    expect(created.conversationId).toBe(other); expect(created.runId).not.toBe(other);
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === firstId)?.creation?.id).toBe(intent?.id);
    expect(f.api.close).not.toHaveBeenCalled();
  });
  it('releases a proven rejected creation but retains its local draft', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const input = { abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'keep rejected draft' };
    vi.mocked(f.api.create).mockRejectedValueOnce(new HcApiError('Target rejected', 'EXECUTION_TARGET_NOT_ALLOWED', 400));
    await expect(f.manager.create(input)).rejects.toThrow('Target rejected');
    const entry = f.manager.snapshot().conversations.find((entry) => entry.id === f.manager.snapshot().selectedId)!;
    expect(entry.creation).toBeNull(); expect(entry.draft).toBe(input.draft); expect(entry.notice).toContain('调整项目');
    const next = await f.manager.create(input, false, entry.id);
    expect(next.conversationId).toBe(entry.id);
    expect(vi.mocked(f.api.create).mock.calls[0]![0].id).not.toBe(vi.mocked(f.api.create).mock.calls[1]![0].id);
  });
  it('cancels an in-flight creation without late adoption or automatic prompt dispatch', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    let finish: (session: EndpointSessionSummary) => void = () => undefined;
    vi.mocked(f.api.create).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const creating = f.manager.create({ abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'cancelled draft' }).catch((error: unknown) => error);
    await vi.waitFor(() => expect(f.api.create).toHaveBeenCalled());
    const id = f.manager.snapshot().selectedId!;
    const run = { ...f.a.session, sessionId: '06'.repeat(16), status: 'CREATING' as const };
    f.setSessions([f.a.session, f.b.session, run]);
    vi.mocked(f.api.creation).mockResolvedValueOnce({ state: 'created', session: run });
    await f.manager.checkCreation(id, true);
    finish(run); expect(await creating).toBeInstanceOf(ConversationActionCancelled);
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === id)?.activeRunId).toBeNull();
    expect(f.api.close).toHaveBeenCalledWith(run.sessionId, expect.any(String));
    expect(f.socket.sent.some((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted')).toBe(false);
  });
  it('retains conversation identity across runs and rejects stale-run actions', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const id = f.a.session.sessionId;
    await f.manager.close(id); await f.manager.continueConversation(id);
    const next = await f.manager.create({ abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'new run' }, false, id);
    expect(next.conversationId).toBe(id); expect(next.runId).not.toBe(id);
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === id)?.runIds).toEqual([id, next.runId]);
    expect(() => f.manager.prompt(id, 'stale continuation', id)).toThrow(ConversationActionCancelled);
  });
  it('preserves drafts typed while an earlier prompt is being encrypted', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const id = f.a.session.sessionId; await f.manager.draft(id, 'submitted');
    let release: () => void = () => undefined; let entered = false;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const sign = crypto.subtle.sign.bind(crypto.subtle);
    vi.spyOn(crypto.subtle, 'sign').mockImplementationOnce(async (...args) => { entered = true; await gate; return sign(...args); });
    const sent = f.manager.prompt(id, 'submitted'); await vi.waitFor(() => expect(entered).toBe(true));
    await f.manager.draft(id, 'typed while sending'); release(); await sent;
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === id)?.draft).toBe('typed while sending');
  });
  it('renames and archives without cancelling active work or releasing its workspace', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const id = f.a.session.sessionId; await f.manager.select(id); await f.manager.prompt(id, 'background work');
    await f.manager.rename(id, 'Named conversation'); await f.manager.archive(id, true);
    const saved = await f.a.store.conversationIndex().read(id);
    expect(saved?.value.title).toBe('Named conversation'); expect(saved?.value.archived).toBe(true);
    expect(f.manager.snapshot().selectedId).toBeNull(); expect(f.api.close).not.toHaveBeenCalled();
    expect(f.manager.workspaceConflict(f.a.value.aba.id, 'fixture')).toBe(true);
  });
  it('can close and continue when one execution record is corrupt without replacing that record', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.dispose();
    const id = f.a.session.sessionId; const recordId = `remote-v1/${testEndpoint}/${id}`;
    const old = await f.a.store.read(id);
    await f.a.vault.write(recordId, new TextEncoder().encode('{}'), old!.revision);
    const manager = new ConversationManager(f.a.store, f.a.identity, testEndpoint, f.api, () => testNow); managers.push(manager);
    await manager.load(); await manager.bind(connection(new Socket()));
    expect(manager.snapshot().storageIssues).toHaveLength(1);
    await manager.close(id); await manager.continueConversation(id);
    const next = await manager.create({ abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'continue' }, false, id);
    expect(next.conversationId).toBe(id);
    expect(new TextDecoder().decode((await f.a.vault.read(recordId))!.bytes)).toBe('{}');
  });
  it('fences delayed control signatures on replacement without poisoning either conversation', async () => {
    const f = await fixture(); await f.manager.load();
    let release: () => void = () => undefined; let entered = false;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const sign = crypto.subtle.sign.bind(crypto.subtle);
    vi.spyOn(crypto.subtle, 'sign').mockImplementationOnce(async (...args) => { entered = true; await gate; return sign(...args); });
    const old = f.manager.bind(connection(f.socket)); await vi.waitFor(() => expect(entered).toBe(true));
    const next = new Socket(); const replacing = f.manager.bind(connection(next, 2n));
    release(); await Promise.all([old, replacing]);
    expect(f.socket.sent).toHaveLength(0); expect(f.manager.snapshot().online).toBe(true);
    expect(f.manager.snapshot().runs.every((item) => item.fault === null && item.data.blocked === null)).toBe(true);
    const sequences = next.sent.map((bytes) => decodeWireMessage(WirePacketSchema, bytes)).flatMap((packet) => packet.body.case === 'control' ? [packet.body.value.controlSequence] : []);
    expect(sequences).toEqual([1n, 2n]);
  });
  it('preserves an unsent packet under backpressure and replays only its original bytes after reconnect', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    f.socket.bufferedAmount = 4 * 1024 * 1024 + 1;
    await f.manager.prompt(f.a.session.sessionId, 'saved but unsent');
    expect(f.manager.snapshot().online).toBe(false); expect(f.manager.snapshot().error).toContain('积压');
    expect(f.socket.sent.filter((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted')).toHaveLength(0);
    const saved = await f.a.store.read(f.a.session.sessionId); const packet = saved?.value.outbox[0];
    expect(saved?.value.outbox).toHaveLength(1); expect(saved?.value.awaiting).not.toBeNull(); expect(f.api.close).not.toHaveBeenCalled();
    const next = new Socket(); await f.manager.bind(connection(next, 2n));
    const sent = next.sent.filter((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted');
    expect(sent).toHaveLength(1); expect(Buffer.from(sent[0]!).toString('base64url')).toBe(packet?.encoded);
    expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data.outbound).toBe('1');
    expect(f.api.close).not.toHaveBeenCalled();
  });
  it('keeps a new creation grant when an older list response finishes and routes its key package', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    let release: (sessions: readonly EndpointSessionSummary[]) => void = () => undefined;
    vi.mocked(f.api.sessions).mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
    const refreshing = f.manager.refresh(); await vi.waitFor(() => expect(f.api.sessions).toHaveBeenCalledTimes(2));
    const { runId: id } = await f.manager.create({ abaEndpointId: f.a.session.abaEndpointId, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'new creation' });
    const created = { ...f.a.session, sessionId: id, status: 'WAITING_KEY' as const }; f.setSessions([f.a.session, f.b.session, created]);
    release([f.a.session, f.b.session]); await refreshing;
    expect(f.manager.snapshot().recovery[id]).toBeUndefined();
    // This test isolates coordinator routing. Core HPKE/signature verification has its own real-crypto tests.
    const receive = vi.spyOn(ConversationController.prototype, 'receive').mockResolvedValueOnce(undefined);
    const packet = encodeWireMessage(WirePacketSchema, createWireMessage(WirePacketSchema, { wireMajor: 1, wireMinor: 0,
      packetId: new Uint8Array(16).fill(8), body: { case: 'control', value: { type: ControlType.SESSION_KEY_PACKAGE,
        payload: encodeWireMessage(SessionKeyPackageSchema, createWireMessage(SessionKeyPackageSchema, {
          sessionId: Uint8Array.from(id.match(/../gu) ?? [], (byte) => Number.parseInt(byte, 16)),
        })) } } }));
    f.socket.receive(packet); expect(receive).toHaveBeenCalledWith(packet);
    expect(f.manager.snapshot().recovery[f.a.session.sessionId]).toBeUndefined();
  });
  it('holds a final frame without ACK while unauthorized, then recovers it once after fresh authorization', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    await f.manager.prompt(f.a.session.sessionId, 'one operation');
    const request = f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data.awaiting;
    f.setSessions([f.b.session]); await f.manager.refresh();
    const final = await f.a.incoming([chunk(f.a.session.sessionId, 'final result'), { jsonrpc: '2.0', id: request, result: { stopReason: 'end_turn' } }], 1n);
    const before = f.socket.sent.length; f.socket.receive(final);
    expect(f.socket.sent).toHaveLength(before);
    expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data.inbound).toBe('0');
    f.setSessions([f.a.session, f.b.session]); await f.manager.refresh();
    const restored = f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data;
    expect(restored?.awaiting).toBeNull(); expect(restored?.messages[1]?.text).toBe('final result'); expect(restored?.inbound).toBe('1');
    f.socket.receive(final);
    await vi.waitFor(() => expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.pending).toBe(0));
    expect((await f.a.store.read(f.a.session.sessionId))?.value.messages).toEqual(restored?.messages);
    const requests = f.socket.sent.filter((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted');
    for (const bytes of requests) expect(bytes).toEqual(requests[0]);
  });
  it('actively removes a changed ABA fingerprint instead of permanently retaining its grant', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    vi.mocked(f.api.abas).mockResolvedValueOnce([{ ...f.a.value.aba, signingJkt: 'changed' }, f.b.value.aba]);
    await f.manager.refresh(); expect(() => f.manager.prompt(f.a.session.sessionId, 'blocked')).toThrow('authorization');
    expect(f.manager.snapshot().recovery[f.a.session.sessionId]).toContain('指纹');
  });
  it('binds immediate typing to visible selection while older metadata writes are delayed', async () => {
    const f = await fixture(); await f.manager.load();
    const write = f.a.store.writeWorkspace.bind(f.a.store); let release: () => void = () => undefined;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    vi.spyOn(f.a.store, 'writeWorkspace').mockImplementationOnce(async (...args) => { await gate; return write(...args); });
    const first = f.manager.select(f.a.session.sessionId);
    expect(f.manager.snapshot().selectedId).toBe(f.a.session.sessionId);
    await f.manager.draft(f.manager.snapshot().selectedId, 'A first edit');
    const second = f.manager.select(f.b.session.sessionId);
    expect(f.manager.snapshot().selectedId).toBe(f.b.session.sessionId);
    await f.manager.draft(f.manager.snapshot().selectedId, 'B immediate edit');
    const third = f.manager.select(f.a.session.sessionId);
    await f.manager.draft(f.manager.snapshot().selectedId, 'A final edit');
    release(); await Promise.all([first, second, third]);
    expect(f.manager.snapshot().selectedId).toBe(f.a.session.sessionId);
    expect((await f.a.store.conversationIndex().read(f.a.session.sessionId))?.value.draft).toBe('A final edit');
    expect((await f.a.store.conversationIndex().read(f.b.session.sessionId))?.value.draft).toBe('B immediate edit');
    expect((await f.a.store.readWorkspace()).value.selectedId).toBe(f.a.session.sessionId);
  });
  it('keeps the intended visible destination when selection persistence fails', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.select(f.a.session.sessionId);
    vi.spyOn(f.a.store, 'writeWorkspace').mockRejectedValueOnce(new Error('disk full'));
    const selecting = f.manager.select(f.b.session.sessionId);
    expect(f.manager.snapshot().selectedId).toBe(f.b.session.sessionId);
    await expect(selecting).rejects.toThrow('disk full');
    expect(f.manager.snapshot().selectedId).toBe(f.b.session.sessionId); expect(f.manager.snapshot().status).toBe('failed');
    expect((await f.a.store.readWorkspace()).value.selectedId).toBe(f.a.session.sessionId);
  });
  it('does not steal a newer navigation choice when an earlier create response completes', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    let finish: (session: EndpointSessionSummary) => void = () => undefined;
    vi.mocked(f.api.create).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const creating = f.manager.create({ abaEndpointId: f.a.session.abaEndpointId, runtimeProfileId: 'fixture', workspaceId: 'fixture', draft: 'new session' });
    await vi.waitFor(() => expect(f.api.create).toHaveBeenCalled());
    await f.manager.select(f.b.session.sessionId);
    finish({ ...f.a.session, sessionId: '06'.repeat(16), status: 'CREATING' }); await creating;
    expect(f.manager.snapshot().selectedId).toBe(f.b.session.sessionId);
    expect((await f.a.store.readWorkspace()).value.selectedId).toBe(f.b.session.sessionId);
  });
  it('fences conflicting pre-upgrade idle runs until one is explicitly closed', async () => {
    const f = await fixture();
    const second = { ...f.b.value, aba: f.a.value.aba, session: { ...f.b.session, abaEndpointId: f.a.session.abaEndpointId, workspaceId: f.a.session.workspaceId } };
    await f.a.store.write(second, 1); f.setSessions([f.a.session, second.session]);
    await f.manager.load(); await f.manager.bind(connection(f.socket));
    await expect(f.manager.prompt(f.a.session.sessionId, 'first workspace writer')).rejects.toThrow('workspace');
    await expect(f.manager.prompt(f.b.session.sessionId, 'competing writer')).rejects.toThrow('workspace');
    await f.manager.close(f.b.session.sessionId);
    await f.manager.prompt(f.a.session.sessionId, 'one remaining workspace writer');
    expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.b.session.sessionId)?.data.awaiting).toBeNull();
  });
  it('routes interleaved events with colliding RPC IDs to independent durable controllers', async () => {
    const f = await fixture();
    for (const value of [f.a.value, f.b.value]) await f.a.store.write({ ...value, awaiting: 'same-request', messages: startTurn([], 'same-request', value.session.workspaceId) }, 1);
    await f.manager.load(); await f.manager.bind(connection(f.socket));
    await f.manager.select(f.a.session.sessionId);
    await Promise.all([f.manager.draft(f.a.session.sessionId, 'draft A'), f.manager.draft(f.b.session.sessionId, 'draft B')]);
    f.socket.receive(await f.b.incoming(chunk(f.b.session.sessionId, 'reply B'), 1n));
    f.socket.receive(await f.a.incoming(chunk(f.a.session.sessionId, 'reply A'), 1n));
    await vi.waitFor(() => expect(f.manager.snapshot().runs.map((item) => item.data.inbound)).toEqual(['1', '1']));
    const a = f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId);
    const b = f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.b.session.sessionId);
    expect(a?.data.messages[1]?.text).toBe('reply A'); expect(b?.data.messages[1]?.text).toBe('reply B');
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === f.a.session.sessionId)?.draft).toBe('draft A');
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === f.b.session.sessionId)?.draft).toBe('draft B');
    await f.manager.select(null); expect(f.api.close).not.toHaveBeenCalled();
    expect((await f.a.store.read(f.b.session.sessionId))?.value.messages[1]?.text).toBe('reply B');
  });
  it('uses one serialized control sequence for all conversations and resets it only on reconnect', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const sequences = (socket: Socket) => socket.sent.map((bytes) => decodeWireMessage(WirePacketSchema, bytes)).flatMap((packet) => packet.body.case === 'control' ? [packet.body.value.controlSequence] : []);
    expect(sequences(f.socket)).toEqual([1n, 2n]);
    await f.manager.prompt(f.a.session.sessionId, 'first');
    const first = f.socket.sent.find((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted');
    const next = new Socket(); await f.manager.bind(connection(next, 2n));
    expect(sequences(next)).toEqual([1n, 2n]);
    expect(next.sent.find((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted')).toEqual(first);
    expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data.outbound).toBe('1');
  });
  it('retains an ambiguous creation intent and retries only its original idempotency identity', async () => {
    const f = await fixture(true); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const input = { abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'keep this draft' };
    vi.mocked(f.api.create).mockRejectedValueOnce(new Error('response lost'));
    await f.manager.draft(null, input.draft);
    await expect(f.manager.create(input)).rejects.toThrow('response lost');
    const selectedId = f.manager.snapshot().selectedId!;
    const saved = await f.a.store.conversationIndex().read(selectedId); expect(saved?.value.creation?.draft).toBe(input.draft);
    await expect(f.manager.create(input)).rejects.toThrow('reconciliation');
    const { runId: id } = await f.manager.create(input, true);
    expect(vi.mocked(f.api.create).mock.calls[0]?.[0].id).toBe(vi.mocked(f.api.create).mock.calls[1]?.[0].id);
    expect(f.manager.snapshot().conversations.find((entry) => entry.id === selectedId)?.creation).toBeNull();
    expect((await f.a.store.read(id))?.value.draft).toBe(input.draft);
    expect(f.socket.sent.some((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted')).toBe(false);
  });
  it('revalidates authorization before replay and prevents a missing session from sending', async () => {
    const f = await fixture(); await f.a.controller.prompt('saved operation');
    f.setSessions([f.b.session]); await f.manager.load(); await f.manager.bind(connection(f.socket));
    expect(f.manager.snapshot().recovery[f.a.session.sessionId]).toContain('授权');
    expect(() => f.manager.prompt(f.a.session.sessionId, 'forbidden')).toThrow('authorization');
    expect(f.socket.sent.some((bytes) => decodeWireMessage(WirePacketSchema, bytes).body.case === 'encrypted')).toBe(false);
  });
  it('fences delayed authorization responses and old-socket messages after replacement', async () => {
    const f = await fixture(); await f.manager.load();
    let finish: (value: readonly EndpointSessionSummary[]) => void = () => undefined;
    vi.mocked(f.api.sessions).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const binding = f.manager.bind(connection(f.socket));
    await vi.waitFor(() => expect(f.api.sessions).toHaveBeenCalled());
    const replacement = new Socket(); await f.manager.bind(connection(replacement, 2n));
    const count = replacement.sent.length; finish([f.a.session, f.b.session]); await binding;
    f.socket.receive(await f.a.incoming(chunk(f.a.session.sessionId, 'stale'), 1n));
    expect(replacement.sent).toHaveLength(count); expect(f.manager.snapshot().online).toBe(true);
    expect(f.manager.snapshot().runs[0]?.data.inbound).toBe('0');
  });
  it('bounds restore buffering and stops the connection when its byte limit is exceeded', async () => {
    const f = await fixture(); await f.manager.load();
    let finish: (value: readonly EndpointSessionSummary[]) => void = () => undefined;
    vi.mocked(f.api.sessions).mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    const pending = f.manager.bind(connection(f.socket)); await vi.waitFor(() => expect(f.api.sessions).toHaveBeenCalled());
    for (let index = 0; index < 5; index += 1) f.socket.receive(new Uint8Array(1_048_576));
    expect(f.socket.readyState).toBe(3); expect(f.manager.snapshot().error).toContain('缓存上限');
    finish([f.a.session, f.b.session]); await pending;
  });
  it('closes only the requested session and drains already accepted draft writes on disposal', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    await f.manager.close(f.a.session.sessionId);
    expect(f.manager.snapshot().runs.find((item) => item.data.session.sessionId === f.b.session.sessionId)?.data.session.status).toBe('ACTIVE');
    const draft = f.manager.draft(f.b.session.sessionId, 'last draft'); const compose = f.manager.draft(null, 'new chat draft');
    await f.manager.dispose(); await Promise.all([draft, compose]);
    expect((await f.a.store.conversationIndex().read(f.b.session.sessionId))?.value.draft).toBe('last draft');
    expect((await f.a.store.readWorkspace()).value.draft).toBe('new chat draft');
    expect(f.api.close).toHaveBeenCalledTimes(1);
  });
});
