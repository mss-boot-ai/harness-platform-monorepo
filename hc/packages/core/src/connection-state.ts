export type ConnectionState =
  | 'SIGNED_OUT'
  | 'HUMAN_AUTHENTICATED'
  | 'ENDPOINT_READY'
  | 'TICKET_ISSUING'
  | 'CONNECTING'
  | 'CHALLENGED'
  | 'READY'
  | 'RECONNECTING'
  | 'REVOKED'
  | 'CLOSED';

export type ConnectionEvent =
  | 'LOGIN_SUCCEEDED'
  | 'ENDPOINT_UNLOCKED'
  | 'START_TICKET'
  | 'TICKET_ISSUED'
  | 'SOCKET_CHALLENGED'
  | 'CONNECTION_READY'
  | 'CONNECTION_LOST'
  | 'REVOKE'
  | 'CLOSE'
  | 'SIGN_OUT';

const transitions: Partial<Record<ConnectionState, Partial<Record<ConnectionEvent, ConnectionState>>>> = {
  SIGNED_OUT: { LOGIN_SUCCEEDED: 'HUMAN_AUTHENTICATED' },
  HUMAN_AUTHENTICATED: { ENDPOINT_UNLOCKED: 'ENDPOINT_READY', SIGN_OUT: 'SIGNED_OUT' },
  ENDPOINT_READY: { SIGN_OUT: 'SIGNED_OUT', START_TICKET: 'TICKET_ISSUING' },
  TICKET_ISSUING: { SIGN_OUT: 'SIGNED_OUT', TICKET_ISSUED: 'CONNECTING' },
  CONNECTING: { SIGN_OUT: 'SIGNED_OUT', SOCKET_CHALLENGED: 'CHALLENGED' },
  CHALLENGED: { CONNECTION_READY: 'READY', SIGN_OUT: 'SIGNED_OUT' },
  READY: { CONNECTION_LOST: 'RECONNECTING', SIGN_OUT: 'SIGNED_OUT' },
  RECONNECTING: { SIGN_OUT: 'SIGNED_OUT', START_TICKET: 'TICKET_ISSUING' },
  REVOKED: { SIGN_OUT: 'SIGNED_OUT' },
  CLOSED: { SIGN_OUT: 'SIGNED_OUT' },
};

export function transitionConnection(
  state: ConnectionState,
  event: ConnectionEvent,
): ConnectionState {
  if (event === 'REVOKE' && state !== 'SIGNED_OUT') {
    return 'REVOKED';
  }
  if (event === 'CLOSE' && state !== 'SIGNED_OUT') {
    return 'CLOSED';
  }

  const next = transitions[state]?.[event];
  if (next === undefined) {
    throw new Error(`invalid HC connection transition: ${state} + ${event}`);
  }
  return next;
}
