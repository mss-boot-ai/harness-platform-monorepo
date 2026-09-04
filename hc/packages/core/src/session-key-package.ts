import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { importP256VerifyingKey, signP1363LowS, verifyP1363LowS } from './crypto';
import {
  ControlFrameSchema,
  ControlType,
  SessionKeyPackageAckSchema,
  SessionKeyPackageSchema,
  WirePacketSchema,
} from './generated/mss/awp/v1/wire_pb';
import {
  deriveSessionDirectionKeys,
  openKeyPackage,
  type SessionKeyMaterial,
} from './hpke';
import type { EndpointIdentity, P256PublicJwk } from './identity';

const textEncoder = new TextEncoder();

export interface OpenedSessionKeyPackage {
  readonly abaToHcKey: Uint8Array;
  readonly controlSequence: bigint;
  readonly hcToAbaKey: Uint8Array;
  readonly keyPackageId: Uint8Array;
  readonly material: SessionKeyMaterial;
}

export async function openSessionKeyPackagePacket(
  encoded: Uint8Array,
  input: {
    readonly abaEndpointId: string;
    readonly abaSigningPublicJwk: P256PublicJwk;
    readonly hcEndpointId: string;
    readonly identity: EndpointIdentity;
    readonly sessionId: string;
  },
  now = new Date(),
): Promise<OpenedSessionKeyPackage | null> {
  if (encoded.length === 0 || encoded.length > 1_048_576) {
    throw new Error('AWP packet size is invalid');
  }
  const packet = fromBinary(WirePacketSchema, encoded);
  if (packet.wireMajor !== 1 || packet.wireMinor !== 0 || packet.packetId.length !== 16) {
    throw new Error('AWP packet envelope is invalid');
  }
  if (packet.body.case !== 'control' || packet.body.value.type !== ControlType.SESSION_KEY_PACKAGE) {
    return null;
  }
  const control = packet.body.value;
  const abaEndpointId = decodeHexId(input.abaEndpointId);
  const hcEndpointId = decodeHexId(input.hcEndpointId);
  const sessionId = decodeHexId(input.sessionId);
  if (
    control.messageId.length !== 16 ||
    !equalBytes(control.senderEndpointId, abaEndpointId) ||
    !equalBytes(control.receiverEndpointId, hcEndpointId) ||
    control.controlSequence <= 0n ||
    control.payload.length === 0 ||
    control.signature.length !== 64 ||
    !timeWithin(control.createdAtMs, BigInt(now.getTime()), 60_000n)
  ) {
    throw new Error('SessionKeyPackage control binding is invalid');
  }
  const signingKey = await importP256VerifyingKey(input.abaSigningPublicJwk);
  const outerTranscript = buildControlTranscript({
    controlSequence: control.controlSequence,
    controlType: control.type,
    createdAtMs: control.createdAtMs,
    messageId: control.messageId,
    payload: control.payload,
    receiverEndpointId: control.receiverEndpointId,
    senderEndpointId: control.senderEndpointId,
  });
  if (!(await verifyP1363LowS(signingKey, outerTranscript, control.signature))) {
    throw new Error('SessionKeyPackage control signature is invalid');
  }
  const keyPackage = fromBinary(SessionKeyPackageSchema, control.payload);
  if (
    keyPackage.keyPackageId.length !== 16 ||
    !equalBytes(keyPackage.sessionId, sessionId) ||
    keyPackage.keyGeneration !== 1n ||
    !equalBytes(keyPackage.issuerAbaEndpointId, abaEndpointId) ||
    !equalBytes(keyPackage.recipientHcEndpointId, hcEndpointId) ||
    keyPackage.issuerCredentialId.length !== 16 ||
    keyPackage.policyRevision !== 1n ||
    keyPackage.cryptoSuite !== 'MSS-AWP-SUITE-0001' ||
    keyPackage.hpkeEnc.length !== 65 ||
    keyPackage.hpkeCiphertext.length !== 157 ||
    keyPackage.issuerSignature.length !== 64 ||
    keyPackage.notBeforeMs > BigInt(now.getTime() + 60_000) ||
    keyPackage.expiresAtMs <= BigInt(now.getTime()) ||
    keyPackage.expiresAtMs > keyPackage.notBeforeMs + 86_400_000n
  ) {
    throw new Error('SessionKeyPackage payload binding is invalid');
  }
  const envelopeTranscript = buildKeyPackageEnvelopeTranscript({
    abaEndpointId,
    ciphertext: keyPackage.hpkeCiphertext,
    credentialId: keyPackage.issuerCredentialId,
    enc: keyPackage.hpkeEnc,
    expiresAtMs: keyPackage.expiresAtMs,
    generation: keyPackage.keyGeneration,
    hcEndpointId,
    keyPackageId: keyPackage.keyPackageId,
    notBeforeMs: keyPackage.notBeforeMs,
    policyRevision: keyPackage.policyRevision,
    sessionId,
  });
  if (!(await verifyP1363LowS(signingKey, envelopeTranscript, keyPackage.issuerSignature))) {
    throw new Error('SessionKeyPackage issuer signature is invalid');
  }
  const material = await openKeyPackage(
    input.identity.kem.privateKey,
    input.identity.kem.publicJwk,
    {
      generation: keyPackage.keyGeneration,
      policyRevision: keyPackage.policyRevision,
      recipientHcEndpointId: hcEndpointId,
      senderAbaEndpointId: abaEndpointId,
      sessionId,
    },
    keyPackage.hpkeEnc,
    keyPackage.hpkeCiphertext,
  );
  const directions = await deriveSessionDirectionKeys(material, hcEndpointId);
  return {
    abaToHcKey: directions.abaToHc,
    controlSequence: control.controlSequence,
    hcToAbaKey: directions.hcToAba,
    keyPackageId: keyPackage.keyPackageId,
    material,
  };
}

