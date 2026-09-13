/** Software-protected, per-browser storage. Does not claim protection from same-origin XSS. */
export interface LocalCipher {
  readonly version: 1;
  readonly nonce: Uint8Array;
  readonly ciphertext: Uint8Array;
}
export interface VaultValue { readonly revision: number; readonly bytes: Uint8Array }
interface VaultRecord { readonly id: string; readonly revision: number; readonly cipher: LocalCipher; readonly size: number }
const MAX_VALUE_BYTES = 4 * 1024 * 1024;
const MAX_TOTAL_BYTES = 32 * 1024 * 1024;
const MAX_RECORDS = 128;
const encoder = new TextEncoder();

export function idbResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.addEventListener('success', () => resolve(request.result), { once: true });
    request.addEventListener('error', () => reject(request.error ?? new Error('Local storage request failed')), { once: true });
  });
}
export function idbComplete(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.addEventListener('complete', () => resolve(), { once: true });
    transaction.addEventListener('abort', () => reject(transaction.error ?? new Error('Local storage transaction aborted')), { once: true });
    transaction.addEventListener('error', () => reject(transaction.error ?? new Error('Local storage transaction failed')), { once: true });
  });
}
function validateScope(scope: string): void {
  if (!/^[a-zA-Z0-9:/_.-]{1,256}$/u.test(scope)) throw new Error('Invalid local storage scope');
}
function validateKey(key: CryptoKey): void {
  if (key.type !== 'secret' || key.extractable || key.algorithm.name !== 'AES-GCM' || (key.algorithm as AesKeyAlgorithm).length !== 256 || !key.usages.includes('encrypt') || !key.usages.includes('decrypt')) throw new Error('Local storage key is invalid');
}
function buffer(bytes: Uint8Array): ArrayBuffer { return new Uint8Array(bytes).buffer; }

