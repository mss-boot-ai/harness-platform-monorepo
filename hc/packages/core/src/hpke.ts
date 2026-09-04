import { base64UrlDecode } from './crypto';
import type { P256PublicJwk } from './identity';

export const suiteName = 'MSS-AWP-SUITE-0001';
export const keyPackageInfoBytes = 84;
export const keyPackagePlaintextBytes = 141;

const textEncoder = new TextEncoder();
const kemSuite = concatenate([textEncoder.encode('KEM'), uint16(0x0010)]);
const hpkeSuite = concatenate([
  textEncoder.encode('HPKE'),
  uint16(0x0010),
  uint16(0x0001),
  uint16(0x0002),
]);

export interface KeyPackageContext {
  readonly generation: bigint;
  readonly policyRevision: bigint;
  readonly recipientHcEndpointId: Uint8Array;
  readonly senderAbaEndpointId: Uint8Array;
  readonly sessionId: Uint8Array;
}

export interface SessionKeyMaterial {
  readonly abaToHcNoncePrefix: Uint8Array;
  readonly expiresAtMs: bigint;
  readonly generation: bigint;
  readonly hcToAbaNoncePrefix: Uint8Array;
  readonly notBeforeMs: bigint;
  readonly sessionId: Uint8Array;
  readonly sessionNonce: Uint8Array;
  readonly srk: Uint8Array;
}

export async function openKeyPackage(
  recipientPrivateKey: CryptoKey,
  recipientPublicJwk: P256PublicJwk,
  context: KeyPackageContext,
  enc: Uint8Array,
  ciphertext: Uint8Array,
): Promise<SessionKeyMaterial> {
  if (
    recipientPrivateKey.type !== 'private' ||
    recipientPrivateKey.algorithm.name !== 'ECDH' ||
    enc.length !== 65 ||
    enc[0] !== 4 ||
    ciphertext.length !== keyPackagePlaintextBytes + 16
  ) {
    throw new Error('HPKE key package input is invalid');
  }
  const subtle = requireWebCrypto();
  const info = buildKeyPackageInfo(context);
  const ephemeral = await subtle.importKey(
    'raw',
    asArrayBuffer(enc),
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    [],
  );
  const dh = new Uint8Array(await subtle.deriveBits(
    { name: 'ECDH', public: ephemeral },
    recipientPrivateKey,
    256,
  ));
  const recipientPublic = p256PublicBytes(recipientPublicJwk);
  const kemContext = concatenate([enc, recipientPublic]);
  const eaePrk = await labeledExtract(new Uint8Array(), kemSuite, 'eae_prk', dh);
  const sharedSecret = await labeledExpand(eaePrk, kemSuite, 'shared_secret', kemContext, 32);
  const pskIdHash = await labeledExtract(new Uint8Array(), hpkeSuite, 'psk_id_hash', new Uint8Array());
  const infoHash = await labeledExtract(new Uint8Array(), hpkeSuite, 'info_hash', info);
  const keyScheduleContext = concatenate([new Uint8Array([0]), pskIdHash, infoHash]);
  const secret = await labeledExtract(sharedSecret, hpkeSuite, 'secret', new Uint8Array());
  const key = await labeledExpand(secret, hpkeSuite, 'key', keyScheduleContext, 32);
  const nonce = await labeledExpand(secret, hpkeSuite, 'base_nonce', keyScheduleContext, 12);
  const aeadKey = await subtle.importKey('raw', asArrayBuffer(key), { name: 'AES-GCM' }, false, ['decrypt']);
  let plaintext: Uint8Array;
  try {
    plaintext = new Uint8Array(await subtle.decrypt(
      { additionalData: asArrayBuffer(info), iv: asArrayBuffer(nonce), name: 'AES-GCM', tagLength: 128 },
      aeadKey,
      asArrayBuffer(ciphertext),
    ));
  } catch {
    throw new Error('HPKE key package authentication failed');
  } finally {
    dh.fill(0);
    eaePrk.fill(0);
    sharedSecret.fill(0);
    secret.fill(0);
    key.fill(0);
  }
  try {
    return parseKeyPackagePlaintext(plaintext, context);
  } finally {
    plaintext.fill(0);
  }
}

