import { createDpopProof, createRegistrationProof, type EndpointIdentity } from '@harness/hc-core';

const adminBase = '/admin/api';

export interface RegistrationSession {
  readonly accessExpiresAt: string;
  readonly accessToken: string;
  readonly endpointId: string;
  readonly kemJkt: string;
  readonly signingJkt: string;
  readonly tokenType: 'DPoP';
}

interface RegistrationChallenge {
  readonly challenge: string;
  readonly challengeId: string;
  readonly expiresAt: string;
  readonly transcriptVersion: 'MSS-HC-REGISTER-V1';
}

export interface WebSocketTicket {
  readonly expiresAt: string;
  readonly protocol: 'mss.awp.v1';
  readonly ticket: string;
  readonly websocketUrl: string;
}

export class HcApiError extends Error {
  public constructor(
    message: string,
    public readonly code: string,
    public readonly status: number,
  ) {
    super(message);
  }
}

export async function loginBrowserSession(username: string, password: string): Promise<string> {
  const response = await requestJson<unknown>(`${adminBase}/user/session/login`, {
    body: JSON.stringify({ password, username }),
    method: 'POST',
  });
  const value = objectValue(response, 'browser session response');
  for (const field of ['token', 'accessToken', 'refreshToken']) {
    if (Object.hasOwn(value, field)) {
      throw new HcApiError('Browser session response exposed a credential', 'HC_UNSAFE_RESPONSE', 500);
    }
  }
  if (typeof value.expire !== 'string' || !Number.isFinite(Date.parse(value.expire))) {
    throw new HcApiError('Browser session response is invalid', 'HC_INVALID_RESPONSE', 500);
  }
  return value.expire;
}

export async function registerBrowserEndpoint(
  identity: EndpointIdentity,
  endpointName: string,
): Promise<RegistrationSession> {
  const challengeValue = await requestJson<unknown>(`${adminBase}/harness/v1/hc/challenges`, {
    body: '{}',
    headers: csrfHeaders(),
    method: 'POST',
  });
  const challenge = parseChallenge(challengeValue);
  const origin = window.location.origin;
  const softwareVersion = '0.1.0';
  const proof = await createRegistrationProof(identity, {
    challenge: challenge.challenge,
    challengeId: challenge.challengeId,
    endpointName,
    origin,
    softwareVersion,
  });
  const response = await requestJson<unknown>(`${adminBase}/harness/v1/hc/endpoints`, {
    body: JSON.stringify({
      assurance: identity.assurance,
      challenge: challenge.challenge,
      challengeId: challenge.challengeId,
      endpointName,
      kemPublicJwk: identity.kem.publicJwk,
      proof,
      signingPublicJwk: identity.signing.publicJwk,
      softwareVersion,
    }),
    headers: csrfHeaders(),
    method: 'POST',
  });
  return parseRegistration(response);
}

export async function issueWebSocketTicket(
  identity: EndpointIdentity,
  session: RegistrationSession,
): Promise<WebSocketTicket> {
  const path = '/gateway/v1/ws/tickets';
  const challengeResponse = await gatewayRequest(path, session.accessToken);
  const nonce = challengeResponse.headers.get('DPoP-Nonce');
  if (challengeResponse.status !== 401 || nonce === null || nonce === '') {
    throw await gatewayFailure(challengeResponse);
  }
  const proof = await createDpopProof({
    accessToken: session.accessToken,
    htm: 'POST',
    htu: new URL(path, window.location.origin).toString(),
    nonce,
    privateKey: identity.signing.privateKey,
    publicJwk: identity.signing.publicJwk,
  });
  const response = await gatewayRequest(path, session.accessToken, proof.proof);
  if (!response.ok) {
    throw await gatewayFailure(response);
  }
  return parseWebSocketTicket(await response.json());
}

export async function refreshEndpointSession(identity: EndpointIdentity): Promise<RegistrationSession> {
  const path = '/gateway/v1/tokens/refresh';
  const challengeResponse = await fetch(path, { credentials: 'include', method: 'POST' });
  const nonce = challengeResponse.headers.get('DPoP-Nonce');
  if (challengeResponse.status !== 401 || nonce === null || nonce === '') {
    throw await gatewayFailure(challengeResponse);
  }
  const proof = await createDpopProof({
    htm: 'POST',
    htu: new URL(path, window.location.origin).toString(),
    nonce,
    privateKey: identity.signing.privateKey,
    publicJwk: identity.signing.publicJwk,
  });
  const response = await fetch(path, {
    credentials: 'include',
    headers: { Accept: 'application/json', DPoP: proof.proof },
    method: 'POST',
  });
  if (!response.ok) {
    throw await gatewayFailure(response);
  }
  return parseRegistration(await response.json());
}

