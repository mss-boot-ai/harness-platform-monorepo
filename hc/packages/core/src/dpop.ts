import { base64UrlDecode, base64UrlEncode, signP1363LowS } from './crypto';
import { parseP256PublicJwk, type P256PublicJwk } from './identity';

export interface DpopProofInput {
  readonly accessToken: string;
  readonly htm: string;
  readonly htu: string;
  readonly nonce: string;
  readonly privateKey: CryptoKey;
  readonly publicJwk: P256PublicJwk;
  readonly issuedAt?: Date;
  readonly jti?: string;
}

export interface DpopProof {
  readonly ath: string;
  readonly htm: string;
  readonly htu: string;
  readonly issuedAt: number;
  readonly jti: string;
  readonly proof: string;
}

const textEncoder = new TextEncoder();

export async function createDpopProof(input: DpopProofInput): Promise<DpopProof> {
  if (input.accessToken.length === 0) {
    throw new Error('DPoP access token is required');
  }
  if (input.nonce.length === 0) {
    throw new Error('DPoP server nonce is required');
  }
  const method = input.htm.trim().toUpperCase();
  if (!/^[A-Z]+$/u.test(method)) {
    throw new Error('DPoP HTTP method is invalid');
  }
  const htu = normalizeHtu(input.htu);
  const publicJwk = parseP256PublicJwk(input.publicJwk);
  const jti = input.jti ?? crypto.randomUUID();
  if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/u.test(jti)) {
    throw new Error('DPoP jti must be a canonical UUID v4');
  }
  const issuedAt = Math.floor((input.issuedAt ?? new Date()).getTime() / 1000);
  if (!Number.isSafeInteger(issuedAt)) {
    throw new Error('DPoP issued-at time is invalid');
  }
  const ath = await accessTokenHash(input.accessToken);
  const header = { alg: 'ES256', jwk: publicJwk, typ: 'dpop+jwt' };
  const claims = { ath, htm: method, htu, iat: issuedAt, jti, nonce: input.nonce };
  const protectedSegment = base64UrlEncode(textEncoder.encode(JSON.stringify(header)));
  const payloadSegment = base64UrlEncode(textEncoder.encode(JSON.stringify(claims)));
  const signingInput = `${protectedSegment}.${payloadSegment}`;
  const signature = await signP1363LowS(input.privateKey, textEncoder.encode(signingInput));

  return {
    ath,
    htm: method,
    htu,
    issuedAt,
    jti,
    proof: `${signingInput}.${base64UrlEncode(signature)}`,
  };
}

export function normalizeHtu(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error('DPoP htu must be an absolute HTTP URI');
  }
  if (parsed.username !== '' || parsed.password !== '') {
    throw new Error('DPoP htu must not contain userinfo');
  }
  const hostname = parsed.hostname.toLowerCase();
  const loopback = hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]';
  if (parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && loopback)) {
    throw new Error('DPoP htu must use HTTPS except for loopback development');
  }
  parsed.search = '';
  parsed.hash = '';
  return parsed.toString();
}

async function accessTokenHash(token: string): Promise<string> {
  if (globalThis.crypto?.subtle === undefined) {
    throw new Error('WebCrypto is unavailable');
  }
  const digest = await globalThis.crypto.subtle.digest('SHA-256', textEncoder.encode(token));
  return base64UrlEncode(new Uint8Array(digest));
}

export function decodeDpopSegment<T>(segment: string): T {
  return JSON.parse(new TextDecoder().decode(base64UrlDecode(segment))) as T;
}