export class EncryptedLocalVault {
  public constructor(private readonly factory: IDBFactory, private readonly name: string) {}
  private open(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
      const request = this.factory.open(this.name, 1);
      request.addEventListener('upgradeneeded', () => {
        const db = request.result;
        db.createObjectStore('keys');
        db.createObjectStore('records', { keyPath: 'id' });
      }, { once: true });
      request.addEventListener('success', () => {
        request.result.addEventListener('versionchange', () => request.result.close());
        resolve(request.result);
      }, { once: true });
      request.addEventListener('error', () => reject(request.error ?? new Error('Local vault unavailable')), { once: true });
      request.addEventListener('blocked', () => reject(new Error('Local vault upgrade is blocked')), { once: true });
    });
  }
  private async key(create = false): Promise<CryptoKey> {
    // Generate outside the IDB transaction; crypto awaits inside one would deactivate it.
    const candidate = create ? await crypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['encrypt', 'decrypt']) : null;
    const db = await this.open();
    try {
      const transaction = db.transaction(['keys', 'records'], 'readwrite');
      const keys = transaction.objectStore('keys');
      let key = await idbResult(keys.get('content-v1') as IDBRequest<CryptoKey | undefined>);
      if (key === undefined) {
        if (candidate === null || await idbResult(transaction.objectStore('records').count()) > 0) throw new Error('Local content key was lost; existing data cannot be recovered automatically');
        key = candidate;
        keys.add(key, 'content-v1');
      }
      validateKey(key);
      await idbComplete(transaction);
      return key;
    } finally { db.close(); }
  }
  public async seal(scope: string, plaintext: Uint8Array): Promise<LocalCipher> {
    validateScope(scope);
    if (plaintext.length === 0 || plaintext.length > MAX_VALUE_BYTES) throw new Error('Local content exceeds storage bound');
    const key = await this.key(true);
    const nonce = crypto.getRandomValues(new Uint8Array(12));
    const aad = encoder.encode(`MSS-HC-LOCAL-V1\0${scope}`);
    const ciphertext = await crypto.subtle.encrypt({ name: 'AES-GCM', iv: buffer(nonce), additionalData: buffer(aad), tagLength: 128 }, key, buffer(plaintext));
    return { version: 1, nonce, ciphertext: new Uint8Array(ciphertext) };
  }
  public async unseal(scope: string, cipher: LocalCipher): Promise<Uint8Array> {
    validateScope(scope);
    if (cipher.version !== 1 || !(cipher.nonce instanceof Uint8Array) || cipher.nonce.length !== 12 || !(cipher.ciphertext instanceof Uint8Array) || cipher.ciphertext.length < 17 || cipher.ciphertext.length > MAX_VALUE_BYTES + 16) throw new Error('Invalid local sealed content');
    const key = await this.key();
    return new Uint8Array(await crypto.subtle.decrypt({ name: 'AES-GCM', iv: buffer(cipher.nonce), additionalData: buffer(encoder.encode(`MSS-HC-LOCAL-V1\0${scope}`)), tagLength: 128 }, key, buffer(cipher.ciphertext)));
  }
  public async read(id: string): Promise<VaultValue | null> {
    validateScope(id);
    const db = await this.open();
    let value: VaultRecord | undefined;
    try {
      const tx = db.transaction('records', 'readonly');
      value = await idbResult(tx.objectStore('records').get(id) as IDBRequest<VaultRecord | undefined>);
      await idbComplete(tx);
    } finally { db.close(); }
    if (value === undefined) return null;
    if (!Number.isSafeInteger(value.revision) || value.revision < 1) throw new Error('Invalid local revision');
    return { revision: value.revision, bytes: await this.unseal(`${id}/revision/${value.revision}`, value.cipher) };
  }
  /** Compare-and-set prevents late snapshots or concurrent tabs overwriting newer state. */
  public async write(id: string, bytes: Uint8Array, expectedRevision: number | null): Promise<number> {
    validateScope(id);
    if (id.length > 200 || (expectedRevision !== null && (!Number.isSafeInteger(expectedRevision) || expectedRevision < 1 || expectedRevision >= Number.MAX_SAFE_INTEGER))) throw new Error('Invalid local revision');
    const revision = (expectedRevision ?? 0) + 1;
    const cipher = await this.seal(`${id}/revision/${revision}`, bytes);
    const db = await this.open();
    try {
      const tx = db.transaction('records', 'readwrite');
      const records = tx.objectStore('records');
      const existing = await idbResult(records.get(id) as IDBRequest<VaultRecord | undefined>);
      if ((existing?.revision ?? null) !== expectedRevision) throw new Error('Local snapshot revision conflict');
      const all = await idbResult(records.getAll() as IDBRequest<VaultRecord[]>);
      if ((existing === undefined && all.length >= MAX_RECORDS) || all.reduce((sum, item) => sum + item.size, 0) - (existing?.size ?? 0) + cipher.ciphertext.length > MAX_TOTAL_BYTES) throw new Error('Local encrypted storage capacity reached');
      records.put({ id, revision, cipher, size: cipher.ciphertext.length } satisfies VaultRecord);
      await idbComplete(tx);
      return revision;
    } finally { db.close(); }
  }
  public async list(prefix: string): Promise<readonly { readonly id: string; readonly revision: number }[]> {
    validateScope(prefix);
    const db = await this.open();
    try {
      const tx = db.transaction('records', 'readonly');
      const values = await idbResult(tx.objectStore('records').getAll() as IDBRequest<VaultRecord[]>);
      await idbComplete(tx);
      return values.filter((item) => item.id.startsWith(prefix)).map(({ id, revision }) => ({ id, revision }));
    } finally { db.close(); }
  }
  public async delete(id: string, expectedRevision: number): Promise<void> {
    validateScope(id);
    const db = await this.open();
    try {
      const tx = db.transaction('records', 'readwrite');
      const records = tx.objectStore('records');
      const existing = await idbResult(records.get(id) as IDBRequest<VaultRecord | undefined>);
      if (existing !== undefined && existing.revision !== expectedRevision) throw new Error('Local snapshot revision conflict');
      records.delete(id);
      await idbComplete(tx);
    } finally { db.close(); }
  }
}