export async function createSessionKeyPackageAckPacket(
  identity: EndpointIdentity,
  input: {
    readonly abaEndpointId: string;
    readonly controlSequence: bigint;
    readonly hcEndpointId: string;
    readonly keyPackageId: Uint8Array;
    readonly sessionId: string;
  },
  now = new Date(),
): Promise<Uint8Array> {
  const abaEndpointId = decodeHexId(input.abaEndpointId);
  const hcEndpointId = decodeHexId(input.hcEndpointId);
  const sessionId = decodeHexId(input.sessionId);
  if (input.keyPackageId.length !== 16 || input.controlSequence <= 0n) {
    throw new Error('SessionKeyPackageAck input is invalid');
  }
  const acknowledgedAtMs = BigInt(now.getTime());
  const payload = toBinary(SessionKeyPackageAckSchema, create(SessionKeyPackageAckSchema, {
    acknowledgedAtMs,
    keyGeneration: 1n,
    keyPackageId: input.keyPackageId,
    recipientHcEndpointId: hcEndpointId,
    sessionId,
  }));
  const messageId = crypto.getRandomValues(new Uint8Array(16));
  const transcript = buildControlTranscript({
    controlSequence: input.controlSequence,
    controlType: ControlType.SESSION_KEY_PACKAGE_ACK,
    createdAtMs: acknowledgedAtMs,
    messageId,
    payload,
    receiverEndpointId: abaEndpointId,
    senderEndpointId: hcEndpointId,
  });
  return toBinary(WirePacketSchema, create(WirePacketSchema, {
    body: {
      case: 'control',
      value: create(ControlFrameSchema, {
        controlSequence: input.controlSequence,
        createdAtMs: acknowledgedAtMs,
        messageId,
        payload,
        receiverEndpointId: abaEndpointId,
        senderEndpointId: hcEndpointId,
        signature: await signP1363LowS(identity.signing.privateKey, transcript),
        type: ControlType.SESSION_KEY_PACKAGE_ACK,
      }),
    },
    packetId: crypto.getRandomValues(new Uint8Array(16)),
    wireMajor: 1,
    wireMinor: 0,
  }));
}

interface ControlTranscriptInput {
  readonly controlSequence: bigint;
  readonly controlType: number;
  readonly createdAtMs: bigint;
  readonly messageId: Uint8Array;
  readonly payload: Uint8Array;
  readonly receiverEndpointId: Uint8Array;
  readonly senderEndpointId: Uint8Array;
}

export function buildControlTranscript(input: ControlTranscriptInput): Uint8Array {
  if (
    input.messageId.length !== 16 ||
    input.senderEndpointId.length !== 16 ||
    input.receiverEndpointId.length !== 16 ||
    input.controlSequence <= 0n ||
    input.controlType <= 0 ||
    input.payload.length === 0 ||
    input.payload.length > 1_048_576
  ) {
    throw new Error('control transcript input is invalid');
  }
  return concatenate([
    textEncoder.encode('mss-awp-control-v1'), input.messageId,
    input.senderEndpointId, input.receiverEndpointId,
    uint64(input.controlSequence), uint64(input.createdAtMs), uint32(input.controlType),
    uint32(input.payload.length), input.payload,
  ]);
}

interface EnvelopeTranscriptInput {
  readonly abaEndpointId: Uint8Array;
  readonly ciphertext: Uint8Array;
  readonly credentialId: Uint8Array;
  readonly enc: Uint8Array;
  readonly expiresAtMs: bigint;
  readonly generation: bigint;
  readonly hcEndpointId: Uint8Array;
  readonly keyPackageId: Uint8Array;
  readonly notBeforeMs: bigint;
  readonly policyRevision: bigint;
  readonly sessionId: Uint8Array;
}

export function buildKeyPackageEnvelopeTranscript(input: EnvelopeTranscriptInput): Uint8Array {
  for (const value of [input.keyPackageId, input.sessionId, input.abaEndpointId, input.hcEndpointId, input.credentialId]) {
    if (value.length !== 16) {
      throw new Error('key package envelope identifier is invalid');
    }
  }
  if (
    input.generation <= 0n ||
    input.policyRevision <= 0n ||
    input.notBeforeMs <= 0n ||
    input.expiresAtMs <= input.notBeforeMs ||
    input.enc.length !== 65 ||
    input.ciphertext.length === 0 ||
    input.ciphertext.length > 16 * 1024
  ) {
    throw new Error('key package envelope input is invalid');
  }
  return concatenate([
    textEncoder.encode('mss-key-package-envelope-v1'),
    input.keyPackageId, input.sessionId, uint64(input.generation),
    input.abaEndpointId, input.hcEndpointId, input.credentialId,
    uint16(1), uint64(input.policyRevision), uint64(input.notBeforeMs), uint64(input.expiresAtMs),
    uint32(input.enc.length), input.enc, uint32(input.ciphertext.length), input.ciphertext,
  ]);
}

function decodeHexId(value: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(value)) {
    throw new Error('AWP identifier is invalid');
  }
  return Uint8Array.from({ length: 16 }, (_, index) => Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
}

function uint16(value: number): Uint8Array {
  const result = new Uint8Array(2);
  new DataView(result.buffer).setUint16(0, value, false);
  return result;
}

function uint32(value: number): Uint8Array {
  if (!Number.isSafeInteger(value) || value < 0 || value > 0xffff_ffff) {
    throw new Error('uint32 value is invalid');
  }
  const result = new Uint8Array(4);
  new DataView(result.buffer).setUint32(0, value, false);
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

function timeWithin(value: bigint, expected: bigint, skew: bigint): boolean {
  return value >= expected - skew && value <= expected + skew;
}
