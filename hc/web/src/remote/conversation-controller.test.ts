import { decodeWireMessage as fromBinary } from '@harness/hc-core';
import { WirePacketSchema } from '@harness/hc-core';
import { ConversationController } from './conversation-controller';
import { controllerFixture, testNow } from './conversation-fixture';
const chunk = (sessionId: string, text: string) => ({ jsonrpc: '2.0', method: 'session/update', params: { sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } } } });
describe('durable conversation controller', () => {
  it('never replays outbound operations for an uncertain session', async () => {
    const f = await controllerFixture(); await f.controller.prompt('do not repeat');
    await f.controller.observe({ ...f.session, status: 'UNCERTAIN' });
    const before = f.sent.length; await f.controller.resume();
    expect(f.sent).toHaveLength(before); expect(f.controls).toHaveLength(1);
    await expect(f.controller.prompt('again')).rejects.toThrow();
    expect(f.sent).toHaveLength(before);
  });
  it('preserves basic prompting without fabricating remote capabilities for legacy sessions', async () => {
    const f = await controllerFixture(); await f.controller.observe({ ...f.session, requestedCapabilities: ['prompt'] });
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('Missing snapshot');
    const legacy = new ConversationController({ ...saved, value: { ...saved.value, runtime: { ...saved.value.runtime, status: 'pending' } } }, f.store, f.identity, f.transport, () => undefined);
    await legacy.describe(); expect(f.sent).toHaveLength(0);
    await legacy.prompt('basic prompt'); expect(f.sent).toHaveLength(1);
  });
  it('saves exact packets before sending and replays identical bytes after refresh', async () => {
    const f = await controllerFixture();
    await f.controller.prompt('do work'); await f.controller.idle();
    expect(f.sent).toHaveLength(1);
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('No saved conversation');
    expect(saved.value.awaiting).not.toBeNull(); expect(saved.value.outbound).toBe('1'); expect(saved.value.reservation).toBeNull();
    expect(saved.value.outbox).toHaveLength(1);
    const restored = new ConversationController(saved, f.store, f.identity, f.transport, () => undefined);
    await restored.resume(); await restored.idle();
    expect(f.sent[f.sent.length - 1]).toEqual(f.sent[0]);
    expect(restored.snapshot().data.messages.filter((item) => item.role === 'user')).toHaveLength(1);
    await expect(restored.prompt('repeat')).rejects.toThrow('another turn');
  });
  it('holds an interrupted reservation and never sends when the second durable commit fails', async () => {
    const f = await controllerFixture();
    const write = f.store.write.bind(f.store); let count = 0;
    vi.spyOn(f.store, 'write').mockImplementation(async (...args) => { count += 1; if (count === 2) throw new Error('disk full'); return write(...args); });
    await expect(f.controller.prompt('side effect')).rejects.toThrow('disk full');
    expect(f.sent).toHaveLength(0);
    const saved = await f.store.read(f.session.sessionId);
    expect(saved?.value.reservation).toBe('1'); expect(saved?.value.blocked).toContain('避免重用');
    await expect(f.controller.prompt('again')).rejects.toThrow('not writable');
  });
  it('commits message effects before ACK and does not duplicate replayed input across refresh', async () => {
    const f = await controllerFixture(); await f.controller.prompt('hello');
    const turn = f.controller.snapshot().data.awaiting;
    const first = await f.incoming(chunk(f.session.sessionId, 'hello result'), 1n);
    const last = await f.incoming({ jsonrpc: '2.0', id: turn, result: { stopReason: 'end_turn' } }, 2n);
    await f.controller.receive(first); await f.controller.receive(last); await f.controller.receive(last);
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('Missing snapshot');
    expect(saved.value.inbound).toBe('2'); expect(saved.value.awaiting).toBeNull();
    expect(saved.value.messages[1]?.text).toBe('hello result'); expect(saved.value.messages[1]?.state).toBe('complete');
    const restored = new ConversationController(saved, f.store, f.identity, f.transport, () => undefined);
    await restored.receive(first); await restored.receive(last);
    expect(restored.snapshot().data.messages).toEqual(saved.value.messages);
    const acknowledgments = f.sent.map((bytes) => fromBinary(WirePacketSchema, bytes)).filter((packet) => packet.body.case === 'ack');
    expect(acknowledgments).toHaveLength(5);
  });
  it('clears the outbox only on a valid ABA ACK, never declares a completed turn from it', async () => {
    const f = await controllerFixture(); await f.controller.prompt('hello');
    await f.controller.receive(await f.ack(1n));
    expect(f.controller.snapshot().data.outbox).toHaveLength(0); expect(f.controller.snapshot().data.awaiting).not.toBeNull();
    const bad = await f.ack(99n);
    await expect(f.controller.receive(bad)).rejects.toThrow('exceeds');
    expect(f.controller.snapshot().data.outboundAck).toBe('1');
  });
  it('uses a fresh missing-range request for gaps and blocks conflicting signed duplicates', async () => {
    const f = await controllerFixture(); await f.controller.prompt('hello');
    await f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'second'), 2n));
    expect(f.controller.snapshot().data.inbound).toBe('0'); expect(f.controls).toHaveLength(1);
    await f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'first'), 1n));
    await expect(f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'different'), 1n))).rejects.toThrow('Conflicting');
    expect(f.controller.snapshot().data.messages[1]?.text).toBe('first');
  });
  it('keeps configuration changes pending until their matching source response', async () => {
    const f = await controllerFixture(); const option = f.value.runtime.config[0]; if (option === undefined) throw new Error('Missing config');
    await f.controller.configure(option, 'b');
    const pending = f.controller.snapshot().data.requests[0];
    expect(f.controller.snapshot().data.runtime.config[0]?.value).toBe('a');
    await expect(f.controller.prompt('too early')).rejects.toThrow('another turn');
    await f.controller.receive(await f.incoming({ jsonrpc: '2.0', id: pending?.id, result: { configOptions: [{ id: 'model', name: 'Model', category: 'model', type: 'select', currentValue: 'b', options: [{ value: 'a' }, { value: 'b' }] }] } }, 1n));
    expect(f.controller.snapshot().data.runtime.config[0]?.value).toBe('b'); expect(f.controller.snapshot().data.requests).toHaveLength(0);
  });
  it('binds permissions and cancel to the active turn, then continues the same session', async () => {
    const f = await controllerFixture(); await f.controller.prompt('action');
    const id = f.controller.snapshot().data.awaiting;
    const permission = { jsonrpc: '2.0', id: 'permission-1', method: 'session/request_permission', params: { sessionId: f.session.sessionId,
      toolCall: { toolCallId: 't1', title: 'write a test file', rawInput: { file: 'fixture.txt' } }, options: [{ optionId: 'deny', name: 'Deny', kind: 'reject_once' }] } };
    await f.controller.receive(await f.incoming(permission, 1n)); await f.controller.decide('permission-1', 'deny');
    await expect(f.controller.decide('permission-1', 'deny')).rejects.toThrow('actionable');
    await f.controller.cancel(); expect(f.controller.snapshot().data.cancelPending).toBe(true);
    await f.controller.receive(await f.incoming({ jsonrpc: '2.0', id, result: { stopReason: 'cancelled' } }, 2n));
    expect(f.controller.snapshot().data.session.status).toBe('ACTIVE'); expect(f.controller.snapshot().data.cancelPending).toBe(false);
    expect(f.controller.snapshot().data.runtime.permissions[0]?.status).toBe('closed');
    await f.controller.prompt('next turn'); expect(f.controller.snapshot().data.messages).toHaveLength(4);
  });
  it('does not generate new ciphertext for stale unconfirmed work and detects wrong-session events', async () => {
    const f = await controllerFixture(); await f.controller.prompt('old');
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('Missing state');
    const restored = new ConversationController(saved, f.store, f.identity, { ...f.transport, now: () => testNow + 300_001 }, () => undefined);
    const sent = f.sent.length; await restored.resume(); expect(f.sent.length).toBe(sent);
    expect(restored.snapshot().data.blocked).toContain('补传窗口');
    const g = await controllerFixture(); await g.controller.prompt('hello');
    await expect(g.controller.receive(await g.incoming(chunk('09'.repeat(16), 'wrong'), 1n))).rejects.toThrow('session binding');
    expect(g.controller.snapshot().data.inbound).toBe('0');
  });
});
