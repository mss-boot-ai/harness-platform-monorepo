import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import { buildAckTranscript, sessionChannelId } from './frame';
import { openABAAckFramePacket } from './outbound-ack';
import { createEndpointIdentity } from './identity';
import { signP1363LowS } from './crypto';
import { AckFrameSchema, Direction, WirePacketSchema } from './generated/mss/awp/v1/wire_pb';
const binding = { sessionId: '01'.repeat(16), abaEndpointId: '02'.repeat(16), hcEndpointId: '03'.repeat(16) };
async function example() {
  const identity = await createEndpointIdentity('aba-test', 'ABA', 'web-ephemeral');
  const input = { ackId: new Uint8Array(16).fill(9), sessionId: new Uint8Array(16).fill(1), endpointId: new Uint8Array(16).fill(2),
    channelId: await sessionChannelId(new Uint8Array(16).fill(1), new Uint8Array(16).fill(2), new Uint8Array(16).fill(3)),
    acknowledgedDirection: Direction.HC_TO_ABA, highestContiguousSequence: 2n, keyGeneration: 1n, createdAtMs: 1_790_000_000_000n };
  const packet = create(WirePacketSchema, { wireMajor: 1, wireMinor: 0, packetId: new Uint8Array(16).fill(8),
    body: { case: 'ack', value: create(AckFrameSchema, { ...input, signature: await signP1363LowS(identity.signing.privateKey, buildAckTranscript(input)) }) } });
  return { identity, packet, encoded: toBinary(WirePacketSchema, packet) };
}
describe('verified ABA transport acknowledgments', () => {
  it('accepts a signed cumulative ACK without declaring execution completed', async () => {
    const { identity, encoded } = await example();
    expect(await openABAAckFramePacket(identity.signing.publicJwk, binding, encoded)).toEqual({ highestContiguousSequence: 2n, receivedRanges: [] });
  });
  it('rejects another session, peer, generation, and forged sequence advancement', async () => {
    const { identity, encoded } = await example();
    const stranger = await createEndpointIdentity('other', 'other', 'web-ephemeral');
    await expect(openABAAckFramePacket(identity.signing.publicJwk, { ...binding, sessionId: '04'.repeat(16) }, encoded)).rejects.toThrow('binding');
    await expect(openABAAckFramePacket(stranger.signing.publicJwk, binding, encoded)).rejects.toThrow('signature');
    await expect(openABAAckFramePacket(identity.signing.publicJwk, binding, encoded, 2n)).rejects.toThrow('binding');
    const packet = fromBinary(WirePacketSchema, encoded);
    if (packet.body.case !== 'ack') throw new Error('Expected ACK');
    packet.body.value.highestContiguousSequence = 200n;
    await expect(openABAAckFramePacket(identity.signing.publicJwk, binding, toBinary(WirePacketSchema, packet))).rejects.toThrow('signature');
  });
  it('rejects malformed selective ranges and oversized input', async () => {
    const { identity, packet } = await example();
    if (packet.body.case !== 'ack') throw new Error('Expected ACK');
    packet.body.value.receivedRanges = [{ $typeName: 'mss.awp.v1.SequenceRange', start: 1n, end: 3n }];
    await expect(openABAAckFramePacket(identity.signing.publicJwk, binding, toBinary(WirePacketSchema, packet))).rejects.toThrow('range');
    await expect(openABAAckFramePacket(identity.signing.publicJwk, binding, new Uint8Array(4097))).rejects.toThrow('bounded');
  });
});
