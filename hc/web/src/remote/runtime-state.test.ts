import { closeTurnPermissions, configRequest, confirmConfiguration, markPermissionSubmitted, newRuntimeState,
  parseConfigOptions, permissionDecision, readDescriptor, receiveRuntime } from './runtime-state';

const options = [{ id: 'model', name: 'Actual model', category: 'model', type: 'select', currentValue: 'small', options: [{ value: 'small', name: 'Small' }, { value: 'large', name: 'Large' }] }];
const descriptor = { initialize: { protocolVersion: 1, agentInfo: { name: 'Fixture runtime' } }, session: { sessionId: 'session', configOptions: options }, bridge: { duplex: true, turnCancellation: true, protocolVersion: 1, processEpoch: '1' } };
const update = (body: Record<string, unknown>, sessionId = 'session') => ({ jsonrpc: '2.0', method: 'session/update', params: { sessionId, update: body } });
const permission = { jsonrpc: '2.0', id: 'original', method: 'session/request_permission', params: { sessionId: 'session', toolCall: { title: 'Read fixture', toolCallId: 'tool', rawInput: { path: 'fixture.txt' } }, options: [{ optionId: 'allow', name: 'Allow', kind: 'allow_once' }, { optionId: 'deny', name: 'Deny', kind: 'reject_once' }] } };

