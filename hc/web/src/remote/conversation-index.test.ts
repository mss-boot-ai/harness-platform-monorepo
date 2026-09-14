import { describe, expect, it } from 'vitest';
import { createEndpointIdentity } from '@harness/hc-core';
import { ConversationIndex } from './conversation-index';
import { controllerFixture, testEndpoint, testNow } from './conversation-fixture';

describe('independent persistent conversations', () => {
  it('creates, renames and archives a conversation without creating any execution', async () => {
    const f = await controllerFixture(); const index = new ConversationIndex(f.vault, testEndpoint, f.identity);
    const value = index.create({ abaEndpointId: f.session.abaEndpointId, workspaceId: 'fixture', runtimeProfileId: 'fixture' }, 'private draft', testNow);
    const first = await index.write(value, null);
    const changed = { ...value, title: 'A user title', archived: true };
    await index.write(changed, first);
    expect((await index.read(value.id))?.value).toEqual(changed);
    expect(changed.runIds).toEqual([]); expect(changed.activeRunId).toBeNull();
    await expect(index.write(value, first)).rejects.toThrow('conflict');
    expect(f.sent).toEqual([]); expect(f.controls).toEqual([]);
  });
  it('adopts an existing run without changing its keys, packets or counters, then retains multiple runs', async () => {
    const f = await controllerFixture(); const index = new ConversationIndex(f.vault, testEndpoint, f.identity);
    const original = await f.store.read(f.session.sessionId);
    const adopted = await index.ensure(index.fromRun(f.value));
    expect((await index.ensure(index.fromRun(f.value))).revision).toBe(adopted.revision);
    expect(await f.store.read(f.session.sessionId)).toEqual(original);
    const nextRun = '08'.repeat(16);
    await index.write({ ...adopted.value, runIds: [...adopted.value.runIds, nextRun], activeRunId: nextRun }, adopted.revision);
    expect((await index.read(adopted.value.id))?.value.runIds).toEqual([f.session.sessionId, nextRun]);
    expect((await index.read(adopted.value.id))?.value.id).toBe(adopted.value.id);
  });
  it('scopes creation intent to one conversation and isolates a malformed record', async () => {
    const f = await controllerFixture(); const index = new ConversationIndex(f.vault, testEndpoint, f.identity);
    const intent = { id: crypto.randomUUID(), abaEndpointId: f.session.abaEndpointId, workspaceId: 'fixture', runtimeProfileId: 'fixture', draft: 'pending draft' };
    const pending = await index.creationEntry(intent, 'latest draft', testNow);
    expect((await index.creationEntry(intent, 'latest draft', testNow)).id).toBe(pending.id);
    await index.ensure(pending);
    const separate = await index.ensure(index.create(null, 'independent draft', testNow));
    await f.vault.write(`conversations-v2/${testEndpoint}/${'09'.repeat(16)}`, new TextEncoder().encode('{}'), null);
    const listed = await index.list();
    expect(listed.entries).toHaveLength(2); expect(listed.issues).toHaveLength(1);
    expect((await index.read(separate.value.id))?.value.creation).toBeNull();
    const other = await createEndpointIdentity('other', 'Other', 'web-software');
    await expect(new ConversationIndex(f.vault, testEndpoint, other).read(pending.id)).rejects.toThrow('identity');
  });
});
