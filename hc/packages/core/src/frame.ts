import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { importP256VerifyingKey, signP1363LowS, verifyP1363LowS } from './crypto';
import {
  Direction,
  EncryptedFrameSchema,
  FrameType,
  WirePacketSchema,
} from './generated/mss/awp/v1/wire_pb';
import type { EndpointIdentity, P256PublicJwk } from './identity';
import type { OpenedSessionKeyPackage } from './session-key-package';

const textEncoder = new TextEncoder();

interface FrameBinding {
  readonly abaEndpointId: string;
  readonly hcEndpointId: string;
  readonly sessionId: string;
}

export async function createHCToABAFramePacket(
  identity: EndpointIdentity,
  keys: OpenedSessionKeyPackage,
  binding: FrameBinding,
  plaintext: Uint8Array,
  sequence: bigint,
  now = new Date(),
): Promise<Uint8Array> {
  const sessionId = decodeHexId(binding.sessionId);
  const abaEndpointId = decodeHexId(binding.abaEndpointId);
  const hcEndpointId = decodeHexId(binding.hcEndpointId);
  const messageId = crypto.getRandomValues(new Uint8Array(16));
  const channelId = await sessionChannelId(sessionId, abaEndpointId, hcEndpointId);
  const ciphertextLength = plaintext.length + 16;
  const aad = buildFrameAAD({
    channelId,
    ciphertextLength,
    createdAtMs: BigInt(now.getTime()),
    direction: Direction.HC_TO_ABA,
    flags: 0,
    keyGeneration: 1n,
    keyId: keys.material.keyId,
    messageId,
    receiverEndpointId: abaEndpointId,
    senderEndpointId: hcEndpointId,
    sequence,
    sessionId,
  });
  const ciphertext = await aesGcm('encrypt', keys.hcToAbaKey, keys.material.hcToAbaNoncePrefix, sequence, plaintext, aad);
  if (ciphertext.length !== ciphertextLength) {
    throw new Error('encrypted frame length is invalid');
  }
  const signature = await signP1363LowS(
    identity.signing.privateKey,
    frameSignatureInput(aad, ciphertext),
  );
  return toBinary(WirePacketSchema, create(WirePacketSchema, {
    body: {
      case: 'encrypted',
      value: create(EncryptedFrameSchema, {
        channelId,
        ciphertext,
        createdAtMs: BigInt(now.getTime()),
        cryptoSuiteId: 1,
        direction: Direction.HC_TO_ABA,
        flags: 0,
        frameType: FrameType.ACP_TRANSPORT_FRAME,
        keyGeneration: 1n,
        keyId: keys.material.keyId,
        messageId,
        receiverEndpointId: abaEndpointId,
        senderEndpointId: hcEndpointId,
        sequence,
        sessionId,
        signature,
      }),
    },
    packetId: crypto.getRandomValues(new Uint8Array(16)),
    wireMajor: 1,
    wireMinor: 0,
  }));
}

export interface OpenedFrame {
  readonly plaintext: Uint8Array;
  readonly sequence: bigint;
}

export async function openABAToHCFramePacket(
  abaSigningPublicJwk: P256PublicJwk,
  keys: OpenedSessionKeyPackage,
  binding: FrameBinding,
  encoded: Uint8Array,
): Promise<OpenedFrame | null> {
  const packet = fromBinary(WirePacketSchema, encoded);
  if (packet.body.case !== 'encrypted') {
    return null;
  }
  if (packet.wireMajor !== 1 || packet.wireMinor !== 0 || packet.packetId.length !== 16) {
    throw new Error('encrypted AWP packet is invalid');
  }
  const frame = packet.body.value;
  const sessionId = decodeHexId(binding.sessionId);
  const abaEndpointId = decodeHexId(binding.abaEndpointId);
  const hcEndpointId = decodeHexId(binding.hcEndpointId);
  const channelId = await sessionChannelId(sessionId, abaEndpointId, hcEndpointId);
  if (
    frame.cryptoSuiteId !== 1 ||
    frame.frameType !== FrameType.ACP_TRANSPORT_FRAME ||
    frame.flags !== 0 ||
    frame.messageId.length !== 16 ||
    !equalBytes(frame.channelId, channelId) ||
    !equalBytes(frame.sessionId, sessionId) ||
    !equalBytes(frame.senderEndpointId, abaEndpointId) ||
    !equalBytes(frame.receiverEndpointId, hcEndpointId) ||
    frame.direction !== Direction.ABA_TO_HC ||
    frame.sequence <= 0n ||
    frame.keyGeneration !== 1n ||
    !equalBytes(frame.keyId, keys.material.keyId) ||
    frame.ciphertext.length < 16 ||
    frame.ciphertext.length > 1_048_576 ||
    frame.signature.length !== 64
  ) {
    throw new Error('encrypted frame binding is invalid');
  }
  const aad = buildFrameAAD({
    channelId: frame.channelId,
    ciphertextLength: frame.ciphertext.length,
    createdAtMs: frame.createdAtMs,
    direction: frame.direction,
    flags: frame.flags,
    keyGeneration: frame.keyGeneration,
    keyId: frame.keyId,
    messageId: frame.messageId,
    receiverEndpointId: frame.receiverEndpointId,
    senderEndpointId: frame.senderEndpointId,
    sequence: frame.sequence,
    sessionId: frame.sessionId,
  });
  const signingKey = await importP256VerifyingKey(abaSigningPublicJwk);
  if (!(await verifyP1363LowS(signingKey, frameSignatureInput(aad, frame.ciphertext), frame.signature))) {
    throw new Error('encrypted frame signature is invalid');
  }
  const plaintext = await aesGcm(
    'decrypt', keys.abaToHcKey, keys.material.abaToHcNoncePrefix,
    frame.sequence, frame.ciphertext, aad,
  );
  const text = new TextDecoder('utf-8', { fatal: true }).decode(plaintext);
  JSON.parse(text) as unknown;
  return { plaintext, sequence: frame.sequence };
}

