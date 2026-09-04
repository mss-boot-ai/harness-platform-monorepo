export type AssuranceLevel = 'web-ephemeral' | 'web-software';

export interface P256PublicJwk {
  readonly crv: 'P-256';
  readonly kty: 'EC';
  readonly x: string;
  readonly y: string;
}

export interface EndpointKeyMaterial {
  readonly privateKey: CryptoKey;
  readonly publicJwk: P256PublicJwk;
  readonly publicKey: CryptoKey;
  readonly thumbprint: string;
}

export interface EndpointIdentity {
  readonly assurance: AssuranceLevel;
  readonly createdAt: string;
  readonly installationId: string;
  readonly kem: EndpointKeyMaterial;
  readonly label: string;
  readonly signing: EndpointKeyMaterial;
}

const textEncoder = new TextEncoder();

function requireWebCrypto(): SubtleCrypto {
  if (globalThis.crypto?.subtle === undefined) {
    throw new Error('WebCrypto is unavailable');
  }
  return globalThis.crypto.subtle;
}

function bytesToBase64Url(bytes: Uint8Array): string {
  let binary = '';
  for (const byte of bytes) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary).replaceAll('+', '-').replaceAll('/', '_').replace(/=+$/u, '');
}

export function parseP256PublicJwk(jwk: JsonWebKey): P256PublicJwk {
  if (
    jwk.kty !== 'EC' ||
    jwk.crv !== 'P-256' ||
    typeof jwk.x !== 'string' ||
    jwk.x.length === 0 ||
    typeof jwk.y !== 'string' ||
    jwk.y.length === 0 ||
    jwk.d !== undefined
  ) {
    throw new Error('public key is not a valid P-256 JWK');
  }

  return Object.freeze({ crv: 'P-256', kty: 'EC', x: jwk.x, y: jwk.y });
}

export async function publicJwkThumbprint(jwk: P256PublicJwk): Promise<string> {
  const canonical = JSON.stringify({ crv: jwk.crv, kty: jwk.kty, x: jwk.x, y: jwk.y });
  const digest = await requireWebCrypto().digest('SHA-256', textEncoder.encode(canonical));
  return bytesToBase64Url(new Uint8Array(digest));
}

async function describePair(pair: CryptoKeyPair): Promise<EndpointKeyMaterial> {
  if (pair.privateKey.extractable) {
    throw new Error('private key unexpectedly became extractable');
  }
  const publicJwk = parseP256PublicJwk(await requireWebCrypto().exportKey('jwk', pair.publicKey));
  return {
    privateKey: pair.privateKey,
    publicJwk,
    publicKey: pair.publicKey,
    thumbprint: await publicJwkThumbprint(publicJwk),
  };
}

export async function createEndpointIdentity(
  installationId: string,
  label: string,
  assurance: AssuranceLevel,
): Promise<EndpointIdentity> {
  const subtle = requireWebCrypto();
  const signingPair = (await subtle.generateKey(
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['sign', 'verify'],
  )) as CryptoKeyPair;
  const kemPair = (await subtle.generateKey(
    { name: 'ECDH', namedCurve: 'P-256' },
    false,
    ['deriveBits'],
  )) as CryptoKeyPair;

  return {
    assurance,
    createdAt: new Date().toISOString(),
    installationId,
    kem: await describePair(kemPair),
    label,
    signing: await describePair(signingPair),
  };
}

export async function assertIdentityUsable(identity: EndpointIdentity): Promise<void> {
  const subtle = requireWebCrypto();
  if (identity.signing.privateKey.extractable || identity.kem.privateKey.extractable) {
    throw new Error('stored private key is extractable');
  }

  const message = textEncoder.encode('harness-hc-key-probe-v1');
  const signature = await subtle.sign(
    { hash: 'SHA-256', name: 'ECDSA' },
    identity.signing.privateKey,
    message,
  );
  const valid = await subtle.verify(
    { hash: 'SHA-256', name: 'ECDSA' },
    identity.signing.publicKey,
    signature,
    message,
  );
  if (!valid) {
    throw new Error('stored signing key pair failed verification');
  }

  const bits = await subtle.deriveBits(
    { name: 'ECDH', public: identity.kem.publicKey },
    identity.kem.privateKey,
    256,
  );
  if (bits.byteLength !== 32) {
    throw new Error('stored KEM key pair failed derivation');
  }
}
