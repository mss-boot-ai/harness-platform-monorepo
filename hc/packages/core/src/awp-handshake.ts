import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import {
  ChallengeResponseSchema,
  WirePacketSchema,
} from './generated/mss/awp/v1/wire_pb';
import {
  base64UrlDecode,
  importP256VerifyingKey,
  signP1363LowS,
  verifyP1363LowS,
} from './crypto';
import { parseP256PublicJwk, publicJwkThumbprint, type EndpointIdentity, type P256PublicJwk } from './identity';

const protocolName = 'mss.awp.v1';
const textEncoder = new TextEncoder();

export interface VerifiedTrustManifest {
  readonly expiresAt: Date;
  readonly onlinePublicJwk: P256PublicJwk;
  readonly onlineVerifyingKey: CryptoKey;
  readonly revision: bigint;
  readonly rootJkt: string;
}

export interface WebSocketTicketInput {
  readonly credentialId: string;
  readonly endpointId: string;
  readonly ticket: string;
  readonly websocketUrl: string;
}

export interface ReadyGatewayConnection {
  readonly connectionGeneration: bigint;
  readonly connectionId: Uint8Array;
  readonly fencingToken: Uint8Array;
  readonly heartbeatIntervalMs: number;
  readonly rootJkt: string;
  readonly socket: WebSocket;
}

export async function verifyTrustManifest(input: unknown, now = new Date()): Promise<VerifiedTrustManifest> {
  const value = objectValue(input, 'trust manifest');
  if (
    typeof value.payloadBase64Url !== 'string' ||
    typeof value.signatureBase64Url !== 'string' ||
    typeof value.revision !== 'number' ||
    !Number.isSafeInteger(value.revision) ||
    value.revision <= 0
  ) {
    throw new Error('trust manifest envelope is invalid');
  }
  const rootPublicJwk = parseP256PublicJwk(objectValue(value.rootPublicJwk, 'root JWK'));
  const payloadBytes = base64UrlDecode(value.payloadBase64Url);
  const rootKey = await importP256VerifyingKey(rootPublicJwk);
  if (!(await verifyP1363LowS(rootKey, payloadBytes, base64UrlDecode(value.signatureBase64Url)))) {
    throw new Error('trust manifest signature is invalid');
  }
  const payload = objectValue(JSON.parse(new TextDecoder().decode(payloadBytes)) as unknown, 'trust manifest payload');
  if (
    typeof payload.expiresAtMs !== 'number' ||
    !Number.isSafeInteger(payload.expiresAtMs) ||
    typeof payload.revision !== 'number' ||
    payload.revision !== value.revision ||
    typeof payload.rootJkt !== 'string'
  ) {
    throw new Error('trust manifest payload is invalid');
  }
  const rootJkt = await publicJwkThumbprint(rootPublicJwk);
  if (payload.rootJkt !== rootJkt || payload.expiresAtMs <= now.getTime()) {
    throw new Error('trust manifest root or expiry is invalid');
  }
  const onlinePublicJwk = parseP256PublicJwk(objectValue(payload.onlinePublicJwk, 'online JWK'));
  if ((await publicJwkThumbprint(onlinePublicJwk)) === rootJkt) {
    throw new Error('root and online signing keys must be distinct');
  }
  return {
    expiresAt: new Date(payload.expiresAtMs),
    onlinePublicJwk,
    onlineVerifyingKey: await importP256VerifyingKey(onlinePublicJwk),
    revision: BigInt(value.revision),
    rootJkt,
  };
}

