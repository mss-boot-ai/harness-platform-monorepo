import { assertIdentityUsable, createEndpointIdentity, publicJwkThumbprint } from './identity';

describe('endpoint identity', () => {
  it('creates separate non-extractable signing and KEM keys', async () => {
    const identity = await createEndpointIdentity('test-installation', 'Test browser', 'web-ephemeral');

    expect(identity.signing.privateKey.extractable).toBe(false);
    expect(identity.kem.privateKey.extractable).toBe(false);
    expect(identity.signing.publicJwk).not.toEqual(identity.kem.publicJwk);
    expect(identity.signing.thumbprint).toHaveLength(43);
    expect(identity.kem.thumbprint).toHaveLength(43);
    await expect(crypto.subtle.exportKey('jwk', identity.signing.privateKey)).rejects.toThrow();
    await assertIdentityUsable(identity);
  });

  it('computes a stable RFC 7638 thumbprint independent of object insertion order', async () => {
    const first = { crv: 'P-256', kty: 'EC', x: 'example-x', y: 'example-y' } as const;
    const second = { y: 'example-y', x: 'example-x', kty: 'EC', crv: 'P-256' } as const;

    await expect(publicJwkThumbprint(first)).resolves.toBe(await publicJwkThumbprint(second));
  });
});
