import vector from '../../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json';
import { publicJwkThumbprint, type P256PublicJwk } from './identity';
import {
  accessTokenHash,
  base64UrlDecode,
  importP256VerifyingKey,
  isP1363LowS,
  signP1363LowS,
  verifyP1363LowS,
} from './crypto';

const textEncoder = new TextEncoder();

describe('Suite 0001 shared JWK and ES256 vector', () => {
  it('computes the shared RFC 7638 JKT and verifies only low-S P1363', async () => {
    expect(vector.fixtureUse.startsWith('TEST ONLY')).toBe(true);
    const publicJwk = vector.publicJwk as P256PublicJwk;
    await expect(publicJwkThumbprint(publicJwk)).resolves.toBe(vector.jkt);
    const key = await importP256VerifyingKey(publicJwk);
    const message = textEncoder.encode(vector.signature.messageUtf8);
    const signature = base64UrlDecode(vector.signature.p1363Base64Url);

    expect(isP1363LowS(signature)).toBe(true);
    await expect(verifyP1363LowS(key, message, signature)).resolves.toBe(true);
    await expect(verifyP1363LowS(key, textEncoder.encode('tampered'), signature)).resolves.toBe(false);
    await expect(verifyP1363LowS(key, message, signature.subarray(1))).resolves.toBe(false);

    const highS = base64UrlDecode(vector.signature.highSP1363Base64Url);
    expect(isP1363LowS(highS)).toBe(false);
    await expect(verifyP1363LowS(key, message, highS)).resolves.toBe(false);
  });

  it('normalizes newly generated WebCrypto signatures to low-S', async () => {
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    const publicKey = await importP256VerifyingKey(vector.publicJwk as P256PublicJwk);
    const message = textEncoder.encode(vector.signature.messageUtf8);
    const signature = await signP1363LowS(privateKey, message);

    expect(isP1363LowS(signature)).toBe(true);
    await expect(verifyP1363LowS(publicKey, message, signature)).resolves.toBe(true);
  });

  it('verifies the shared DPoP signing primitive and access-token hash', async () => {
    await expect(accessTokenHash(vector.dpop.accessToken)).resolves.toBe(vector.dpop.ath);
    const parts = vector.dpop.proof.split('.');
    expect(parts).toHaveLength(3);
    expect(parts.slice(0, 2).join('.')).toBe(vector.dpop.signingInput);
    const signature = base64UrlDecode(vector.dpop.signatureP1363Base64Url);
    const publicKey = await importP256VerifyingKey(vector.publicJwk as P256PublicJwk);
    await expect(
      verifyP1363LowS(publicKey, textEncoder.encode(vector.dpop.signingInput), signature),
    ).resolves.toBe(true);
  });
});
