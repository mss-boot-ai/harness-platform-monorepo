import { decodeWireMessage as fromBinary } from '@harness/hc-core';
import { WirePacketSchema } from '@harness/hc-core';
import { ConversationController, ConversationTransportInterrupted } from './conversation-controller';
import { controllerFixture, testNow } from './conversation-fixture';
const chunk = (sessionId: string, text: string) => ({ jsonrpc: '2.0', method: 'session/update', params: { sessionId, update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } } } });
describe('durable conversation controller', () => {
  it('persists uncertain configuration and a stopped runtime across refresh instead of claiming rollback', async () => {
    const f = await controllerFixture(); const option = f.value.runtime.config[0];
    if (option === undefined) throw new Error('Missing config');
    await f.controller.configure(option, 'b');
    const id = f.controller.snapshot().data.requests[0]?.id;
    await f.controller.receive(await f.incoming({ jsonrpc: '2.0', id, error: { code: -32001, message: 'CONFIGURATION_UNCONFIRMED', data: { executionState: 'unknown', runtimeStopped: true } } }, 1n));
    await f.controller.observe(f.session);
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('Missing snapshot');
    expect(saved.value.runtime.status).toBe('failed'); expect(saved.value.recovery?.kind).toBe('execution-unknown');
    expect(saved.value.runtime.configError).not.toContain('保持不变');
    const restored = new ConversationController(saved, f.store, f.identity, f.transport, () => undefined);
    await expect(restored.prompt('must not retry on a stopped runtime')).rejects.toThrow();
  });
  it('treats an authenticated terminal provider failure as a failed turn and permits a new turn', async () => {
    const f = await controllerFixture(); await f.controller.prompt('first turn');
    const id = f.controller.snapshot().data.awaiting;
    await f.controller.receive(await f.incoming({ jsonrpc: '2.0', id, error: { code: -32001, message: 'PROVIDER_ERROR', data: { status: 504 } } }, 1n));
    expect(f.controller.snapshot().data.blocked).toBeNull();
    expect(f.controller.snapshot().data.recovery?.kind).toBe('turn-failed');
    expect(f.controller.snapshot().data.messages[1]?.state).toBe('failed');
    expect(f.controller.snapshot().data.messages[1]?.error).toContain('504');
    await f.controller.prompt('continue explicitly');
    expect(f.controller.snapshot().data.outbound).toBe('2');
    expect(f.controller.snapshot().data.recovery).toBeNull();
  });
  it('keeps unknown execution and malformed error responses quarantined across reconstruction', async () => {
    const f = await controllerFixture(); await f.controller.prompt('unknown execution');
    const id = f.controller.snapshot().data.awaiting;
    await f.controller.receive(await f.incoming({ jsonrpc: '2.0', id, error: { code: -32002, message: 'RUNTIME_LOST', data: { executionState: 'unknown', runtimeStopped: true } } }, 1n));
    expect((await f.store.read(f.session.sessionId))?.value.recovery?.kind).toBe('execution-unknown');
    await expect(f.controller.prompt('unsafe retry')).rejects.toThrow();
    const malformed = await controllerFixture(); await malformed.controller.prompt('invalid response');
    await expect(malformed.controller.receive(await malformed.incoming({ jsonrpc: '2.0', id: malformed.controller.snapshot().data.awaiting, error: null }, 1n))).rejects.toThrow('Invalid ACP error');
    expect((await malformed.store.read(malformed.session.sessionId))?.value.recovery?.kind).toBe('integrity');
  });
  it('does not quarantine a verified frame when its gap request is interrupted by connection replacement', async () => {
    const f = await controllerFixture(); await f.controller.prompt('pending turn');
    let rejectControl: (cause: Error) => void = () => undefined;
    const control = vi.spyOn(f.transport, 'control').mockImplementationOnce(() => new Promise<void>((_resolve, reject) => { rejectControl = reject; }));
    const receiving = f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'gap'), 2n));
    await vi.waitFor(() => expect(control).toHaveBeenCalled());
    rejectControl(new ConversationTransportInterrupted('replacement')); await receiving;
    expect(f.controller.snapshot().fault).toBeNull(); expect((await f.store.read(f.session.sessionId))?.value.blocked).toBeNull();
    await f.controller.resume(); expect(f.controller.snapshot().fault).toBeNull();
    expect(f.sent[f.sent.length - 1]).toEqual(f.sent[0]);
  });
  it('persists an authenticated conflict so reconciliation and reconstruction cannot re-enable the channel', async () => {
    const f = await controllerFixture(); await f.controller.prompt('one turn');
    const id = f.controller.snapshot().data.awaiting;
    await f.controller.receive(await f.incoming([chunk(f.session.sessionId, 'accepted'), { jsonrpc: '2.0', id, result: { stopReason: 'end_turn' } }], 1n));
    await expect(f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'conflicting'), 1n))).rejects.toThrow('Conflicting');
    await f.controller.observe(f.session);
    const saved = await f.store.read(f.session.sessionId); if (saved === null) throw new Error('Missing snapshot');
    expect(saved.value.blocked).toContain('完整性');
    const before = f.sent.length; const controls = f.controls.length;
    const restored = new ConversationController(saved, f.store, f.identity, f.transport, () => undefined);
    await restored.resume(); await expect(restored.prompt('must not execute')).rejects.toThrow('not writable');
    expect(f.sent).toHaveLength(before); expect(f.controls).toHaveLength(controls);
  });
  it('does not overwrite a newer snapshot when persisting a quarantine hits a CAS conflict', async () => {
    const f = await controllerFixture(); await f.controller.prompt('one turn');
    await f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'accepted'), 1n));
    const current = await f.store.read(f.session.sessionId); if (current === null) throw new Error('Missing snapshot');
    await f.store.write({ ...current.value, draft: 'newer owner record' }, current.revision);
    await expect(f.controller.receive(await f.incoming(chunk(f.session.sessionId, 'different'), 1n))).rejects.toThrow('Conflicting');
    expect(f.controller.snapshot().fault).toContain('安全状态未能保存');
    expect((await f.store.read(f.session.sessionId))?.value.draft).toBe('newer owner record');
    await expect(f.controller.prompt('again')).rejects.toThrow();
  });
  it('validates the entire saved outbox before replaying its first operation', async () => {
    const f = await controllerFixture(); await f.controller.prompt('pending');
    const saved = await f.store.read(f.session.sessionId); const first = saved?.value.outbox[0];
    if (saved === null || first === undefined) throw new Error('Missing outbox');
    const value = { ...saved.value, outbound: '2', outbox: [first, { ...first, sequence: '2', messageId: '09'.repeat(16) }] };
    const revision = await f.store.write(value, saved.revision);
    const restored = new ConversationController({ revision, value }, f.store, f.identity, f.transport, () => undefined);
    const before = f.sent.length; await expect(restored.resume()).rejects.toThrow('binding');
    expect(f.sent).toHaveLength(before); expect((await f.store.read(f.session.sessionId))?.value.blocked).toContain('恢复记录');
  });
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
