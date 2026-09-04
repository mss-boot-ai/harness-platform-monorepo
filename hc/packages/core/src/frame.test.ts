import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { signP1363LowS, verifyP1363LowS } from './crypto';
import {
  buildAckTranscript,
  buildErrorFrameTranscript,
  buildFrameAAD,
  createHCAckFramePacket,
  createHCResumeStatePacket,
  openABAUncertainErrorPacket,
  createHCToABAFramePacket,
  openABAToHCFramePacket,
  sessionChannelId,
} from './frame';
import {
  ControlType,
  Direction,
  EncryptedFrameSchema,
  ErrorCode,
  ErrorFrameSchema,
  FrameType,
  WirePacketSchema,
} from './generated/mss/awp/v1/wire_pb';
import { createEndpointIdentity, type P256PublicJwk } from './identity';
import type { OpenedSessionKeyPackage } from './session-key-package';

describe('Suite 0001 encrypted frames', () => {
  it('creates HC frames and opens signed ABA frames', async () => {
    const identity = await createEndpointIdentity('frame-test', 'Frame Test', 'web-ephemeral');
    const keys = testKeys();
    const binding = {
      abaEndpointId: '02020202020202020202020202020202',
      hcEndpointId: '03030303030303030303030303030303',
      sessionId: '01010101010101010101010101010101',
    };
    const request = new TextEncoder().encode('{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"test","prompt":[]}}');
    const hcPacket = await createHCToABAFramePacket(identity, keys, binding, request, 1n, new Date(1_800_000_000_000));
    const decodedHC = fromBinary(WirePacketSchema, hcPacket);
    expect(decodedHC.body.case).toBe('encrypted');
    if (decodedHC.body.case !== 'encrypted') {
      throw new Error('HC packet is not encrypted');
    }
    expect(decodedHC.body.value.direction).toBe(Direction.HC_TO_ABA);
    expect(decodedHC.body.value.ciphertext).not.toEqual(request);

    const abaSigning = await crypto.subtle.generateKey(
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign', 'verify'],
    );
    const abaSigningJwk = await publicJwk(abaSigning.publicKey);
    const response = new TextEncoder().encode('{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}');
    const abaPacket = await createABATestPacket(abaSigning.privateKey, keys, binding, response);
    const opened = await openABAToHCFramePacket(abaSigningJwk, keys, binding, abaPacket);
    expect(opened?.sequence).toBe(1n);
    expect(opened?.plaintext).toEqual(response);
    expect(opened?.messageId).toEqual(new Uint8Array(16).fill(9));
    expect(opened?.contentHash).toHaveLength(32);

    const ackPacket = await createHCAckFramePacket(
      identity,
      binding,
      Direction.ABA_TO_HC,
      1n,
      1n,
      new Date(1_800_000_000_000),
    );
    const decodedACK = fromBinary(WirePacketSchema, ackPacket);
    expect(decodedACK.body.case).toBe('ack');
    if (decodedACK.body.case !== 'ack') {
      throw new Error('HC packet is not an ACK');
    }
    const ack = decodedACK.body.value;
    expect(ack.endpointId).toEqual(hex(binding.hcEndpointId));
    expect(ack.acknowledgedDirection).toBe(Direction.ABA_TO_HC);
    expect(ack.highestContiguousSequence).toBe(1n);
    const transcript = buildAckTranscript({
      ackId: ack.ackId,
      acknowledgedDirection: ack.acknowledgedDirection,
      channelId: ack.channelId,
      createdAtMs: ack.createdAtMs,
      endpointId: ack.endpointId,
      highestContiguousSequence: ack.highestContiguousSequence,
      keyGeneration: ack.keyGeneration,
      receivedRanges: ack.receivedRanges,
      sessionId: ack.sessionId,
    });
    expect(transcript).toHaveLength(114);
    await expect(verifyP1363LowS(identity.signing.publicKey, transcript, ack.signature)).resolves.toBe(true);

    const resumePacket = await createHCResumeStatePacket(
      identity, binding, 1n, 0n, 1n, new Date(1_800_000_000_000),
    );
    const decodedResume = fromBinary(WirePacketSchema, resumePacket);
    expect(decodedResume.body.case).toBe('control');
    if (decodedResume.body.case !== 'control') {
      throw new Error('HC packet is not ResumeState control');
    }
    expect(decodedResume.body.value.type).toBe(ControlType.RESUME_STATE);
    expect(decodedResume.body.value.receiverEndpointId).toEqual(new Uint8Array(16));

    const errorId = new Uint8Array(16).fill(11);
    const relatedMessageId = new Uint8Array(16).fill(12);
    const safeMessage = 'Local agent dispatch result is uncertain';
    const errorTranscript = buildErrorFrameTranscript({
      code: ErrorCode.LOCAL_DISPATCH_UNCERTAIN,
      errorId,
      relatedMessageId,
      retryable: false,
      retryAfterMs: 0,
      safeMessage,
    });
    const errorPacket = toBinary(WirePacketSchema, create(WirePacketSchema, {
      body: {
        case: 'error',
        value: create(ErrorFrameSchema, {
          code: ErrorCode.LOCAL_DISPATCH_UNCERTAIN,
          errorId,
          relatedMessageId,
          safeMessage,
          signature: await signP1363LowS(abaSigning.privateKey, errorTranscript),
        }),
      },
      packetId: new Uint8Array(16).fill(13),
      wireMajor: 1,
    }));
    await expect(openABAUncertainErrorPacket(abaSigningJwk, errorPacket)).resolves.toEqual({
      relatedMessageId,
    });
  });
});

