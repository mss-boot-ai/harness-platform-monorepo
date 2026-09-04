import { create, fromBinary, toBinary } from '@bufbuild/protobuf';
import vector from '../../../../protocol/testdata/v1/suite-0001-hpke-session.json';
import { base64UrlDecode, signP1363LowS, verifyP1363LowS } from './crypto';
import {
  ControlFrameSchema,
  ControlType,
  SessionKeyPackageSchema,
  WirePacketSchema,
} from './generated/mss/awp/v1/wire_pb';
import type { EndpointIdentity, P256PublicJwk } from './identity';
import {
  buildControlTranscript,
  buildKeyPackageEnvelopeTranscript,
  createSessionKeyPackageAckPacket,
  openSessionKeyPackagePacket,
} from './session-key-package';

describe('signed SessionKeyPackage packet', () => {
  it('verifies both ABA signatures and opens the shared HPKE package', async () => {
    const abaSigning = await crypto.subtle.generateKey(
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign', 'verify'],
    );
    const abaSigningPublicJwk = await exportPublicJwk(abaSigning.publicKey);
    const identity = await vectorRecipientIdentity();
    const sessionId = hex(vector.context.sessionIdHex);
    const abaEndpointId = hex(vector.context.senderAbaEndpointIdHex);
    const hcEndpointId = hex(vector.context.recipientHcEndpointIdHex);
    const credentialId = new Uint8Array(16).fill(8);
    const keyPackageId = new Uint8Array(16).fill(9);
    const enc = base64UrlDecode(vector.encBase64Url);
    const ciphertext = base64UrlDecode(vector.ciphertextBase64Url);
    const envelope = buildKeyPackageEnvelopeTranscript({
      abaEndpointId,
      ciphertext,
      credentialId,
      enc,
      expiresAtMs: BigInt(vector.material.expiresAtMs),
      generation: 1n,
      hcEndpointId,
      keyPackageId,
      notBeforeMs: BigInt(vector.material.notBeforeMs),
      policyRevision: 1n,
      sessionId,
    });
    const packagePayload = toBinary(SessionKeyPackageSchema, create(SessionKeyPackageSchema, {
      cryptoSuite: 'MSS-AWP-SUITE-0001',
      expiresAtMs: BigInt(vector.material.expiresAtMs),
      hpkeCiphertext: ciphertext,
      hpkeEnc: enc,
      issuerAbaEndpointId: abaEndpointId,
      issuerCredentialId: credentialId,
      issuerSignature: await signP1363LowS(abaSigning.privateKey, envelope),
      keyGeneration: 1n,
      keyPackageId,
      notBeforeMs: BigInt(vector.material.notBeforeMs),
      policyRevision: 1n,
      recipientHcEndpointId: hcEndpointId,
      sessionId,
    }));
    const messageId = new Uint8Array(16).fill(10);
    const createdAtMs = BigInt(vector.material.notBeforeMs);
    const controlTranscript = buildControlTranscript({
      controlSequence: 2n,
      controlType: ControlType.SESSION_KEY_PACKAGE,
      createdAtMs,
      messageId,
      payload: packagePayload,
      receiverEndpointId: hcEndpointId,
      senderEndpointId: abaEndpointId,
    });
    const packet = toBinary(WirePacketSchema, create(WirePacketSchema, {
      body: {
        case: 'control',
        value: create(ControlFrameSchema, {
          controlSequence: 2n,
          createdAtMs,
          messageId,
          payload: packagePayload,
          receiverEndpointId: hcEndpointId,
          senderEndpointId: abaEndpointId,
          signature: await signP1363LowS(abaSigning.privateKey, controlTranscript),
          type: ControlType.SESSION_KEY_PACKAGE,
        }),
      },
      packetId: new Uint8Array(16).fill(11),
      wireMajor: 1,
      wireMinor: 0,
    }));
    const opened = await openSessionKeyPackagePacket(packet, {
      abaEndpointId: vector.context.senderAbaEndpointIdHex,
      abaSigningPublicJwk,
      hcEndpointId: vector.context.recipientHcEndpointIdHex,
      identity,
      sessionId: vector.context.sessionIdHex,
    }, new Date(vector.material.notBeforeMs));
    expect(opened).not.toBeNull();
    expect(base64Url(opened?.hcToAbaKey ?? new Uint8Array())).toBe(
      vector.directionKeys.hcToAbaBase64Url,
    );
    expect(base64Url(opened?.abaToHcKey ?? new Uint8Array())).toBe(
      vector.directionKeys.abaToHcBase64Url,
    );
    if (opened === null) {
      throw new Error('SessionKeyPackage did not open');
    }
    const acknowledgment = await createSessionKeyPackageAckPacket(identity, {
      abaEndpointId: vector.context.senderAbaEndpointIdHex,
      controlSequence: 1n,
      hcEndpointId: vector.context.recipientHcEndpointIdHex,
      keyPackageId: opened.keyPackageId,
      sessionId: vector.context.sessionIdHex,
    }, new Date(vector.material.notBeforeMs));
    const ackPacket = fromBinary(WirePacketSchema, acknowledgment);
    expect(ackPacket.body.case).toBe('control');
    if (ackPacket.body.case !== 'control') {
      throw new Error('acknowledgment is not a control packet');
    }
    expect(ackPacket.body.value.type).toBe(ControlType.SESSION_KEY_PACKAGE_ACK);
    expect(await verifyP1363LowS(
      identity.signing.publicKey,
      buildControlTranscript({
        controlSequence: ackPacket.body.value.controlSequence,
        controlType: ackPacket.body.value.type,
        createdAtMs: ackPacket.body.value.createdAtMs,
        messageId: ackPacket.body.value.messageId,
        payload: ackPacket.body.value.payload,
        receiverEndpointId: ackPacket.body.value.receiverEndpointId,
        senderEndpointId: ackPacket.body.value.senderEndpointId,
      }),
      ackPacket.body.value.signature,
    )).toBe(true);
  });
});

