import { IDBFactory } from 'fake-indexeddb';
import { IndexedDbSecureStore } from './secure-store';

describe('IndexedDbSecureStore', () => {
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
    await store.pinTrustRoot(root, 1n);
    await store.pinTrustRoot(root, 2n);
    await expect(store.pinTrustRoot(root, 1n)).rejects.toThrow('rolled back');
    await expect(store.pinTrustRoot('B'.repeat(43), 3n)).rejects.toThrow('changed unexpectedly');
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
