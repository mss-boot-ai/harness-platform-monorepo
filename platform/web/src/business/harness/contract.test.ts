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

  it('maps the numeric AWP direction enum and rejects unknown values', () => {
    const session = {
      abaEndpointId: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
      createdAt: '2026-09-04T10:00:00Z',
      hcEndpointId: 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
      id: 'cccccccccccccccccccccccccccccccc',
      keyGeneration: 1,
      requestedCapabilities: ['prompt'],
      runtimeProfileId: 'local-acp',
      status: 'ACTIVE',
      workspaceId: 'h5-debug',
    };
    const frame = {
      channelId: 'dddddddddddddddddddddddddddddddd',
      ciphertextBytes: 8,
      direction: 1,
      keyGeneration: 1,
      messageId: 'eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee',
      receivedAt: '2026-09-04T10:00:01Z',
      receiverEndpointId: session.abaEndpointId,
      senderEndpointId: session.hcEndpointId,
      sequence: 1,
      status: 'STORED',
    };

    expect(parseHarnessDelivery({ acks: [], frames: [frame], session }).frames[0]?.direction).toBe(
      'HC_TO_ABA',
    );
    expect(() =>
      parseHarnessDelivery({ acks: [], frames: [{ ...frame, direction: 3 }], session }),
    ).toThrow('direction is invalid');
  });
});