export function buildKeyPackageInfo(context: KeyPackageContext): Uint8Array {
  validateContext(context);
  const result = concatenate([
    textEncoder.encode('mss-key-package-v1'),
    context.sessionId,
    uint64(context.generation),
    context.senderAbaEndpointId,
    context.recipientHcEndpointId,
    uint16(1),
    uint64(context.policyRevision),
  ]);
  if (result.length !== keyPackageInfoBytes) {
    throw new Error('key package info length is invalid');
  }
  return result;
}

export async function deriveSessionDirectionKeys(
  material: SessionKeyMaterial,
  hcEndpointId: Uint8Array,
): Promise<{ readonly abaToHc: Uint8Array; readonly hcToAba: Uint8Array }> {
  requireId(hcEndpointId, 'HC endpoint');
  const prk = await hkdfExtract(material.sessionNonce, material.srk);
  const prefix = `mss-awp/v1/session/${hex(material.sessionId)}/generation/${material.generation}/endpoint/${hex(hcEndpointId)}`;
  try {
    return {
      abaToHc: await hkdfExpand(prk, textEncoder.encode(`${prefix}/aba-to-hc`), 32),
      hcToAba: await hkdfExpand(prk, textEncoder.encode(`${prefix}/hc-to-aba`), 32),
    };
  } finally {
    prk.fill(0);
  }
}

export function frameNonce(prefix: Uint8Array, sequence: bigint): Uint8Array {
  if (prefix.length !== 4 || sequence <= 0n || sequence > 0xffff_ffff_ffff_ffffn) {
    throw new Error('frame nonce input is invalid');
  }
  return concatenate([prefix, uint64(sequence)]);
}

function parseKeyPackagePlaintext(
  plaintext: Uint8Array,
  context: KeyPackageContext,
): SessionKeyMaterial {
  if (plaintext.length !== keyPackagePlaintextBytes) {
    throw new Error('key package plaintext length is invalid');
  }
  const label = textEncoder.encode('mss-key-package-plaintext-v1');
  if (!equalBytes(plaintext.subarray(0, label.length), label)) {
    throw new Error('key package plaintext label is invalid');
  }
  let offset = label.length;
  const sessionId = plaintext.slice(offset, offset += 16);
  const generation = readUint64(plaintext, offset); offset += 8;
  const srk = plaintext.slice(offset, offset += 32);
  const sessionNonce = plaintext.slice(offset, offset += 32);
  const hcToAbaNoncePrefix = plaintext.slice(offset, offset += 4);
  const abaToHcNoncePrefix = plaintext.slice(offset, offset += 4);
  const notBeforeMs = readInt64(plaintext, offset); offset += 8;
  const expiresAtMs = readInt64(plaintext, offset); offset += 8;
  const role = plaintext[offset];
  if (
    !equalBytes(sessionId, context.sessionId) ||
    generation !== context.generation ||
    role !== 1 ||
    notBeforeMs <= 0n ||
    expiresAtMs <= notBeforeMs ||
    equalBytes(hcToAbaNoncePrefix, abaToHcNoncePrefix)
  ) {
    srk.fill(0);
    sessionNonce.fill(0);
    throw new Error('key package plaintext binding is invalid');
  }
  return {
    abaToHcNoncePrefix,
    expiresAtMs,
    generation,
    hcToAbaNoncePrefix,
    notBeforeMs,
    sessionId,
    sessionNonce,
    srk,
  };
}

async function labeledExtract(
  salt: Uint8Array,
  suite: Uint8Array,
  label: string,
  ikm: Uint8Array,
): Promise<Uint8Array> {
  return hkdfExtract(salt, concatenate([textEncoder.encode('HPKE-v1'), suite, textEncoder.encode(label), ikm]));
}

async function labeledExpand(
  prk: Uint8Array,
  suite: Uint8Array,
  label: string,
  info: Uint8Array,
  length: number,
): Promise<Uint8Array> {
  return hkdfExpand(
    prk,
    concatenate([uint16(length), textEncoder.encode('HPKE-v1'), suite, textEncoder.encode(label), info]),
    length,
  );
}

