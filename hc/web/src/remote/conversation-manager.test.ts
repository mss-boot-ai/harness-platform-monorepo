import { decodeWireMessage, WirePacketSchema, type ReadyGatewayConnection } from '@harness/hc-core';
import { ConversationManager, type ConversationAPI } from './conversation-manager';
import { controllerFixture, testEndpoint, testNow } from './conversation-fixture';
import { startTurn } from '../chat/model';
import type { EndpointSessionSummary } from '../api';
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
async function fixture() {
  const a = await controllerFixture();
  const b = await controllerFixture({ identity: a.identity, sessionId: '04'.repeat(16), abaId: '05'.repeat(16), workspaceId: 'other' });
  await a.store.write(b.value, null);
  let sessions: readonly EndpointSessionSummary[] = [a.session, b.session];
  const api: ConversationAPI = { abas: vi.fn(async () => [a.value.aba, b.value.aba]), sessions: vi.fn(async () => sessions),
    create: vi.fn(async () => ({ ...a.session, sessionId: '06'.repeat(16), status: 'CREATING' as const })),
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
  it('routes interleaved events with colliding RPC IDs to independent durable controllers', async () => {
    const f = await fixture();
    for (const value of [f.a.value, f.b.value]) await f.a.store.write({ ...value, awaiting: 'same-request', messages: startTurn([], 'same-request', value.session.workspaceId) }, 1);
    await f.manager.load(); await f.manager.bind(connection(f.socket));
    await f.manager.select(f.a.session.sessionId);
    await Promise.all([f.manager.draft(f.a.session.sessionId, 'draft A'), f.manager.draft(f.b.session.sessionId, 'draft B')]);
    f.socket.receive(await f.b.incoming(chunk(f.b.session.sessionId, 'reply B'), 1n));
    f.socket.receive(await f.a.incoming(chunk(f.a.session.sessionId, 'reply A'), 1n));
    await vi.waitFor(() => expect(f.manager.snapshot().conversations.map((item) => item.data.inbound)).toEqual(['1', '1']));
    const a = f.manager.snapshot().conversations.find((item) => item.data.session.sessionId === f.a.session.sessionId);
    const b = f.manager.snapshot().conversations.find((item) => item.data.session.sessionId === f.b.session.sessionId);
    expect(a?.data.messages[1]?.text).toBe('reply A'); expect(b?.data.messages[1]?.text).toBe('reply B');
    expect(a?.data.draft).toBe('draft A'); expect(b?.data.draft).toBe('draft B');
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
    expect(f.manager.snapshot().conversations.find((item) => item.data.session.sessionId === f.a.session.sessionId)?.data.outbound).toBe('1');
  });
  it('retains an ambiguous creation intent and retries only its original idempotency identity', async () => {
    const f = await fixture(); await f.manager.load(); await f.manager.bind(connection(f.socket));
    const input = { abaEndpointId: f.a.value.aba.id, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'keep this draft' };
    vi.mocked(f.api.create).mockRejectedValueOnce(new Error('response lost'));
    await f.manager.draft(null, input.draft);
    await expect(f.manager.create(input)).rejects.toThrow('response lost');
    const saved = await f.a.store.readWorkspace(); expect(saved.value.creation?.draft).toBe(input.draft);
    await expect(f.manager.create(input)).rejects.toThrow('reconciliation');
    const id = await f.manager.create(input, true);
    expect(vi.mocked(f.api.create).mock.calls[0]?.[0].id).toBe(vi.mocked(f.api.create).mock.calls[1]?.[0].id);
    expect(f.manager.snapshot().workspace?.creation).toBeNull();
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
    expect(f.manager.snapshot().conversations[0]?.data.inbound).toBe('0');
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
    expect(f.manager.snapshot().conversations.find((item) => item.data.session.sessionId === f.b.session.sessionId)?.data.session.status).toBe('ACTIVE');
    const draft = f.manager.draft(f.b.session.sessionId, 'last draft'); const compose = f.manager.draft(null, 'new chat draft');
    await f.manager.dispose(); await Promise.all([draft, compose]);
    expect((await f.a.store.read(f.b.session.sessionId))?.value.draft).toBe('last draft');
    expect((await f.a.store.readWorkspace()).value.draft).toBe('new chat draft');
    expect(f.api.close).toHaveBeenCalledTimes(1);
  });
});