export async function connectGateway(
  identity: EndpointIdentity,
  ticket: WebSocketTicketInput,
  trust: VerifiedTrustManifest,
): Promise<ReadyGatewayConnection> {
  const endpointId = decodeHexId(ticket.endpointId);
  const credentialId = decodeHexId(ticket.credentialId);
  const socket = new WebSocket(ticket.websocketUrl, [protocolName, `mss.ticket.${ticket.ticket}`]);
  socket.binaryType = 'arraybuffer';
  const challengeMessage = nextBinaryMessage(socket);
  await socketOpened(socket);
  if (socket.protocol !== protocolName) {
    socket.close(1002, 'AWP subprotocol mismatch');
    throw new Error('Gateway selected an invalid WebSocket subprotocol');
  }
  try {
    const challengePacket = fromBinary(WirePacketSchema, await challengeMessage);
    if (
      challengePacket.wireMajor !== 1 ||
      challengePacket.packetId.length !== 16 ||
      challengePacket.body.case !== 'serverChallenge'
    ) {
      throw new Error('Gateway challenge packet is invalid');
    }
    const challenge = challengePacket.body.value;
    if (
      challenge.connectionId.length !== 16 ||
      challenge.connectionGeneration <= 0n ||
      challenge.serverNonce.length !== 32 ||
      challenge.serverSignature.length !== 64 ||
      challenge.trustManifestRevision !== trust.revision
    ) {
      throw new Error('Gateway challenge binding is invalid');
    }
    const serverTranscript = buildServerChallengeTranscript({
      connectionGeneration: challenge.connectionGeneration,
      connectionId: challenge.connectionId,
      credentialStatusRevision: challenge.credentialStatusRevision,
      endpointId,
      manifestRevision: challenge.trustManifestRevision,
      serverNonce: challenge.serverNonce,
      serverTimeMs: challenge.serverTimeMs,
    });
    if (!(await verifyP1363LowS(trust.onlineVerifyingKey, serverTranscript, challenge.serverSignature))) {
      throw new Error('Gateway challenge signature is invalid');
    }
    const clientNonce = crypto.getRandomValues(new Uint8Array(32));
    const clientTranscript = buildClientChallengeTranscript({
      clientNonce,
      connectionGeneration: challenge.connectionGeneration,
      connectionId: challenge.connectionId,
      credentialId,
      credentialStatusRevision: challenge.credentialStatusRevision,
      endpointId,
      manifestRevision: challenge.trustManifestRevision,
      serverNonce: challenge.serverNonce,
    });
    const endpointSignature = await signP1363LowS(identity.signing.privateKey, clientTranscript);
    const response = create(WirePacketSchema, {
      body: {
        case: 'challengeResponse',
        value: create(ChallengeResponseSchema, {
          clientNonce,
          connectionGeneration: challenge.connectionGeneration,
          connectionId: challenge.connectionId,
          credentialSerial: credentialId,
          endpointId,
          endpointSignature,
          lastCredentialStatusRevision: challenge.credentialStatusRevision,
          lastManifestRevision: challenge.trustManifestRevision,
        }),
      },
      packetId: crypto.getRandomValues(new Uint8Array(16)),
      wireMajor: 1,
      wireMinor: 0,
    });
    const readyMessage = nextBinaryMessage(socket);
    socket.send(toBinary(WirePacketSchema, response));
    const readyPacket = fromBinary(WirePacketSchema, await readyMessage);
    if (readyPacket.wireMajor !== 1 || readyPacket.packetId.length !== 16 || readyPacket.body.case !== 'connectionReady') {
      throw new Error('Gateway ready packet is invalid');
    }
    const ready = readyPacket.body.value;
    if (
      !equalBytes(ready.connectionId, challenge.connectionId) ||
      ready.connectionGeneration !== challenge.connectionGeneration ||
      ready.fencingToken.length !== 32 ||
      ready.maxPacketBytes <= 0 ||
      ready.maxInflightFrames <= 0 ||
      ready.heartbeatIntervalMs <= 0 ||
      ready.serverSignature.length !== 64
    ) {
      throw new Error('Gateway ready binding is invalid');
    }
    const readyTranscript = buildConnectionReadyTranscript({
      connectionGeneration: ready.connectionGeneration,
      connectionId: ready.connectionId,
      endpointId,
      fencingToken: ready.fencingToken,
      heartbeatIntervalMs: ready.heartbeatIntervalMs,
      maxInflightFrames: ready.maxInflightFrames,
      maxPacketBytes: ready.maxPacketBytes,
      readyAtMs: ready.readyAtMs,
    });
    if (!(await verifyP1363LowS(trust.onlineVerifyingKey, readyTranscript, ready.serverSignature))) {
      throw new Error('Gateway ready signature is invalid');
    }
    return {
      connectionGeneration: ready.connectionGeneration,
      connectionId: ready.connectionId,
      fencingToken: ready.fencingToken,
      heartbeatIntervalMs: ready.heartbeatIntervalMs,
      rootJkt: trust.rootJkt,
      socket,
    };
  } catch (error) {
    socket.close(1008, 'AWP verification failed');
    throw error;
  }
}

interface ServerTranscriptInput {
  readonly connectionGeneration: bigint;
  readonly connectionId: Uint8Array;
  readonly credentialStatusRevision: bigint;
  readonly endpointId: Uint8Array;
  readonly manifestRevision: bigint;
  readonly serverNonce: Uint8Array;
  readonly serverTimeMs: bigint;
}

