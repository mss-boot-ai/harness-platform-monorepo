import { parseHarnessDelivery, parseHarnessEndpointList, parseHarnessOverview } from './contract';

describe('Harness response contracts', () => {
  it('parses the safe overview projection', () => {
    expect(
      parseHarnessOverview({
        activeEndpoints: 2,
        activeSessions: 1,
        conflictFrames: 0,
        pendingEnrollments: 3,
        unacknowledgedFrames: 4,
      }),
    ).toEqual({
      activeEndpoints: 2,
      activeSessions: 1,
      conflictFrames: 0,
      pendingEnrollments: 3,
      unacknowledgedFrames: 4,
    });
  });

  it('rejects malformed or unbounded list responses', () => {
    expect(() => parseHarnessEndpointList({ items: 'not-a-list' })).toThrow();
    expect(() =>
      parseHarnessEndpointList({ items: Array.from({ length: 201 }, () => ({})) }),
    ).toThrow('Harness endpoints is invalid');
  });

  it('rejects delivery payloads that expose an invalid shape', () => {
    expect(() => parseHarnessDelivery({ acks: [], frames: [], session: null })).toThrow(
      'Harness session is invalid',
    );
  });
});
