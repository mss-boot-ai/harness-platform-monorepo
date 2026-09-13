import { conversationTitle, MAX_REPLY_CHARACTERS, mergeSessionObservation, receiveAcp, safeLink, settleTurn, shouldSendOnEnter, startTurn } from './model';
const chunk = (text: string) => ({ method: 'session/update', params: { update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text } } } });
describe('chat presentation model', () => {
  it('collects chunks into one assistant message and completes only the matching request', () => {
    let result = receiveAcp(startTurn([], 'turn-a', '你好'), chunk('你'), 'turn-a');
    result = receiveAcp(result.messages, chunk('好'), 'turn-a');
    expect(result.messages).toHaveLength(2);
    expect(result.messages[1]?.text).toBe('你好');
    expect(receiveAcp(result.messages, { id: 'other', result: {} }, 'turn-a').completed).toBe(false);
    const finished = receiveAcp(result.messages, { id: 'turn-a', result: { stopReason: 'end_turn' } }, 'turn-a');
    expect(finished.completed).toBe(true);
    expect(finished.messages[1]?.state).toBe('complete');
    expect(finished.messages.some((message) => message.text.includes('turn completed'))).toBe(false);
  });
  it('does not render reasoning, tool payloads or protocol JSON as ordinary reply text', () => {
    const initial = startTurn([], 'a', 'hello');
    for (const sessionUpdate of ['agent_thought_chunk', 'tool_call', 'permission_request']) {
      const result = receiveAcp(initial, { method: 'session/update', params: { update: { sessionUpdate, content: { type: 'text', text: 'not a normal reply' } } } }, 'a');
      expect(result.messages).toEqual(initial);
    }
    expect(receiveAcp(initial, chunk('unexpected'), null).messages).toEqual(initial);
  });
  it('handles batches and ignores updates after the request terminal response', () => {
    const result = receiveAcp(startTurn([], 'a', 'hello'), [chunk('one'), chunk('two'), { id: 'a', result: {} }, chunk('late')], 'a');
    expect(result.messages[1]?.text).toBe('onetwo'); expect(result.completed).toBe(true);
  });
  it('represents errors and interrupted output without declaring rollback or a successful turn', () => {
    const initial = startTurn([], 'a', 'deploy');
    const result = receiveAcp(initial, { id: 'a', error: { code: -1 } }, 'a');
    expect(result.failed).toBe(true); expect(result.messages[1]?.state).toBe('uncertain');
    expect(settleTurn(initial, null, 'uncertain')).toEqual(initial);
  });
  it('bounds response display and keeps Unicode titles intact', () => {
    const result = receiveAcp(startTurn([], 'a', 'hello'), chunk('x'.repeat(MAX_REPLY_CHARACTERS + 10)), 'a');
    expect(result.messages[1]?.text.length).toBe(MAX_REPLY_CHARACTERS);
    expect(conversationTitle('  一段\n  对话  ')).toBe('一段 对话');
    expect(conversationTitle('😀'.repeat(40))).toBe('😀'.repeat(32) + '…');
  });
  it('never sends when Enter confirms IME text, inserts a newline, or is held down', () => {
    const base = { key: 'Enter', shiftKey: false, altKey: false, isComposing: false, keyCode: 13, repeat: false };
    expect(shouldSendOnEnter(base)).toBe(true);
    for (const patch of [{ shiftKey: true }, { altKey: true }, { isComposing: true }, { keyCode: 229 }, { repeat: true }, { key: 'a' }]) expect(shouldSendOnEnter({ ...base, ...patch })).toBe(false);
  });
  it('allows explicit web links but blocks script, data, file and relative targets', () => {
    expect(safeLink('https://example.com/docs')).toBe('https://example.com/docs');
    for (const value of ['javascript:alert(1)', 'data:text/html,test', 'file:///etc/passwd', '//evil.test', '/admin', 'vbscript:test']) expect(safeLink(value)).toBeNull();
  });
});

it('keeps late polls from rolling active, closing or uncertain sessions backwards', () => {
  const snapshot = (status: string) => ({ sessionId: 'same', status });
  for (const [current, observed, expected] of [
    ['WAITING_KEY', 'CREATING', 'WAITING_KEY'],
    ['ACTIVE', 'WAITING_KEY', 'ACTIVE'],
    ['ACTIVE', 'CREATING', 'ACTIVE'],
    ['ACTIVE', 'DRAINING', 'DRAINING'],
    ['DRAINING', 'ACTIVE', 'DRAINING'],
    ['DRAINING', 'CLOSED', 'CLOSED'],
    ['UNCERTAIN', 'ACTIVE', 'UNCERTAIN'],
    ['UNCERTAIN', 'DRAINING', 'DRAINING'],
    ['CLOSED', 'ACTIVE', 'CLOSED'],
  ] as const) expect(mergeSessionObservation(snapshot(current), snapshot(observed))?.status).toBe(expected);
  expect(mergeSessionObservation(snapshot('CLOSED'), null)).toBeNull();
  expect(mergeSessionObservation(snapshot('CLOSED'), { sessionId: 'new', status: 'CREATING' })?.status).toBe('CREATING');
});