async function createABATestPacket(
  signingKey: CryptoKey,
  keys: OpenedSessionKeyPackage,
  binding: { readonly abaEndpointId: string; readonly hcEndpointId: string; readonly sessionId: string },
  plaintext: Uint8Array,
): Promise<Uint8Array> {
  const sessionId = hex(binding.sessionId);
  const abaEndpointId = hex(binding.abaEndpointId);
  const hcEndpointId = hex(binding.hcEndpointId);
  const channelId = await sessionChannelId(sessionId, abaEndpointId, hcEndpointId);
  const messageId = new Uint8Array(16).fill(9);
  const createdAtMs = 1_800_000_000_000n;
  const aad = buildFrameAAD({
    channelId,
    ciphertextLength: plaintext.length + 16,
    createdAtMs,
    direction: Direction.ABA_TO_HC,
    flags: 0,
    keyGeneration: 1n,
    keyId: keys.material.keyId,
    messageId,
    receiverEndpointId: hcEndpointId,
    senderEndpointId: abaEndpointId,
    sequence: 1n,
    sessionId,
  });
  const key = await crypto.subtle.importKey('raw', buffer(keys.abaToHcKey), 'AES-GCM', false, ['encrypt']);
  const nonce = new Uint8Array(12);
  nonce.set(keys.material.abaToHcNoncePrefix);
  new DataView(nonce.buffer).setBigUint64(4, 1n, false);
  const ciphertext = new Uint8Array(await crypto.subtle.encrypt(
    { additionalData: buffer(aad), iv: nonce.buffer, name: 'AES-GCM', tagLength: 128 },
    key,
    buffer(plaintext),
  ));
  const signatureInput = concatenate([new TextEncoder().encode('mss-awp-frame-signature-v1'), aad, ciphertext]);
  return toBinary(WirePacketSchema, create(WirePacketSchema, {
    body: {
      case: 'encrypted',
      value: create(EncryptedFrameSchema, {
        channelId,
        ciphertext,
        createdAtMs,
        cryptoSuiteId: 1,
        direction: Direction.ABA_TO_HC,
        frameType: FrameType.ACP_TRANSPORT_FRAME,
        keyGeneration: 1n,
        keyId: keys.material.keyId,
        messageId,
        receiverEndpointId: hcEndpointId,
        senderEndpointId: abaEndpointId,
        sequence: 1n,
        sessionId,
        signature: await signP1363LowS(signingKey, signatureInput),
      }),
    },
    packetId: new Uint8Array(16).fill(10),
    wireMajor: 1,
  }));
}

function testKeys(): OpenedSessionKeyPackage {
  return {
    abaToHcKey: new Uint8Array(32).fill(4),
    controlSequence: 2n,
    hcToAbaKey: new Uint8Array(32).fill(5),
    keyPackageId: new Uint8Array(16).fill(6),
    material: {
      abaToHcNoncePrefix: new Uint8Array(4).fill(7),
      expiresAtMs: 1_800_003_600_000n,
      generation: 1n,
      hcToAbaNoncePrefix: new Uint8Array(4).fill(6),
      keyId: new Uint8Array(16).fill(8),
      notBeforeMs: 1_800_000_000_000n,
      sessionId: new Uint8Array(16).fill(1),
      sessionNonce: new Uint8Array(32).fill(9),
      srk: new Uint8Array(32).fill(10),
    },
  };
}

async function publicJwk(key: CryptoKey): Promise<P256PublicJwk> {
  const value = await crypto.subtle.exportKey('jwk', key);
  if (value.crv !== 'P-256' || value.kty !== 'EC' || typeof value.x !== 'string' || typeof value.y !== 'string') {
    throw new Error('test public key is invalid');
  }
  return { crv: 'P-256', kty: 'EC', x: value.x, y: value.y };
}

function hex(value: string): Uint8Array {
  return Uint8Array.from({ length: value.length / 2 }, (_, index) =>
    Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
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

function buffer(value: Uint8Array): ArrayBuffer {
  const copy = new Uint8Array(value.length);
  copy.set(value);
  return copy.buffer;
}
