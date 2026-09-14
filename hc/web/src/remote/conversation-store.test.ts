import { IDBFactory } from 'fake-indexeddb';
import { createEndpointIdentity, deriveSessionDirectionKeys, EncryptedLocalVault, type OpenedSessionKeyPackage, type SessionKeyMaterial } from '@harness/hc-core';
import { ConversationStore, idBytes, newConversation, sequence } from './conversation-store';
import { startTurn } from '../chat/model';
import type { ABAEndpointSummary, EndpointSessionSummary } from '../api';
const endpoint = '03'.repeat(16);
async function fixture() {
  const identity = await createEndpointIdentity('conversation-browser', 'Browser', 'web-software');
  const peer = await createEndpointIdentity('conversation-aba', 'ABA', 'web-software');
  const session: EndpointSessionSummary = { sessionId: '01'.repeat(16), hcEndpointId: endpoint, abaEndpointId: '02'.repeat(16),
    status: 'ACTIVE', createdAt: new Date().toISOString(), requestedCapabilities: ['remote-session-v1'], runtimeProfileId: 'fixture-agent', workspaceId: 'fixture-workspace' };
  const aba: ABAEndpointSummary = { id: session.abaEndpointId, name: 'My device', status: 'ACTIVE', type: 'ABA', signingPublicJwk: peer.signing.publicJwk, signingJkt: peer.signing.thumbprint };
  const material: SessionKeyMaterial = { sessionId: idBytes(session.sessionId), generation: 1n, keyId: new Uint8Array(16).fill(5),
    srk: crypto.getRandomValues(new Uint8Array(32)), sessionNonce: crypto.getRandomValues(new Uint8Array(32)),
    hcToAbaNoncePrefix: new Uint8Array(4).fill(6), abaToHcNoncePrefix: new Uint8Array(4).fill(7),
    notBeforeMs: BigInt(Date.now()), expiresAtMs: BigInt(Date.now() + 3_600_000) };
  const derived = await deriveSessionDirectionKeys(material, idBytes(endpoint));
  const keys: OpenedSessionKeyPackage = { material, controlSequence: 2n, keyPackageId: new Uint8Array(16).fill(8), hcToAbaKey: derived.hcToAba, abaToHcKey: derived.abaToHc };
  const factory = new IDBFactory(); const name = `conversation-${crypto.randomUUID()}`;
  const vault = new EncryptedLocalVault(factory, name);
  const store = new ConversationStore(vault, endpoint, identity);
  const value = { ...newConversation(session, aba, identity, 'private-draft-canary'), keys,
    messages: startTurn([], 'pending-1', 'private-prompt-canary'), awaiting: 'pending-1' };
  return { factory, name, vault, store, value, identity, session, aba, keys };
}
describe('bound encrypted conversation snapshots', () => {
  it('stores selection, compose drafts and pending creation outside the conversation prefix with CAS', async () => {
    const f = await fixture(); const initial = await f.store.readWorkspace();
    const value = { ...initial.value, draft: 'private compose', creation: { id: crypto.randomUUID(), abaEndpointId: f.aba.id,
      workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'private pending creation' } };
    const revision = await f.store.writeWorkspace(value, null);
    expect((await f.store.readWorkspace()).value).toEqual(value); expect(await f.store.list()).toHaveLength(0);
    await expect(f.store.writeWorkspace(value, null)).rejects.toThrow('conflict');
    const other = await createEndpointIdentity('other', 'Other', 'web-software');
    await expect(new ConversationStore(f.vault, endpoint, other).readWorkspace()).rejects.toThrow('binding');
    expect((await f.store.readWorkspace()).revision).toBe(revision);
  });
  it('restores keys, messages, pending turn, independent draft and sequence without executing anything', async () => {
    const { factory, name, store, value, identity } = await fixture();
    await store.write(value, null);
    const restored = await new ConversationStore(new EncryptedLocalVault(factory, name), endpoint, identity).read(value.session.sessionId);
    expect(restored?.revision).toBe(1);
    expect(restored?.value.messages).toEqual(value.messages);
    expect(restored?.value.draft).toBe('private-draft-canary');
    expect(restored?.value.awaiting).toBe('pending-1');
    expect(restored?.value.keys?.material.srk).toEqual(value.keys.material.srk);
    expect(restored?.value.keys?.hcToAbaKey).toEqual(value.keys.hcToAbaKey);
    const db = await new Promise<IDBDatabase>((resolve, reject) => {
      const request = factory.open(name); request.onsuccess = () => resolve(request.result); request.onerror = () => reject(request.error);
    });
    const row = await new Promise<unknown>((resolve) => { const request = db.transaction('records').objectStore('records').getAll(); request.onsuccess = () => resolve(request.result); });
    db.close();
    const serialized = JSON.stringify(row);
    expect(serialized).not.toContain('private-prompt-canary'); expect(serialized).not.toContain('private-draft-canary');
    expect(serialized).not.toContain('"srk"'); expect(serialized).not.toContain('"awaiting"');
  });
  it('fails closed for a different browser identity and mismatched ABA fingerprints', async () => {
    const { vault, store, value } = await fixture();
    await store.write(value, null);
    const other = await createEndpointIdentity('other', 'Other', 'web-software');
    await expect(new ConversationStore(vault, endpoint, other).read(value.session.sessionId)).rejects.toThrow('binding');
    await store.write({ ...value, aba: { ...value.aba, signingJkt: 'wrong' } }, 1);
    await expect(store.read(value.session.sessionId)).rejects.toThrow('fingerprint');
  });
  it('never silently reuses an interrupted nonce reservation', async () => {
    const { store, value } = await fixture();
    await store.write({ ...value, outbound: '4', outboundAck: '3', reservation: '4' }, null);
    const restored = await store.read(value.session.sessionId);
    expect(restored?.value.outbound).toBe('4'); expect(restored?.value.reservation).toBe('4');
    expect(restored?.value.blocked).toContain('避免重用');
    await expect(store.write({ ...value, outbound: '4', outboundAck: '5' }, 1)).rejects.toThrow('Acknowledgment');
    await expect(store.write({ ...value, outbound: '4', reservation: '3' }, 1)).rejects.toThrow('reservation');
  });
  it('keeps conversations independent and rejects late writes or deletion of active work', async () => {
    const { store, value, aba, identity } = await fixture();
    const first = await store.write(value, null);
    const second = newConversation({ ...value.session, sessionId: '04'.repeat(16), runtimeProfileId: 'other-agent' }, aba, identity, 'other draft');
    await store.write(second, null);
    await store.write({ ...value, draft: 'new draft' }, first);
    await expect(store.write({ ...value, draft: 'late draft' }, first)).rejects.toThrow('revision conflict');
    expect((await store.read(second.session.sessionId))?.value.draft).toBe('other draft');
    expect(await store.list()).toHaveLength(2);
    const active = await store.read(value.session.sessionId); if (active === null) throw new Error('Missing snapshot');
    await expect(store.delete(active)).rejects.toThrow('Active');
    const revision = await store.write({ ...active.value, session: { ...active.value.session, status: 'CLOSED' }, keys: null }, active.revision);
    await store.delete({ revision, value: { ...active.value, session: { ...active.value.session, status: 'CLOSED' }, keys: null } });
    expect(await store.list()).toHaveLength(1);
  });
  it('binds the ciphertext scope and validates 64-bit cursors rather than truncating numbers', async () => {
    const { vault, store, value } = await fixture();
    await store.write(value, null);
    const raw = await vault.read(`remote-v1/${endpoint}/${value.session.sessionId}`); if (raw === null) throw new Error('Missing record');
    const moved = '07'.repeat(16);
    await vault.write(`remote-v1/${endpoint}/${moved}`, raw.bytes, null);
    await expect(store.read(moved)).rejects.toThrow('scope');
    expect(sequence('18446744073709551615')).toBe(0xffff_ffff_ffff_ffffn);
    for (const invalid of ['01', '-1', '1.5', '18446744073709551616', 1]) expect(() => sequence(invalid)).toThrow();
  });
});
