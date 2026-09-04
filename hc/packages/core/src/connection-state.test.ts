import { transitionConnection, type ConnectionState } from './connection-state';

describe('HC connection state machine', () => {
  it('reaches READY only through login, endpoint, ticket, socket, and challenge', () => {
    const events = [
      'LOGIN_SUCCEEDED',
      'ENDPOINT_UNLOCKED',
      'START_TICKET',
      'TICKET_ISSUED',
      'SOCKET_CHALLENGED',
      'CONNECTION_READY',
    ] as const;
    let state: ConnectionState = 'SIGNED_OUT';
    for (const event of events) {
      state = transitionConnection(state, event);
    }
    expect(state).toBe('READY');
  });

  it('fails closed on skipped challenge and treats revocation as terminal', () => {
    expect(() => transitionConnection('CONNECTING', 'CONNECTION_READY')).toThrow(
      'invalid HC connection transition',
    );
    expect(transitionConnection('READY', 'REVOKE')).toBe('REVOKED');
    expect(() => transitionConnection('REVOKED', 'START_TICKET')).toThrow(
      'invalid HC connection transition',
    );
  });

  it('requires a fresh ticket after a lost connection', () => {
    expect(transitionConnection('READY', 'CONNECTION_LOST')).toBe('RECONNECTING');
    expect(transitionConnection('RECONNECTING', 'START_TICKET')).toBe('TICKET_ISSUING');
  });
});
