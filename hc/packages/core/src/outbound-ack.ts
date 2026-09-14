import { fromBinary } from '@bufbuild/protobuf';
import { importP256VerifyingKey, verifyP1363LowS } from './crypto';
import { buildAckTranscript, sessionChannelId } from './frame';
import { Direction, WirePacketSchema } from './generated/mss/awp/v1/wire_pb';
import type { P256PublicJwk } from './identity';
function equalBytes(a: Uint8Array, b: Uint8Array): boolean { return a.length === b.length && a.every((byte, index) => byte === b[index]); }
function decodeId(value: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(value) || /^0+$/u.test(value)) throw new Error('ACK identifier is invalid');
  return Uint8Array.from(value.match(/../gu) ?? [], (byte) => Number.parseInt(byte, 16));
}
export interface OpenedABAAcknowledgment {
  readonly highestContiguousSequence: bigint;
  readonly receivedRanges: readonly { readonly start: bigint; readonly end: bigint }[];
}
/** A transport ACK is trusted only after peer/signature/channel verification, not merely routing. */
export async function openABAAckFramePacket(
  abaSigningPublicJwk: P256PublicJwk,
  binding: { readonly sessionId: string; readonly abaEndpointId: string; readonly hcEndpointId: string },
  encoded: Uint8Array,
  keyGeneration = 1n,
): Promise<OpenedABAAcknowledgment | null> {
  if (encoded.length > 4096) throw new Error('ACK exceeds bounded size');
  const packet = fromBinary(WirePacketSchema, encoded);
  if (packet.body.case !== 'ack') return null;
  if (packet.wireMajor !== 1 || packet.wireMinor !== 0 || packet.packetId.length !== 16) throw new Error('ACK packet is invalid');
  const ack = packet.body.value;
  const sessionId = decodeId(binding.sessionId);
  const abaId = decodeId(binding.abaEndpointId);
  const hcId = decodeId(binding.hcEndpointId);
  const channel = await sessionChannelId(sessionId, abaId, hcId);
  if (!equalBytes(ack.sessionId, sessionId) || !equalBytes(ack.endpointId, abaId) ||
      !equalBytes(ack.channelId, channel) || ack.acknowledgedDirection !== Direction.HC_TO_ABA ||
      ack.keyGeneration !== keyGeneration || ack.signature.length !== 64) throw new Error('ACK binding is invalid');
  const transcript = buildAckTranscript(ack);
  const key = await importP256VerifyingKey(abaSigningPublicJwk);
  if (!(await verifyP1363LowS(key, transcript, ack.signature))) throw new Error('ACK signature is invalid');
  return { highestContiguousSequence: ack.highestContiguousSequence,
    receivedRanges: ack.receivedRanges.map(({ start, end }) => ({ start, end })) };
}
