// Deterministic protocol test helpers, not a production entry point or model integration.
import { createWireMessage as create, encodeWireMessage as toBinary } from '@harness/hc-core';
import { IDBFactory } from 'fake-indexeddb';
import { AckFrameSchema, buildAckTranscript, buildFrameAAD, createEndpointIdentity, deriveSessionDirectionKeys, Direction,
  EncryptedFrameSchema, EncryptedLocalVault, FrameType, sessionChannelId, signP1363LowS, WirePacketSchema,
  type EndpointIdentity, type OpenedSessionKeyPackage, type SessionKeyMaterial } from '@harness/hc-core';
import { ConversationController, type ConversationTransport } from './conversation-controller';
import { ConversationStore, idBytes, newConversation, type Conversation } from './conversation-store';
import { newRuntimeState, readDescriptor } from './runtime-state';
import type { ABAEndpointSummary, EndpointSessionSummary } from '../api';
export const testEndpoint = '03'.repeat(16);
export const testNow = 1_790_000_000_000;
export const descriptor = (sessionId: string) => ({ initialize: { protocolVersion: 1, agentInfo: { name: 'Fixture Agent' } },
  session: { sessionId, configOptions: [{ id: 'model', name: 'Model', category: 'model', type: 'select', currentValue: 'a', options: [{ value: 'a', name: 'A' }, { value: 'b', name: 'B' }] }] },
  bridge: { protocolVersion: 1, duplex: true, turnCancellation: true, processEpoch: 'fixture-process-1' } });
export async function controllerFixture(options: { readonly sessionId?: string; readonly abaId?: string; readonly workspaceId?: string; readonly identity?: EndpointIdentity; readonly transport?: ConversationTransport } = {}) {
  const identity = options.identity ?? await createEndpointIdentity('browser', 'Browser', 'web-software');
  const peer = await createEndpointIdentity('aba', 'ABA', 'web-software');
  const session: EndpointSessionSummary = { sessionId: options.sessionId ?? '01'.repeat(16), hcEndpointId: testEndpoint, abaEndpointId: options.abaId ?? '02'.repeat(16),
    status: 'ACTIVE', createdAt: new Date(testNow).toISOString(), requestedCapabilities: ['remote-session-v1'], runtimeProfileId: 'fixture', workspaceId: options.workspaceId ?? 'fixture' };
  const aba: ABAEndpointSummary = { id: session.abaEndpointId, name: 'Test device', type: 'ABA', status: 'ACTIVE', signingPublicJwk: peer.signing.publicJwk, signingJkt: peer.signing.thumbprint };
  const material: SessionKeyMaterial = { sessionId: idBytes(session.sessionId), generation: 1n, keyId: crypto.getRandomValues(new Uint8Array(16)), srk: crypto.getRandomValues(new Uint8Array(32)),
    sessionNonce: crypto.getRandomValues(new Uint8Array(32)), hcToAbaNoncePrefix: new Uint8Array(4).fill(5), abaToHcNoncePrefix: new Uint8Array(4).fill(6), notBeforeMs: BigInt(testNow - 1000), expiresAtMs: BigInt(testNow + 3_600_000) };
  const directions = await deriveSessionDirectionKeys(material, idBytes(testEndpoint));
  const keys: OpenedSessionKeyPackage = { material, hcToAbaKey: directions.hcToAba, abaToHcKey: directions.abaToHc, controlSequence: 2n, keyPackageId: new Uint8Array(16).fill(8) };
  const value: Conversation = { ...newConversation(session, aba, identity, 'a draft'), keys, runtime: readDescriptor(newRuntimeState(), descriptor(session.sessionId), session.sessionId) };
  const vault = new EncryptedLocalVault(new IDBFactory(), `controller-${crypto.randomUUID()}`);
  const store = new ConversationStore(vault, testEndpoint, identity); const revision = await store.write(value, null);
  const sent: Uint8Array[] = []; const controls: Uint8Array[] = []; let controlSequence = 0n;
  const transport: ConversationTransport = options.transport ?? { now: () => testNow, send: (bytes) => { sent.push(bytes.slice()); return true; },
    control: async (build) => { controls.push(await build(++controlSequence)); } };
  const controller = new ConversationController({ revision, value }, store, identity, transport, () => undefined);
  const binding = { sessionId: session.sessionId, abaEndpointId: aba.id, hcEndpointId: testEndpoint };
  const channel = await sessionChannelId(idBytes(session.sessionId), idBytes(aba.id), idBytes(testEndpoint));
  const incoming = async (payload: unknown, sequence: bigint): Promise<Uint8Array> => {
    const plaintext = new TextEncoder().encode(JSON.stringify(payload)); const messageId = crypto.getRandomValues(new Uint8Array(16));
    const aad = buildFrameAAD({ sessionId: idBytes(session.sessionId), channelId: channel, messageId, keyGeneration: 1n, keyId: material.keyId,
      senderEndpointId: idBytes(aba.id), receiverEndpointId: idBytes(testEndpoint), direction: Direction.ABA_TO_HC, flags: 0,
      createdAtMs: BigInt(testNow), sequence, ciphertextLength: plaintext.length + 16 });
    const key = await crypto.subtle.importKey('raw', new Uint8Array(keys.abaToHcKey).buffer, 'AES-GCM', false, ['encrypt']);
    const nonce = new Uint8Array(12); nonce.set(material.abaToHcNoncePrefix); new DataView(nonce.buffer).setBigUint64(4, sequence, false);
    const ciphertext = new Uint8Array(await crypto.subtle.encrypt({ name: 'AES-GCM', iv: nonce.buffer, additionalData: new Uint8Array(aad).buffer, tagLength: 128 }, key, plaintext));
    const prefix = new TextEncoder().encode('mss-awp-frame-signature-v1'); const transcript = new Uint8Array(prefix.length + aad.length + ciphertext.length);
    transcript.set(prefix); transcript.set(aad, prefix.length); transcript.set(ciphertext, prefix.length + aad.length);
    return toBinary(WirePacketSchema, create(WirePacketSchema, { wireMajor: 1, wireMinor: 0, packetId: crypto.getRandomValues(new Uint8Array(16)), body: { case: 'encrypted', value: create(EncryptedFrameSchema, {
      cryptoSuiteId: 1, frameType: FrameType.ACP_TRANSPORT_FRAME, flags: 0, messageId, channelId: channel, sessionId: idBytes(session.sessionId),
      senderEndpointId: idBytes(aba.id), receiverEndpointId: idBytes(testEndpoint), direction: Direction.ABA_TO_HC, sequence, keyGeneration: 1n, keyId: material.keyId,
      createdAtMs: BigInt(testNow), ciphertext, signature: await signP1363LowS(peer.signing.privateKey, transcript) }) } }));
  };
  const ack = async (highest: bigint): Promise<Uint8Array> => {
    const payload = { ackId: crypto.getRandomValues(new Uint8Array(16)), channelId: channel, sessionId: idBytes(session.sessionId), endpointId: idBytes(aba.id),
      acknowledgedDirection: Direction.HC_TO_ABA, highestContiguousSequence: highest, keyGeneration: 1n, createdAtMs: BigInt(testNow) };
    return toBinary(WirePacketSchema, create(WirePacketSchema, { wireMajor: 1, wireMinor: 0, packetId: crypto.getRandomValues(new Uint8Array(16)),
      body: { case: 'ack', value: create(AckFrameSchema, { ...payload, signature: await signP1363LowS(peer.signing.privateKey, buildAckTranscript(payload)) }) } }));
  };
  return { controller, store, vault, identity, peer, value, keys, session, sent, controls, transport, incoming, ack, binding };
}
