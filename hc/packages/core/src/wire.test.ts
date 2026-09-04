import { fromBinary, toBinary } from '@bufbuild/protobuf';
import vector from '../../../../protocol/testdata/v1/wire-server-challenge.json';
import { WirePacketSchema } from './generated/mss/awp/v1/wire_pb';

describe('generated TypeScript AWP binding', () => {
  it('matches the shared deterministic ServerChallenge packet', () => {
    const bytes = Uint8Array.from(atob(vector.wirePacketBase64), (value) => value.charCodeAt(0));
    const packet = fromBinary(WirePacketSchema, bytes);

    expect(packet.wireMajor).toBe(vector.wireMajor);
    expect(packet.wireMinor).toBe(vector.wireMinor);
    expect([...packet.packetId]).toEqual(Array(16).fill(vector.packetIdByte));
    expect(packet.body.case).toBe('serverChallenge');
    if (packet.body.case !== 'serverChallenge') {
      throw new Error('packet is not a ServerChallenge');
    }
    expect([...packet.body.value.connectionId]).toEqual(Array(16).fill(vector.connectionIdByte));
    expect(packet.body.value.connectionGeneration).toBe(BigInt(vector.connectionGeneration));
    expect([...packet.body.value.serverNonce]).toEqual(Array(32).fill(vector.serverNonceByte));
    expect(packet.body.value.serverTimeMs).toBe(BigInt(vector.serverTimeMs));
    expect(packet.body.value.trustManifestRevision).toBe(BigInt(vector.trustManifestRevision));
    expect(packet.body.value.credentialStatusRevision).toBe(BigInt(vector.credentialStatusRevision));
    expect([...packet.body.value.serverSignature]).toEqual(Array(64).fill(vector.serverSignatureByte));
    expect(toBinary(WirePacketSchema, packet)).toEqual(bytes);
  });
});