async function vectorRecipientIdentity(): Promise<EndpointIdentity> {
  const publicJwk = vector.recipient.publicJwk as P256PublicJwk;
  const privateKey = await crypto.subtle.importKey(
    'jwk',
    { ...publicJwk, d: vector.recipient.privateD },
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    ['deriveBits'],
  );
  const publicKey = await crypto.subtle.importKey(
    'jwk',
    publicJwk,
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    [],
  );
  const signing = await crypto.subtle.generateKey(
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['sign', 'verify'],
  );
  const signingPublicJwk = await exportPublicJwk(signing.publicKey);
  return {
    assurance: 'web-software',
    createdAt: new Date().toISOString(),
    installationId: 'test',
    kem: { privateKey, publicJwk, publicKey, thumbprint: 'test' },
    label: 'test',
    signing: {
      privateKey: signing.privateKey,
      publicJwk: signingPublicJwk,
      publicKey: signing.publicKey,
      thumbprint: 'test-signing',
    },
  };
}

async function exportPublicJwk(key: CryptoKey): Promise<P256PublicJwk> {
  const jwk = await crypto.subtle.exportKey('jwk', key);
  if (jwk.crv !== 'P-256' || jwk.kty !== 'EC' || typeof jwk.x !== 'string' || typeof jwk.y !== 'string') {
    throw new Error('test signing key is invalid');
  }
  return { crv: 'P-256', kty: 'EC', x: jwk.x, y: jwk.y };
}

function hex(value: string): Uint8Array {
  return Uint8Array.from({ length: value.length / 2 }, (_, index) =>
    Number.parseInt(value.slice(index * 2, index * 2 + 2), 16));
}

function base64Url(value: Uint8Array): string {
  let binary = '';
  for (const byte of value) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/u, '');
}
