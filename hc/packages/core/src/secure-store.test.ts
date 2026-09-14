import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
Object.defineProperty(globalThis, 'IDBKeyRange', { value: IDBKeyRange, configurable: true });
import { IndexedDbSecureStore } from './secure-store';

describe('IndexedDbSecureStore', () => {
  it('switches the active identity atomically while retaining old keys and records', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-active-${crypto.randomUUID()}`);
    const old = await store.create('primary-browser-installation', 'Previous browser');
    expect((await store.loadActiveIdentity())?.signing.thumbprint).toBe(old.signing.thumbprint);
    await store.localVault().write('preserved-record', new TextEncoder().encode('retained encrypted history'), null);
    const next = await store.createActiveIdentity('New browser', old.installationId);
    expect(next.signing.thumbprint).not.toBe(old.signing.thumbprint);
    expect((await store.loadActiveIdentity())?.installationId).toBe(next.installationId);
    expect((await store.load(old.installationId))?.signing.thumbprint).toBe(old.signing.thumbprint);
    expect(await store.localVault().read('preserved-record')).not.toBeNull();
    await expect(store.createActiveIdentity('Stale replacement', old.installationId)).rejects.toThrow('concurrently');
    expect((await store.loadActiveIdentity())?.installationId).toBe(next.installationId);
    await expect(crypto.subtle.exportKey('jwk', next.signing.privateKey)).rejects.toThrow();
  });

  it('does not overwrite an existing installation through first-time registration', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-initial-${crypto.randomUUID()}`);
    const first = await store.createActiveIdentity('Browser', null);
    await expect(store.createActiveIdentity('Duplicate', null)).rejects.toThrow('concurrently');
    expect((await store.loadActiveIdentity())?.installationId).toBe(first.installationId);
  });
  it('persists CryptoKey handles without exporting private key material', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-test-${crypto.randomUUID()}`);
    const identity = await store.create('primary', 'Test browser');
    const restored = await store.load('primary');

    expect(restored?.assurance).toBe('web-software');
    expect(restored?.signing.thumbprint).toBe(identity.signing.thumbprint);
    expect(restored?.signing.privateKey.extractable).toBe(false);
    await expect(crypto.subtle.exportKey('jwk', restored!.kem.privateKey)).rejects.toThrow();

    await store.delete('primary');
    await expect(store.load('primary')).resolves.toBeNull();
  });

  it('reports a supported assurance only after a write/read/crypto probe', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-probe-${crypto.randomUUID()}`);
    await expect(store.probe()).resolves.toEqual({
      assurance: 'web-software',
      detail: '不可导出私钥可作为 CryptoKey 句柄持久保存并重新使用。',
      supported: true,
    });
  });

  it('pins the first Gateway root and rejects replacement or revision rollback', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-trust-${crypto.randomUUID()}`);
    const root = 'A'.repeat(43);
    const online = 'B'.repeat(43);
    await store.pinTrustRoot(root, 1n, online, new Date('2030-01-01T00:00:00Z'));
    await store.pinTrustRoot(root, 1n, online, new Date('2030-01-02T00:00:00Z'));
    await expect(
      store.pinTrustRoot(root, 1n, online, new Date('2030-01-01T12:00:00Z')),
    ).rejects.toThrow('changed within one revision');
    await expect(
      store.pinTrustRoot(root, 1n, 'C'.repeat(43), new Date('2030-01-03T00:00:00Z')),
    ).rejects.toThrow('changed within one revision');
    await store.pinTrustRoot(root, 2n, 'C'.repeat(43), new Date('2030-01-03T00:00:00Z'));
    await expect(
      store.pinTrustRoot(root, 1n, 'C'.repeat(43), new Date('2030-01-04T00:00:00Z')),
    ).rejects.toThrow('rolled back');
    await expect(
      store.pinTrustRoot('D'.repeat(43), 3n, 'C'.repeat(43), new Date('2030-01-04T00:00:00Z')),
    ).rejects.toThrow('changed unexpectedly');
  });

  it('durably deduplicates bounded session inbox frames and detects conflicts', async () => {
    const store = new IndexedDbSecureStore(new IDBFactory(), `hc-inbox-${crypto.randomUUID()}`);
    const frame = {
      contentHash: new Uint8Array(32).fill(4),
      direction: 2 as const,
      messageId: new Uint8Array(16).fill(3),
      plaintext: new TextEncoder().encode('{"jsonrpc":"2.0","id":1,"result":{}}'),
      receivedAt: '2030-01-01T00:00:00Z',
      sequence: 1n,
      sessionId: '01010101010101010101010101010101',
    };
    await expect(store.putInboxFrame(frame)).resolves.toBe('stored');
    await expect(store.putInboxFrame(frame)).resolves.toBe('duplicate');
    await expect(store.putInboxFrame({
      ...frame,
      contentHash: new Uint8Array(32).fill(5),
    })).rejects.toThrow('sequence conflict');
    await store.deleteSessionInbox(frame.sessionId);
    await expect(store.putInboxFrame(frame)).resolves.toBe('stored');
  });
});

it('keeps the durable inbox encrypted and allows same-browser history reads', async () => {
  const factory = new IDBFactory(); const name = `encrypted-inbox-${crypto.randomUUID()}`;
  const store = new IndexedDbSecureStore(factory, name);
  const frame = { sessionId: '01'.repeat(16), direction: 2 as const, sequence: 1n, contentHash: new Uint8Array(32).fill(4), messageId: new Uint8Array(16).fill(3), plaintext: new TextEncoder().encode('sensitive-inbox-canary'), receivedAt: '2030-01-01T00:00:00Z' };
  await store.putInboxFrame(frame);
  const db = await new Promise<IDBDatabase>((resolve, reject) => { const req = factory.open(name); req.onsuccess = () => resolve(req.result); req.onerror = () => reject(req.error); });
  const tx = db.transaction('session-inbox', 'readonly');
  const rows = await new Promise<unknown[]>((resolve, reject) => { const req = tx.objectStore('session-inbox').getAll(); req.onsuccess = () => resolve(req.result); req.onerror = () => reject(req.error); });
  db.close();
  expect(rows[0]).not.toHaveProperty('plaintext'); expect(JSON.stringify(rows)).not.toContain('sensitive-inbox-canary');
  const restored = await new IndexedDbSecureStore(factory, name).readSessionInbox(frame.sessionId);
  expect(restored).toHaveLength(1); expect(new TextDecoder().decode(restored[0]?.plaintext)).toBe('sensitive-inbox-canary');
  expect(await store.readSessionInbox(frame.sessionId, 1n)).toEqual([]);
});

it('migrates legacy inbox plaintext during the readiness probe and binds encrypted metadata', async () => {
  const factory = new IDBFactory(); const name = `legacy-inbox-${crypto.randomUUID()}`;
  const store = new IndexedDbSecureStore(factory, name);
  const frame = { sessionId: '02'.repeat(16), direction: 2 as const, sequence: 2n, contentHash: new Uint8Array(32).fill(5), messageId: new Uint8Array(16).fill(6), plaintext: new TextEncoder().encode('legacy-private-message'), receivedAt: '2030-01-01T00:00:00Z' };
  await store.putInboxFrame(frame);
  const open = () => new Promise<IDBDatabase>((resolve, reject) => { const req = factory.open(name); req.onsuccess = () => resolve(req.result); req.onerror = () => reject(req.error); });
  const id = `${frame.sessionId}:2:${frame.sequence.toString().padStart(20, '0')}`;
  let db = await open();
  let tx = db.transaction('session-inbox', 'readwrite'); tx.objectStore('session-inbox').put({ ...frame, sequence: '2', id });
  await new Promise<void>((resolve, reject) => { tx.oncomplete = () => resolve(); tx.onabort = () => reject(tx.error); }); db.close();
  expect((await store.probe()).supported).toBe(true);
  expect(new TextDecoder().decode((await store.readSessionInbox(frame.sessionId, 1n, 1))[0]?.plaintext)).toBe('legacy-private-message');
  db = await open(); tx = db.transaction('session-inbox', 'readwrite');
  const raw = await new Promise<Record<string, unknown>>((resolve) => { const request = tx.objectStore('session-inbox').get(id); request.onsuccess = () => resolve(request.result as Record<string, unknown>); });
  expect(raw).not.toHaveProperty('plaintext');
  tx.objectStore('session-inbox').put({ ...raw, messageId: new Uint8Array(16).fill(7) });
  await new Promise<void>((resolve, reject) => { tx.oncomplete = () => resolve(); tx.onabort = () => reject(tx.error); }); db.close();
  await expect(store.readSessionInbox(frame.sessionId)).rejects.toThrow();
});