interface FrameAADInput {
  readonly channelId: Uint8Array;
  readonly ciphertextLength: number;
  readonly createdAtMs: bigint;
  readonly direction: Direction;
  readonly flags: number;
  readonly keyGeneration: bigint;
  readonly keyId: Uint8Array;
  readonly messageId: Uint8Array;
  readonly receiverEndpointId: Uint8Array;
  readonly senderEndpointId: Uint8Array;
  readonly sequence: bigint;
  readonly sessionId: Uint8Array;
}

export function buildFrameAAD(input: FrameAADInput): Uint8Array {
  for (const value of [
    input.messageId, input.channelId, input.sessionId,
    input.senderEndpointId, input.receiverEndpointId, input.keyId,
  ]) {
    if (value.length !== 16 || value.every((byte) => byte === 0)) {
      throw new Error('frame AAD identifier is invalid');
    }
  }
  if (
    input.flags < 0 || input.flags > 0xffff ||
    input.sequence <= 0n || input.keyGeneration <= 0n ||
    input.createdAtMs < 0n || input.ciphertextLength < 16 || input.ciphertextLength > 1_048_576 ||
    ![Direction.HC_TO_ABA, Direction.ABA_TO_HC].includes(input.direction)
  ) {
    throw new Error('frame AAD field is invalid');
  }
  const output = new Uint8Array(148);
  output.set(textEncoder.encode('AWP1'), 0);
  const view = new DataView(output.buffer);
  view.setUint16(4, 1, false);
  view.setUint16(6, 0, false);
  view.setUint16(8, 1, false);
  view.setUint16(10, 1, false);
  view.setUint32(12, input.flags, false);
  output.set(input.messageId, 16);
  output.set(input.channelId, 32);
  output.set(input.sessionId, 48);
  output.set(input.senderEndpointId, 64);
  output.set(input.receiverEndpointId, 80);
  output[96] = input.direction;
  view.setBigUint64(104, input.sequence, false);
  view.setBigUint64(112, input.keyGeneration, false);
  output.set(input.keyId, 120);
  view.setBigInt64(136, input.createdAtMs, false);
  view.setUint32(144, input.ciphertextLength, false);
  return output;
}

export async function sessionChannelId(
  sessionId: Uint8Array,
  abaEndpointId: Uint8Array,
  hcEndpointId: Uint8Array,
): Promise<Uint8Array> {
  const digest = await crypto.subtle.digest('SHA-256', asArrayBuffer(concatenate([
    textEncoder.encode('mss-awp-channel-v1'), sessionId, abaEndpointId, hcEndpointId,
  ])));
  return new Uint8Array(digest).slice(0, 16);
}

async function aesGcm(
  operation: 'encrypt' | 'decrypt',
  key: Uint8Array,
  prefix: Uint8Array,
  sequence: bigint,
  input: Uint8Array,
  aad: Uint8Array,
): Promise<Uint8Array> {
  if (key.length !== 32 || prefix.length !== 4 || sequence <= 0n) {
    throw new Error('frame key or nonce is invalid');
  }
  const cryptoKey = await crypto.subtle.importKey('raw', asArrayBuffer(key), 'AES-GCM', false, [operation]);
  const nonce = new Uint8Array(12);
  nonce.set(prefix);
  new DataView(nonce.buffer).setBigUint64(4, sequence, false);
  try {
    const params = { additionalData: asArrayBuffer(aad), iv: nonce.buffer, name: 'AES-GCM', tagLength: 128 };
    const result = operation === 'encrypt'
      ? await crypto.subtle.encrypt(params, cryptoKey, asArrayBuffer(input))
      : await crypto.subtle.decrypt(params, cryptoKey, asArrayBuffer(input));
    return new Uint8Array(result);
  } catch {
    throw new Error('frame AEAD operation failed');
  }
}

function frameSignatureInput(aad: Uint8Array, ciphertext: Uint8Array): Uint8Array {
  return concatenate([textEncoder.encode('mss-awp-frame-signature-v1'), aad, ciphertext]);
}

function decodeHexId(value: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(value)) {
    throw new Error('frame identifier is invalid');
  }
  return Uint8Array.from({ length: 16 }, (_, index) => Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
}

function concatenate(values: readonly Uint8Array[]): Uint8Array {
  const result = new Uint8Array(values.reduce((sum, value) => sum + value.length, 0));
  let offset = 0;
  for (const value of values) {
    result.set(value, offset);
    offset += value.length;
  }
  return result;
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function asArrayBuffer(value: Uint8Array): ArrayBuffer {
  const copy = new Uint8Array(value.byteLength);
  copy.set(value);
  return copy.buffer;
}