export function buildServerChallengeTranscript(input: ServerTranscriptInput): Uint8Array {
  return concatenate([
    textEncoder.encode('mss-awp-server-challenge-v1'), input.connectionId,
    uint64(input.connectionGeneration), input.serverNonce, uint64(input.serverTimeMs),
    uint64(input.manifestRevision), uint64(input.credentialStatusRevision), input.endpointId,
  ]);
}

interface ClientTranscriptInput {
  readonly clientNonce: Uint8Array;
  readonly connectionGeneration: bigint;
  readonly connectionId: Uint8Array;
  readonly credentialId: Uint8Array;
  readonly credentialStatusRevision: bigint;
  readonly endpointId: Uint8Array;
  readonly manifestRevision: bigint;
  readonly serverNonce: Uint8Array;
}

export function buildClientChallengeTranscript(input: ClientTranscriptInput): Uint8Array {
  return concatenate([
    textEncoder.encode('mss-awp-client-challenge-v1'), input.connectionId,
    uint64(input.connectionGeneration), input.serverNonce, input.clientNonce,
    input.endpointId, input.credentialId, textEncoder.encode(protocolName),
    uint64(input.manifestRevision), uint64(input.credentialStatusRevision),
  ]);
}

interface ReadyTranscriptInput {
  readonly connectionGeneration: bigint;
  readonly connectionId: Uint8Array;
  readonly endpointId: Uint8Array;
  readonly fencingToken: Uint8Array;
  readonly heartbeatIntervalMs: number;
  readonly maxInflightFrames: number;
  readonly maxPacketBytes: number;
  readonly readyAtMs: bigint;
}

export function buildConnectionReadyTranscript(input: ReadyTranscriptInput): Uint8Array {
  return concatenate([
    textEncoder.encode('mss-awp-connection-ready-v1'), input.connectionId,
    uint64(input.connectionGeneration), input.fencingToken, uint64(input.readyAtMs),
    uint32(input.maxPacketBytes), uint32(input.maxInflightFrames),
    uint32(input.heartbeatIntervalMs), input.endpointId,
  ]);
}

function socketOpened(socket: WebSocket): Promise<void> {
  return new Promise((resolve, reject) => {
    socket.addEventListener('open', () => resolve(), { once: true });
    socket.addEventListener('error', () => reject(new Error('Gateway WebSocket failed to open')), { once: true });
  });
}

function nextBinaryMessage(socket: WebSocket): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const message = async (event: MessageEvent) => {
      cleanup();
      const data = event.data instanceof Blob ? await event.data.arrayBuffer() : event.data;
      if (!(data instanceof ArrayBuffer) || data.byteLength === 0 || data.byteLength > 1_048_576) {
        reject(new Error('Gateway sent an invalid binary packet'));
        return;
      }
      resolve(new Uint8Array(data));
    };
    const closed = () => {
      cleanup();
      reject(new Error('Gateway closed before handshake completed'));
    };
    const cleanup = () => {
      socket.removeEventListener('message', message);
      socket.removeEventListener('close', closed);
    };
    socket.addEventListener('message', message, { once: true });
    socket.addEventListener('close', closed, { once: true });
  });
}

function uint64(value: bigint): Uint8Array {
  if (value < 0n || value > 0xffff_ffff_ffff_ffffn) {
    throw new Error('AWP 64-bit transcript value is invalid');
  }
  const result = new Uint8Array(8);
  new DataView(result.buffer).setBigUint64(0, value, false);
  return result;
}

function uint32(value: number): Uint8Array {
  if (!Number.isSafeInteger(value) || value < 0 || value > 0xffff_ffff) {
    throw new Error('AWP 32-bit transcript value is invalid');
  }
  const result = new Uint8Array(4);
  new DataView(result.buffer).setUint32(0, value, false);
  return result;
}

function decodeHexId(value: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(value)) {
    throw new Error('AWP identifier must be 16-byte lowercase hexadecimal');
  }
  return Uint8Array.from({ length: 16 }, (_, index) => Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
}

function concatenate(values: readonly Uint8Array[]): Uint8Array {
  const result = new Uint8Array(values.reduce((total, value) => total + value.length, 0));
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

function objectValue(input: unknown, label: string): Record<string, unknown> {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) {
    throw new Error(`${label} is invalid`);
  }
  return input as Record<string, unknown>;
}