describe('runtime-backed session controls', () => {
  it('projects a late tool update onto its original turn, including after cancellation and while B is active', () => {
    let state = receiveRuntime(newRuntimeState(), update({ sessionUpdate: 'tool_call', toolCallId: 'tool-a', status: 'in_progress' }), 'session', 'turn-a');
    state = closeTurnPermissions(state, 'turn-a');
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call', toolCallId: 'tool-b', status: 'in_progress' }), 'session', 'turn-b');
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call_update', toolCallId: 'tool-a', status: 'completed' }), 'session', 'turn-b');
    expect(state.tools.find((item) => item.id === 'tool-a')).toMatchObject({ turnId: 'turn-a', status: 'completed' });
    expect(state.tools.find((item) => item.id === 'tool-b')).toMatchObject({ turnId: 'turn-b', status: 'in_progress' });
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call_update', toolCallId: 'tool-b', status: 'completed' }), 'session', null);
    expect(state.tools.find((item) => item.id === 'tool-b')?.status).toBe('completed');
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call_update', toolCallId: 'unowned', status: 'completed' }), 'session', 'turn-b');
    expect(state.tools).toHaveLength(2); expect(state.diagnostics.length).toBeGreaterThan(0);
  });
  it('discovers actual options, ignores unsupported types, and never invents a model list', () => {
    const state = readDescriptor(newRuntimeState(), descriptor, 'session');
    expect(state.status).toBe('ready'); expect(state.cancelSupported).toBe(true);
    expect(state.config.map((value) => value.id)).toEqual(['model']);
    expect(readDescriptor(newRuntimeState(), descriptor, 'other').status).toBe('unsupported');
    expect(parseConfigOptions([{ id: 'future', type: 'future-type' }])).toEqual([]);
    expect(readDescriptor(newRuntimeState(), { ...descriptor, bridge: { ...descriptor.bridge, turnCancellation: false } }, 'session').cancelSupported).toBe(false);
  });
  it('validates nested choices and rejects duplicates or unknown effective values', () => {
    expect(parseConfigOptions([{ ...options[0], options: [{ name: 'Group', options: options[0]?.options }] }])[0]?.options[0]?.group).toBe('Group');
    expect(() => parseConfigOptions([options[0], options[0]])).toThrow();
    expect(() => parseConfigOptions([{ ...options[0], currentValue: 'fabricated' }])).toThrow();
    expect(() => parseConfigOptions([{ ...options[0], options: [{ value: 'small' }, { value: 'small' }] }])).toThrow();
  });
  it('keeps effective values unchanged until an authoritative result or update arrives', () => {
    const state = readDescriptor(newRuntimeState(), descriptor, 'session');
    const option = state.config[0]; if (option === undefined) throw new Error('Missing fixture option');
    expect(configRequest(option, 'large', 'session', 'change')).toMatchObject({ method: 'session/set_config_option', params: { sessionId: 'session', configId: 'model', value: 'large' } });
    expect(state.config[0]?.value).toBe('small');
    const confirmed = confirmConfiguration(state, { configOptions: [{ ...options[0], currentValue: 'large' }] });
    expect(confirmed.config[0]?.value).toBe('large');
    expect(confirmed.configRevision).toBe(state.configRevision + 1);
    expect(confirmConfiguration(state, { configOptions: 'bad' }).config[0]?.value).toBe('small');
    expect(() => configRequest(option, 'unknown', 'session', 'change')).toThrow();
  });
  it('handles legacy model/mode descriptors without pretending a result contains configOptions', () => {
    const state = readDescriptor(newRuntimeState(), { ...descriptor, session: { sessionId: 'session', models: { currentModelId: 'one', availableModels: [{ modelId: 'one', name: 'One' }] }, modes: { currentModeId: 'read', availableModes: [{ id: 'read', name: 'Read only' }] } } }, 'session');
    expect(state.config.map((value) => value.method)).toEqual(['session/set_model', 'session/set_mode']);
    expect(state.config[0]?.value).toBe('one');
  });
  it('binds permission decisions to their original request and consumes the UI choice once', () => {
    const state = receiveRuntime(newRuntimeState(), permission, 'session', 'turn', 100);
    expect(state.permissions).toHaveLength(1);
    expect(permissionDecision(state, 'original', 'allow')).toEqual({ jsonrpc: '2.0', id: 'original', result: { outcome: { outcome: 'selected', optionId: 'allow' } } });
    expect(() => permissionDecision(state, 'original', 'fabricated')).toThrow();
    expect(() => permissionDecision(markPermissionSubmitted(state, 'original'), 'original', 'allow')).toThrow();
    expect(() => permissionDecision(closeTurnPermissions(state, 'turn'), 'original', 'allow')).toThrow();
    expect(receiveRuntime(state, permission, 'session', 'turn').permissions).toHaveLength(1);
  });
  it('never approves a truncated action and isolates another session or an absent turn', () => {
    const oversized = { ...permission, params: { ...permission.params, toolCall: { title: 'Too long', rawInput: 'x'.repeat(60_000) } } };
    const state = receiveRuntime(newRuntimeState(), oversized, 'session', 'turn');
    expect(state.permissions[0]?.complete).toBe(false);
    expect(() => permissionDecision(state, 'original', 'allow')).toThrow();
    expect(permissionDecision(state, 'original', 'deny')).toMatchObject({ result: { outcome: { optionId: 'deny' } } });
    expect(receiveRuntime(newRuntimeState(), permission, 'other', 'turn').permissions).toHaveLength(0);
    expect(receiveRuntime(newRuntimeState(), permission, 'session', null).permissions).toHaveLength(0);
  });
  it('accepts only real finite token usage and records incremental tools separately from answer text', () => {
    let state = receiveRuntime(newRuntimeState(), update({ sessionUpdate: 'usage_update', used: 512, size: 4096 }), 'session', 'turn');
    expect(state.usage).toEqual({ used: 512, size: 4096 });
    state = receiveRuntime(state, update({ sessionUpdate: 'usage_update', used: -1, size: 0 }), 'session', 'turn');
    expect(state.usage).toEqual({ used: 512, size: 4096 });
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call', toolCallId: 'tool', title: 'Read', status: 'in_progress', rawInput: { path: 'file' } }), 'session', 'turn');
    state = receiveRuntime(state, update({ sessionUpdate: 'tool_call_update', toolCallId: 'tool', status: 'completed', content: [{ type: 'content', content: { type: 'text', text: 'done' } }] }), 'session', 'turn');
    expect(state.tools).toHaveLength(1); expect(state.tools[0]).toMatchObject({ title: 'Read', status: 'completed', content: 'done', turnId: 'turn' });
    expect(receiveRuntime(state, update({ sessionUpdate: 'tool_call', toolCallId: 'alien' }, 'other'), 'session', 'turn')).toBe(state);
  });
  it('removes a pending approval on an execution-side closure, even before final answer', () => {
    const state = receiveRuntime(newRuntimeState(), permission, 'session', 'turn');
    const result = receiveRuntime(state, { method: '_mss/permission/closed', params: { sessionId: 'session', requestId: 'original' } }, 'session', 'turn');
    expect(result.permissions[0]?.status).toBe('closed');
  });
});
