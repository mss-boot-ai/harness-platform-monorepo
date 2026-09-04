import { classifyHarnessPageState, isEmptyOverview } from './state';

describe('Harness page states', () => {
  it('classifies loading, empty, ready, forbidden, and error states', () => {
    expect(classifyHarnessPageState({ loading: true })).toBe('loading');
    expect(classifyHarnessPageState({ empty: true })).toBe('empty');
    expect(classifyHarnessPageState({ empty: false })).toBe('ready');
    expect(classifyHarnessPageState({ error: { response: { status: 403 } } })).toBe('forbidden');
    expect(classifyHarnessPageState({ error: { response: { status: 500 } } })).toBe('error');
  });

  it('treats an all-zero overview as empty', () => {
    expect(
      isEmptyOverview({
        activeEndpoints: 0,
        activeSessions: 0,
        conflictFrames: 0,
        pendingEnrollments: 0,
        unacknowledgedFrames: 0,
      }),
    ).toBe(true);
    expect(
      isEmptyOverview({
        activeEndpoints: 1,
        activeSessions: 0,
        conflictFrames: 0,
        pendingEnrollments: 0,
        unacknowledgedFrames: 0,
      }),
    ).toBe(false);
  });
});
