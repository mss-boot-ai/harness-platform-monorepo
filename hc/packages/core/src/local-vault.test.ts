import { IDBFactory } from 'fake-indexeddb';
import { EncryptedLocalVault, idbComplete, idbResult } from './local-vault';
const encoder = new TextEncoder();
const decoder = new TextDecoder();
function database(factory: IDBFactory, name: string): Promise<IDBDatabase> { return idbResult(factory.open(name)); }

describe('encrypted local vault', () => {
  it('persists ciphertext and a non-exportable key, not conversation text', async () => {
    const factory = new IDBFactory(); const name = `vault-${crypto.randomUUID()}`;
    const vault = new EncryptedLocalVault(factory, name);
    expect(await vault.write('session/example', encoder.encode('private-canary-phrase'), null)).toBe(1);
    expect(decoder.decode((await new EncryptedLocalVault(factory, name).read('session/example'))?.bytes)).toBe('private-canary-phrase');
    const db = await database(factory, name);
    const tx = db.transaction(['keys', 'records'], 'readonly');
    const record = await idbResult(tx.objectStore('records').get('session/example'));
    const key = await idbResult(tx.objectStore('keys').get('content-v1')) as CryptoKey;
    await idbComplete(tx); db.close();
    expect(JSON.stringify(record)).not.toContain('private-canary-phrase');
    expect(record).not.toHaveProperty('bytes'); expect(key.extractable).toBe(false);
    await expect(crypto.subtle.exportKey('raw', key)).rejects.toThrow();
  });
  it('binds the record identity/revision and detects altered ciphertext', async () => {
    const vault = new EncryptedLocalVault(new IDBFactory(), 'vault-tamper');
    const sealed = await vault.seal('session/a', encoder.encode('content'));
    await expect(vault.unseal('session/b', sealed)).rejects.toThrow();
    const changed = { ...sealed, ciphertext: sealed.ciphertext.slice() }; changed.ciphertext[0] = (changed.ciphertext[0] ?? 0) ^ 1;
    await expect(vault.unseal('session/a', changed)).rejects.toThrow();
    expect(decoder.decode(await vault.unseal('session/a', sealed))).toBe('content');
  });
  it('serializes concurrent creation and rejects stale snapshot writes/deletes', async () => {
    const factory = new IDBFactory(); const name = 'vault-cas';
    const one = new EncryptedLocalVault(factory, name); const two = new EncryptedLocalVault(factory, name);
    const results = await Promise.allSettled([one.write('session/a', encoder.encode('one'), null), two.write('session/a', encoder.encode('two'), null)]);
    expect(results.filter((item) => item.status === 'fulfilled')).toHaveLength(1);
    expect((await one.read('session/a'))?.revision).toBe(1);
    await expect(two.write('session/a', encoder.encode('three'), null)).rejects.toThrow('revision conflict');
    expect(await one.write('session/a', encoder.encode('new'), 1)).toBe(2);
    await expect(two.delete('session/a', 1)).rejects.toThrow('revision conflict');
    expect(await two.list('session/')).toEqual([{ id: 'session/a', revision: 2 }]);
    await one.delete('session/a', 2); expect(await one.read('session/a')).toBeNull();
  });
  it('fails closed if a key is lost while encrypted records remain', async () => {
    const factory = new IDBFactory(); const name = 'vault-lost'; const vault = new EncryptedLocalVault(factory, name);
    await vault.write('session/a', encoder.encode('secret'), null);
    const db = await database(factory, name); const tx = db.transaction('keys', 'readwrite'); tx.objectStore('keys').clear(); await idbComplete(tx); db.close();
    await expect(vault.read('session/a')).rejects.toThrow('key was lost');
    await expect(vault.write('session/b', encoder.encode('other'), null)).rejects.toThrow('key was lost');
  });
});

it('uses strict durable transactions before acknowledging key and cursor writes', async () => {
  const factory = new IDBFactory(); const name = 'vault-durable';
  const vault = new EncryptedLocalVault(factory, name);
  await vault.write('session/a', encoder.encode('initial'), null);
  const db = await database(factory, name);
  const prototype: IDBDatabase = Object.getPrototypeOf(db) as IDBDatabase;
  const calls = vi.spyOn(prototype, 'transaction');
  db.close();
  await vault.write('session/a', encoder.encode('new cursor'), 1);
  await vault.delete('session/a', 2);
  const writes = calls.mock.calls.filter((call) => call[1] === 'readwrite');
  expect(writes.length).toBeGreaterThanOrEqual(3);
  expect(writes.every((call) => call[2]?.durability === 'strict')).toBe(true);
});
