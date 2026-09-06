import vector from '../../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json';
import { base64UrlDecode, base64UrlEncode, importP256VerifyingKey, verifyP1363LowS } from './crypto';
import { createEndpointIdentity, type P256PublicJwk } from './identity';
import {
  buildRegistrationTranscript,
  createRegistrationProof,
  normalizeRegistrationOrigin,
} from './registration';

describe('HC registration transcript', () => {
  it('encodes all security bindings and produces a verifiable low-S proof', async () => {
    const identity = await createEndpointIdentity('test', 'Test browser', 'web-software');
    const signingPrivateKey = await crypto.subtle.importKey(
      'jwk',
      vector.privateJwk as JsonWebKey,
      { name: 'ECDSA', namedCurve: 'P-256' },
      false,
      ['sign'],
    );
    const fixedIdentity = {
      ...identity,
      signing: {
        ...identity.signing,
        privateKey: signingPrivateKey,
        publicJwk: vector.publicJwk as P256PublicJwk,
        publicKey: await importP256VerifyingKey(vector.publicJwk as P256PublicJwk),
        thumbprint: vector.jkt,
      },
    };
    const challenge = base64UrlEncode(Uint8Array.from({ length: 32 }, (_, index) => index));
    const input = {
      challenge,
      challengeId: '00112233445566778899aabbccddeeff',
      endpointName: 'H5 本地浏览器',
      origin: 'http://127.0.0.1:4173',
      softwareVersion: '0.1.0',
    };
    const proof = await createRegistrationProof(fixedIdentity, input);
    const transcript = buildRegistrationTranscript({
      ...input,
      assurance: fixedIdentity.assurance,
      kemJkt: fixedIdentity.kem.thumbprint,
      signingJkt: fixedIdentity.signing.thumbprint,
    });

    expect(new TextDecoder().decode(transcript.subarray(0, 19))).toBe('MSS-HC-REGISTER-V1\0');
    await expect(
      verifyP1363LowS(fixedIdentity.signing.publicKey, transcript, base64UrlDecode(proof)),
    ).resolves.toBe(true);
  });

  it('normalizes only safe origins and rejects ambiguous input', () => {
    expect(normalizeRegistrationOrigin('HTTPS://HC.Example:443/')).toBe('https://hc.example');
    expect(normalizeRegistrationOrigin('http://localhost:4173')).toBe('http://localhost:4173');
    expect(() => normalizeRegistrationOrigin('http://hc.example')).toThrow('HTTPS');
    expect(() => normalizeRegistrationOrigin('https://hc.example/path')).toThrow('only scheme');
  });
});
