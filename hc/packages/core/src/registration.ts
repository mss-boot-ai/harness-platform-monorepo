import { base64UrlDecode, base64UrlEncode, signP1363LowS } from './crypto';
import type { EndpointIdentity } from './identity';

const transcriptDomain = new TextEncoder().encode('MSS-HC-REGISTER-V1\0');
const textEncoder = new TextEncoder();

export interface RegistrationTranscriptInput {
  readonly assurance: 'web-ephemeral' | 'web-software';
  readonly challenge: string;
  readonly challengeId: string;
  readonly endpointName: string;
  readonly kemJkt: string;
  readonly origin: string;
  readonly signingJkt: string;
  readonly softwareVersion: string;
}

export function buildRegistrationTranscript(input: RegistrationTranscriptInput): Uint8Array {
  const challengeId = decodeHexId(input.challengeId);
  const challenge = base64UrlDecode(input.challenge);
  const signingJkt = base64UrlDecode(input.signingJkt);
  const kemJkt = base64UrlDecode(input.kemJkt);
  if (challenge.length !== 32 || signingJkt.length !== 32 || kemJkt.length !== 32) {
    throw new Error('HC registration challenge and JKT values must be 32 bytes');
  }
  if (input.signingJkt === input.kemJkt) {
    throw new Error('HC registration signing and KEM JKT must be distinct');
  }
  const assurance = input.assurance === 'web-ephemeral' ? 1 : input.assurance === 'web-software' ? 2 : 0;
  if (assurance === 0) {
    throw new Error('HC registration assurance is unsupported');
  }
  const origin = encodeText(normalizeRegistrationOrigin(input.origin), 512, 'origin');
  const endpointName = encodeText(input.endpointName.trim(), 120, 'endpoint name');
  const softwareVersion = encodeText(input.softwareVersion.trim(), 64, 'software version');
  return concatenate([
    transcriptDomain,
    challengeId,
    challenge,
    signingJkt,
    kemJkt,
    withLength(origin),
    withLength(endpointName),
    Uint8Array.of(assurance),
    withLength(softwareVersion),
  ]);
}

export async function createRegistrationProof(
  identity: EndpointIdentity,
  input: Omit<RegistrationTranscriptInput, 'assurance' | 'kemJkt' | 'signingJkt'>,
): Promise<string> {
  const transcript = buildRegistrationTranscript({
    ...input,
    assurance: identity.assurance,
    kemJkt: identity.kem.thumbprint,
    signingJkt: identity.signing.thumbprint,
  });
  return base64UrlEncode(await signP1363LowS(identity.signing.privateKey, transcript));
}

export function normalizeRegistrationOrigin(value: string): string {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error('HC registration origin must be absolute');
  }
  if (
    parsed.username !== '' ||
    parsed.password !== '' ||
    (parsed.pathname !== '' && parsed.pathname !== '/') ||
    parsed.search !== '' ||
    parsed.hash !== ''
  ) {
    throw new Error('HC registration origin must contain only scheme and authority');
  }
  const hostname = parsed.hostname.toLowerCase();
  const loopback = hostname === 'localhost' || hostname === '127.0.0.1' || hostname === '[::1]';
  if (parsed.protocol !== 'https:' && !(parsed.protocol === 'http:' && loopback)) {
    throw new Error('HC registration origin must use HTTPS except for loopback development');
  }
  return parsed.origin;
}

function decodeHexId(value: string): Uint8Array {
  if (!/^[0-9a-fA-F]{32}$/u.test(value)) {
    throw new Error('HC registration challenge ID must be 16-byte hexadecimal');
  }
  const result = new Uint8Array(16);
  for (let index = 0; index < result.length; index += 1) {
    result[index] = Number.parseInt(value.slice(index * 2, index * 2 + 2), 16);
  }
  return result;
}

function encodeText(value: string, maxBytes: number, label: string): Uint8Array {
  const encoded = textEncoder.encode(value);
  if (encoded.length === 0 || encoded.length > maxBytes || encoded.length > 0xffff) {
    throw new Error(`HC registration ${label} is invalid`);
  }
  return encoded;
}

function withLength(value: Uint8Array): Uint8Array {
  const prefix = new Uint8Array(2);
  new DataView(prefix.buffer).setUint16(0, value.length, false);
  return concatenate([prefix, value]);
}

function concatenate(values: readonly Uint8Array[]): Uint8Array {
  const length = values.reduce((total, value) => total + value.length, 0);
  const result = new Uint8Array(length);
  let offset = 0;
  for (const value of values) {
    result.set(value, offset);
    offset += value.length;
  }
  return result;
}
