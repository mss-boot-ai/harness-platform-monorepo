import { parseP256PublicJwk, type P256PublicJwk } from './identity';

const P256_ORDER = 0xffff_ffff_0000_0000_ffff_ffff_ffff_ffff_bce6_faad_a717_9e84_f3b9_cac2_fc63_2551n;
const P256_HALF_ORDER = P256_ORDER >> 1n;

function requireWebCrypto(): SubtleCrypto {
  if (globalThis.crypto?.subtle === undefined) {
    throw new Error('WebCrypto is unavailable');
  }
  return globalThis.crypto.subtle;
}

export function base64UrlEncode(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/u, '');
}

export function base64UrlDecode(value: string): Uint8Array {
  if (!/^[A-Za-z0-9_-]*$/u.test(value)) {
    throw new Error('value is not unpadded base64url');
  }
  const padding = '='.repeat((4 - (value.length % 4)) % 4);
  const binary = atob(value.replaceAll('-', '+').replaceAll('_', '/') + padding);
  return Uint8Array.from(binary, (character) => character.charCodeAt(0));
}

export async function accessTokenHash(token: string): Promise<string> {
  const digest = await requireWebCrypto().digest('SHA-256', new TextEncoder().encode(token));
  return base64UrlEncode(new Uint8Array(digest));
}

export async function importP256VerifyingKey(jwk: P256PublicJwk): Promise<CryptoKey> {
  return requireWebCrypto().importKey(
    'jwk',
    parseP256PublicJwk(jwk),
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['verify'],
  );
}

export async function verifyP1363LowS(
  publicKey: CryptoKey,
  message: Uint8Array,
  signature: Uint8Array,
): Promise<boolean> {
  if (!isP1363LowS(signature)) {
    return false;
  }
  return requireWebCrypto().verify(
    { hash: 'SHA-256', name: 'ECDSA' },
    publicKey,
    Uint8Array.from(signature),
    Uint8Array.from(message),
  );
}

export async function signP1363LowS(
  privateKey: CryptoKey,
  message: Uint8Array,
): Promise<Uint8Array> {
  const signature = new Uint8Array(
    await requireWebCrypto().sign(
      { hash: 'SHA-256', name: 'ECDSA' },
      privateKey,
      Uint8Array.from(message),
    ),
  );
  if (signature.length !== 64) {
    throw new Error('WebCrypto returned a non-P1363 P-256 signature');
  }
  const result = signature.slice();
  const s = bytesToBigInt(result.subarray(32));
  if (s > P256_HALF_ORDER) {
    result.set(bigIntToFixedBytes(P256_ORDER - s, 32), 32);
  }
  return result;
}

export function isP1363LowS(signature: Uint8Array): boolean {
  if (signature.length !== 64) {
    return false;
  }
  const r = bytesToBigInt(signature.subarray(0, 32));
  const s = bytesToBigInt(signature.subarray(32));
  return r > 0n && r < P256_ORDER && s > 0n && s <= P256_HALF_ORDER;
}

function bytesToBigInt(bytes: Uint8Array): bigint {
  let value = 0n;
  for (const byte of bytes) {
    value = (value << 8n) | BigInt(byte);
  }
  return value;
}

function bigIntToFixedBytes(value: bigint, length: number): Uint8Array {
  const result = new Uint8Array(length);
  let remaining = value;
  for (let index = length - 1; index >= 0; index -= 1) {
    result[index] = Number(remaining & 0xffn);
    remaining >>= 8n;
  }
  if (remaining !== 0n) {
    throw new Error('integer does not fit target length');
  }
  return result;
}