async function requestJson<T>(path: string, init: RequestInit): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  headers.set('Content-Type', 'application/json');
  const response = await fetch(path, { ...init, credentials: 'include', headers });
  const text = await response.text();
  let value: unknown = null;
  if (text !== '') {
    try {
      value = JSON.parse(text) as unknown;
    } catch {
      throw new HcApiError('Platform returned invalid JSON', 'HC_INVALID_RESPONSE', response.status);
    }
  }
  if (!response.ok) {
    const error = value !== null && typeof value === 'object' ? (value as Record<string, unknown>) : {};
    throw new HcApiError(
      typeof error.message === 'string' ? error.message : 'Platform request failed',
      typeof error.code === 'string' ? error.code : 'HC_REQUEST_FAILED',
      response.status,
    );
  }
  return value as T;
}

function csrfHeaders(): HeadersInit {
  const csrf = readCookie('mss_csrf');
  if (csrf === null) {
    throw new HcApiError('Browser session CSRF token is unavailable', 'HC_CSRF_UNAVAILABLE', 401);
  }
  return { 'X-CSRF-Token': csrf };
}

function readCookie(name: string): string | null {
  const prefix = `${encodeURIComponent(name)}=`;
  const entry = document.cookie
    .split(';')
    .map((cookie) => cookie.trim())
    .find((cookie) => cookie.startsWith(prefix));
  return entry === undefined ? null : decodeURIComponent(entry.slice(prefix.length));
}

function parseChallenge(input: unknown): RegistrationChallenge {
  const value = objectValue(input, 'registration challenge');
  if (
    typeof value.challengeId !== 'string' ||
    !/^[0-9a-f]{32}$/u.test(value.challengeId) ||
    typeof value.challenge !== 'string' ||
    typeof value.expiresAt !== 'string' ||
    !Number.isFinite(Date.parse(value.expiresAt)) ||
    value.transcriptVersion !== 'MSS-HC-REGISTER-V1'
  ) {
    throw new HcApiError('Registration challenge is invalid', 'HC_INVALID_RESPONSE', 500);
  }
  return value as unknown as RegistrationChallenge;
}

function parseRegistration(input: unknown): RegistrationSession {
  const value = objectValue(input, 'registration response');
  if (
    typeof value.endpointId !== 'string' ||
    !/^[0-9a-f]{32}$/u.test(value.endpointId) ||
    value.tokenType !== 'DPoP' ||
    typeof value.accessToken !== 'string' ||
    value.accessToken.length !== 43 ||
    typeof value.accessExpiresAt !== 'string' ||
    !Number.isFinite(Date.parse(value.accessExpiresAt)) ||
    typeof value.signingJkt !== 'string' ||
    typeof value.kemJkt !== 'string'
  ) {
    throw new HcApiError('Registration response is invalid', 'HC_INVALID_RESPONSE', 500);
  }
  return value as unknown as RegistrationSession;
}

function parseWebSocketTicket(input: unknown): WebSocketTicket {
  const value = objectValue(input, 'WebSocket ticket response');
  if (
    typeof value.ticket !== 'string' ||
    value.ticket.length !== 43 ||
    typeof value.expiresAt !== 'string' ||
    !Number.isFinite(Date.parse(value.expiresAt)) ||
    value.protocol !== 'mss.awp.v1' ||
    typeof value.websocketUrl !== 'string' ||
    value.websocketUrl.includes(value.ticket)
  ) {
    throw new HcApiError('WebSocket ticket response is invalid', 'HC_INVALID_RESPONSE', 500);
  }
  return value as unknown as WebSocketTicket;
}

function gatewayRequest(path: string, accessToken: string, proof?: string): Promise<Response> {
  const headers = new Headers({ Accept: 'application/json', Authorization: `DPoP ${accessToken}` });
  if (proof !== undefined) {
    headers.set('DPoP', proof);
  }
  return fetch(path, { credentials: 'include', headers, method: 'POST' });
}

async function gatewayFailure(response: Response): Promise<HcApiError> {
  let value: unknown;
  try {
    value = await response.json();
  } catch {
    return new HcApiError('Gateway returned an invalid response', 'HC_INVALID_RESPONSE', response.status);
  }
  const error = value !== null && typeof value === 'object' ? (value as Record<string, unknown>) : {};
  return new HcApiError(
    typeof error.message === 'string' ? error.message : 'Gateway request failed',
    typeof error.code === 'string' ? error.code : 'HC_GATEWAY_FAILED',
    response.status,
  );
}

function objectValue(input: unknown, label: string): Record<string, unknown> {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) {
    throw new HcApiError(`Invalid ${label}`, 'HC_INVALID_RESPONSE', 500);
  }
  return input as Record<string, unknown>;
}