async function hkdfExtract(salt: Uint8Array, ikm: Uint8Array): Promise<Uint8Array> {
  return hmac(salt.length === 0 ? new Uint8Array(32) : salt, ikm);
}

async function hkdfExpand(prk: Uint8Array, info: Uint8Array, length: number): Promise<Uint8Array> {
  if (length <= 0 || length > 255 * 32) {
    throw new Error('HKDF output length is invalid');
  }
  const output = new Uint8Array(length);
  let previous: Uint8Array<ArrayBufferLike> = new Uint8Array();
  let offset = 0;
  for (let counter = 1; offset < length; counter += 1) {
    previous = await hmac(prk, concatenate([previous, info, new Uint8Array([counter])]));
    const take = Math.min(previous.length, length - offset);
    output.set(previous.subarray(0, take), offset);
    offset += take;
  }
  previous.fill(0);
  return output;
}

async function hmac(key: Uint8Array, input: Uint8Array): Promise<Uint8Array> {
  const cryptoKey = await requireWebCrypto().importKey(
    'raw',
    asArrayBuffer(key),
    { hash: 'SHA-256', name: 'HMAC' },
    false,
    ['sign'],
  );
  return new Uint8Array(await requireWebCrypto().sign('HMAC', cryptoKey, asArrayBuffer(input)));
}

function p256PublicBytes(jwk: P256PublicJwk): Uint8Array {
  const x = base64UrlDecode(jwk.x);
  const y = base64UrlDecode(jwk.y);
  if (jwk.crv !== 'P-256' || jwk.kty !== 'EC' || x.length !== 32 || y.length !== 32) {
    throw new Error('recipient P-256 public key is invalid');
  }
  return concatenate([new Uint8Array([4]), x, y]);
}

function validateContext(context: KeyPackageContext): void {
  requireId(context.sessionId, 'session');
  requireId(context.senderAbaEndpointId, 'ABA endpoint');
  requireId(context.recipientHcEndpointId, 'HC endpoint');
  if (
    equalBytes(context.senderAbaEndpointId, context.recipientHcEndpointId) ||
    context.generation <= 0n ||
    context.policyRevision <= 0n
  ) {
    throw new Error('key package context is invalid');
  }
}

function requireId(value: Uint8Array, label: string): void {
  if (value.length !== 16 || value.every((byte) => byte === 0)) {
    throw new Error(`${label} ID is invalid`);
  }
}

function uint16(value: number): Uint8Array {
  if (!Number.isSafeInteger(value) || value < 0 || value > 0xffff) {
    throw new Error('uint16 value is invalid');
  }
  const result = new Uint8Array(2);
  new DataView(result.buffer).setUint16(0, value, false);
  return result;
}

function uint64(value: bigint): Uint8Array {
  if (value < 0n || value > 0xffff_ffff_ffff_ffffn) {
    throw new Error('uint64 value is invalid');
  }
  const result = new Uint8Array(8);
  new DataView(result.buffer).setBigUint64(0, value, false);
  return result;
}

function readUint64(value: Uint8Array, offset: number): bigint {
  return new DataView(value.buffer, value.byteOffset, value.byteLength).getBigUint64(offset, false);
}

function readInt64(value: Uint8Array, offset: number): bigint {
  return new DataView(value.buffer, value.byteOffset, value.byteLength).getBigInt64(offset, false);
}

function hex(value: Uint8Array): string {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function concatenate(values: readonly Uint8Array[]): Uint8Array {
  const output = new Uint8Array(values.reduce((sum, value) => sum + value.length, 0));
  let offset = 0;
  for (const value of values) {
    output.set(value, offset);
    offset += value.length;
  }
  return output;
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function asArrayBuffer(value: Uint8Array): ArrayBuffer {
  const copy = new Uint8Array(value.byteLength);
  copy.set(value);
  return copy.buffer;
}

function requireWebCrypto(): SubtleCrypto {
  if (globalThis.crypto?.subtle === undefined) {
    throw new Error('WebCrypto is unavailable');
  }
  return globalThis.crypto.subtle;
}
