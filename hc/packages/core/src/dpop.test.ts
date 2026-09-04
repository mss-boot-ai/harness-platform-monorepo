import vector from '../../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json';
import { base64UrlDecode, importP256VerifyingKey, verifyP1363LowS } from './crypto';
import { createDpopProof, decodeDpopSegment, normalizeHtu } from './dpop';
import type { P256PublicJwk } from './identity';

describe('DPoP proof creation', () => {
  it('binds a fresh low-S proof to method, normalized URI, token, nonce, and JWK', async () => {
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    const result = await createDpopProof({
      accessToken: vector.dpop.accessToken,
      htm: 'post',
      htu: `${vector.dpop.claims.htu}?ticket=must-not-be-signed#fragment`,
      issuedAt: new Date(vector.dpop.claims.iat * 1000),
      jti: vector.dpop.claims.jti,
      nonce: vector.dpop.claims.nonce,
      privateKey,
      publicJwk: vector.publicJwk as P256PublicJwk,
    });

    const parts = result.proof.split('.');
    expect(parts).toHaveLength(3);
    expect(result.htm).toBe('POST');
    expect(result.htu).toBe(vector.dpop.claims.htu);
    expect(result.ath).toBe(vector.dpop.ath);
    expect(decodeDpopSegment(parts[0]!)).toEqual(vector.dpop.header);
    expect(decodeDpopSegment(parts[1]!)).toEqual(vector.dpop.claims);

    const verifyingKey = await importP256VerifyingKey(vector.publicJwk as P256PublicJwk);
    await expect(
      verifyP1363LowS(
        verifyingKey,
        new TextEncoder().encode(parts.slice(0, 2).join('.')),
        base64UrlDecode(parts[2]!),
      ),
    ).resolves.toBe(true);
  });

  it('creates a new JTI for each request', async () => {
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    const input = {
      accessToken: vector.dpop.accessToken,
      htm: 'POST',
      htu: vector.dpop.claims.htu,
      nonce: vector.dpop.claims.nonce,
      privateKey,
      publicJwk: vector.publicJwk as P256PublicJwk,
    };
    const first = await createDpopProof(input);
    const second = await createDpopProof(input);
    expect(first.jti).not.toBe(second.jti);
    expect(first.proof).not.toBe(second.proof);
  });

  it('rejects plaintext non-loopback and missing nonce', async () => {
    expect(() => normalizeHtu('http://platform.example/gateway')).toThrow('HTTPS');
    expect(normalizeHtu('http://127.0.0.1:8082/gateway?ignored=yes')).toBe(
      'http://127.0.0.1:8082/gateway',
    );

    const privateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    await expect(
      createDpopProof({
        accessToken: vector.dpop.accessToken,
        htm: 'POST',
        htu: vector.dpop.claims.htu,
        nonce: '',
        privateKey,
        publicJwk: vector.publicJwk as P256PublicJwk,
      }),
    ).rejects.toThrow('nonce');
  });

  it('creates a token-endpoint proof without ath when the credential is HttpOnly', async () => {
    const privateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    const result = await createDpopProof({
      htm: 'POST',
      htu: 'https://platform.example/gateway/v1/tokens/refresh',
      nonce: vector.dpop.claims.nonce,
      privateKey,
      publicJwk: vector.publicJwk as P256PublicJwk,
    });
    const payload = decodeDpopSegment<Record<string, unknown>>(result.proof.split('.')[1]!);
    expect(Object.hasOwn(payload, 'ath')).toBe(false);
  });
});
