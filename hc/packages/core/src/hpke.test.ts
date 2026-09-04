import vector from '../../../../protocol/testdata/v1/suite-0001-hpke-session.json';
import { base64UrlDecode } from './crypto';
import {
  buildKeyPackageInfo,
  deriveSessionDirectionKeys,
  frameNonce,
  openKeyPackage,
  type KeyPackageContext,
} from './hpke';
import type { P256PublicJwk } from './identity';

describe('Suite 0001 HPKE and session KDF vector', () => {
  it('opens the Rust-generated package with WebCrypto and derives matching directions', async () => {
    expect(vector.fixtureUse.startsWith('TEST ONLY')).toBe(true);
    expect(vector.hpke).toEqual({ aeadId: 2, kdfId: 1, kemId: 16 });
    const publicJwk = vector.recipient.publicJwk as P256PublicJwk;
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      { ...publicJwk, d: vector.recipient.privateD },
      { name: 'ECDH', namedCurve: 'P-256' },
      false,
      ['deriveBits'],
    );
    const context = vectorContext();
    const info = buildKeyPackageInfo(context);
    expect(base64Url(info)).toBe(vector.infoBase64Url);
    expect(base64Url(new Uint8Array(await crypto.subtle.digest('SHA-256', new Uint8Array(info).buffer)))).toBe(
      vector.contextHashBase64Url,
    );

    const material = await openKeyPackage(
      privateKey,
      publicJwk,
      context,
      base64UrlDecode(vector.encBase64Url),
      base64UrlDecode(vector.ciphertextBase64Url),
    );
    expect(base64Url(material.srk)).toBe(vector.material.srkBase64Url);
    expect(base64Url(material.keyId)).toBe(vector.material.keyIdBase64Url);
    expect(base64Url(material.sessionNonce)).toBe(vector.material.sessionNonceBase64Url);
    expect(base64Url(material.hcToAbaNoncePrefix)).toBe(
      vector.material.hcToAbaNoncePrefixBase64Url,
    );
    expect(base64Url(material.abaToHcNoncePrefix)).toBe(
      vector.material.abaToHcNoncePrefixBase64Url,
    );
    expect(material.notBeforeMs).toBe(BigInt(vector.material.notBeforeMs));
    expect(material.expiresAtMs).toBe(BigInt(vector.material.expiresAtMs));

    const keys = await deriveSessionDirectionKeys(material, context.recipientHcEndpointId);
    expect(base64Url(keys.hcToAba)).toBe(vector.directionKeys.hcToAbaBase64Url);
    expect(base64Url(keys.abaToHc)).toBe(vector.directionKeys.abaToHcBase64Url);
    expect(base64Url(frameNonce(material.hcToAbaNoncePrefix, 1n))).toBe(
      vector.nonces.hcToAbaSequence1Base64Url,
    );
    expect(base64Url(frameNonce(material.abaToHcNoncePrefix, 1n))).toBe(
      vector.nonces.abaToHcSequence1Base64Url,
    );
  });

  it('rejects a modified ciphertext', async () => {
    const publicJwk = vector.recipient.publicJwk as P256PublicJwk;
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      { ...publicJwk, d: vector.recipient.privateD },
      { name: 'ECDH', namedCurve: 'P-256' },
      false,
      ['deriveBits'],
    );
    const ciphertext = base64UrlDecode(vector.ciphertextBase64Url);
    ciphertext[0] = (ciphertext[0] ?? 0) ^ 1;
    await expect(openKeyPackage(
      privateKey,
      publicJwk,
      vectorContext(),
      base64UrlDecode(vector.encBase64Url),
      ciphertext,
    )).rejects.toThrow('authentication failed');
  });
});

function vectorContext(): KeyPackageContext {
  return {
    generation: BigInt(vector.context.generation),
    policyRevision: BigInt(vector.context.policyRevision),
    recipientHcEndpointId: hex(vector.context.recipientHcEndpointIdHex),
    senderAbaEndpointId: hex(vector.context.senderAbaEndpointIdHex),
    sessionId: hex(vector.context.sessionIdHex),
  };
}

function hex(value: string): Uint8Array {
  if (!/^[0-9a-f]+$/u.test(value) || value.length % 2 !== 0) {
    throw new Error('test vector hex is invalid');
  }
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
