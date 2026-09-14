import { EncryptedLocalVault, type LocalCipher } from './local-vault';
import {
  assertIdentityUsable,
  createEndpointIdentity,
  type EndpointIdentity,
} from './identity';

const DATABASE_VERSION = 4;
const IDENTITY_STORE = 'endpoint-identities';
const INSTALLATION_SETTINGS_STORE = 'installation-settings';
const PRE_REMOTE_INSTALLATION_ID = 'primary-browser-installation';
const TRUST_PIN_STORE = 'trust-pins';
const SESSION_INBOX_STORE = 'session-inbox';
const GATEWAY_ROOT_PIN = 'gateway-root';
const MAX_INBOX_FRAMES = 4096;

export interface InboxFrame {
  readonly contentHash: Uint8Array;
  readonly direction: 1 | 2;
  readonly messageId: Uint8Array;
  readonly plaintext: Uint8Array;
  readonly receivedAt: string;
  readonly sequence: bigint;
  readonly sessionId: string;
}

interface StoredInboxFrame extends Omit<InboxFrame, 'sequence' | 'plaintext'> {
  readonly id: string;
  readonly sequence: string;
  readonly sealed?: LocalCipher;
  /** Read-only migration input from pre-Remote versions; never written again. */
  readonly plaintext?: Uint8Array;
}

export interface SecureStoreProbe {
  readonly assurance: 'web-software' | 'unsupported';
  readonly detail: string;
  readonly supported: boolean;
}

function requestResult<T>(request: IDBRequest<T>): Promise<T> {
  return new Promise((resolve, reject) => {
    request.addEventListener('success', () => resolve(request.result), { once: true });
    request.addEventListener('error', () => reject(request.error ?? new Error('IndexedDB request failed')), {
      once: true,
    });
  });
}

function transactionComplete(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.addEventListener('complete', () => resolve(), { once: true });
    transaction.addEventListener(
      'abort',
      () => reject(transaction.error ?? new Error('IndexedDB transaction aborted')),
      { once: true },
    );
    transaction.addEventListener(
      'error',
      () => reject(transaction.error ?? new Error('IndexedDB transaction failed')),
      { once: true },
    );
  });
}

export class IndexedDbSecureStore {
  public constructor(
    private readonly factory: IDBFactory,
    private readonly databaseName = 'harness-hc-secure-store-v1',
  ) {}

  private open(): Promise<IDBDatabase> {
    return new Promise((resolve, reject) => {
      const request = this.factory.open(this.databaseName, DATABASE_VERSION);
      request.addEventListener(
        'upgradeneeded',
        () => {
          if (!request.result.objectStoreNames.contains(INSTALLATION_SETTINGS_STORE)) {
            request.result.createObjectStore(INSTALLATION_SETTINGS_STORE, { keyPath: 'id' });
          }
          if (!request.result.objectStoreNames.contains(IDENTITY_STORE)) {
            request.result.createObjectStore(IDENTITY_STORE, { keyPath: 'installationId' });
          }
          if (!request.result.objectStoreNames.contains(TRUST_PIN_STORE)) {
            request.result.createObjectStore(TRUST_PIN_STORE, { keyPath: 'id' });
          }
          if (!request.result.objectStoreNames.contains(SESSION_INBOX_STORE)) {
            const inbox = request.result.createObjectStore(SESSION_INBOX_STORE, { keyPath: 'id' });
            inbox.createIndex('sessionId', 'sessionId', { unique: false });
          }
        },
        { once: true },
      );
      request.addEventListener('success', () => resolve(request.result), { once: true });
      request.addEventListener('error', () => reject(request.error ?? new Error('IndexedDB open failed')), {
        once: true,
      });
      request.addEventListener('blocked', () => reject(new Error('IndexedDB upgrade was blocked')), {
        once: true,
      });
    });
  }

  public async load(installationId: string): Promise<EndpointIdentity | null> {
    const database = await this.open();
    try {
      const transaction = database.transaction(IDENTITY_STORE, 'readonly');
      const value = await requestResult(
        transaction.objectStore(IDENTITY_STORE).get(installationId) as IDBRequest<
          EndpointIdentity | undefined
        >,
      );
      await transactionComplete(transaction);
      if (value === undefined) {
        return null;
      }
      await assertIdentityUsable(value);
      return value;
    } finally {
      database.close();
    }
  }

  public async create(installationId: string, label: string): Promise<EndpointIdentity> {
    const identity = await createEndpointIdentity(installationId, label, 'web-software');
    await this.put(identity);
    const stored = await this.load(installationId);
    if (stored === null) {
      throw new Error('persisted identity could not be loaded');
    }
    return stored;
  }

  public async loadActiveIdentity(): Promise<EndpointIdentity | null> {
    const database = await this.open();
    let installationId: string;
    try {
      const transaction = database.transaction(INSTALLATION_SETTINGS_STORE, 'readonly');
      const value = await requestResult(transaction.objectStore(INSTALLATION_SETTINGS_STORE).get('active') as IDBRequest<{ installationId: string } | undefined>);
      await transactionComplete(transaction);
      installationId = value?.installationId ?? PRE_REMOTE_INSTALLATION_ID;
    } finally { database.close(); }
    return this.load(installationId);
  }

  /** Explicit initial registration/replacement. Old identities and encrypted records remain intact. */
  public async createActiveIdentity(label: string, expectedInstallationId: string | null): Promise<EndpointIdentity> {
    const identity = await createEndpointIdentity(`browser-${crypto.randomUUID()}`, label, 'web-software');
    const database = await this.open();
    const transaction = database.transaction([IDENTITY_STORE, INSTALLATION_SETTINGS_STORE], 'readwrite');
    const completed = transactionComplete(transaction);
    // Observe rejection immediately even when an early validation below aborts the transaction.
    void completed.catch(() => undefined);
    try {
      const settings = transaction.objectStore(INSTALLATION_SETTINGS_STORE);
      const pointer = await requestResult(settings.get('active') as IDBRequest<{ installationId: string } | undefined>);
      const currentId = pointer?.installationId ?? PRE_REMOTE_INSTALLATION_ID;
      const existing = await requestResult(transaction.objectStore(IDENTITY_STORE).get(currentId) as IDBRequest<EndpointIdentity | undefined>);
      if ((existing?.installationId ?? null) !== expectedInstallationId) throw new Error('Active browser identity changed concurrently');
      transaction.objectStore(IDENTITY_STORE).add(identity);
      settings.put({ id: 'active', installationId: identity.installationId });
      await completed;
    } catch (cause) {
      try { transaction.abort(); } catch { /* A completed transaction needs no abort. */ }
      throw cause;
    } finally { database.close(); }
    const restored = await this.load(identity.installationId);
    if (restored === null) throw new Error('New browser identity could not be restored');
    return restored;
  }

  public async delete(installationId: string): Promise<void> {
    const database = await this.open();
    try {
      const transaction = database.transaction(IDENTITY_STORE, 'readwrite');
      transaction.objectStore(IDENTITY_STORE).delete(installationId);
      await transactionComplete(transaction);
    } finally {
      database.close();
    }
  }

  public async probe(): Promise<SecureStoreProbe> {
    const probeId = `probe-${crypto.randomUUID()}`;
    try {
      await this.create(probeId, 'Secure store capability probe');
      await this.delete(probeId);
      await this.migrateLegacyInbox();
      return {
        assurance: 'web-software',
        detail: '不可导出私钥可作为 CryptoKey 句柄持久保存并重新使用。',
        supported: true,
      };
    } catch {
      try {
        await this.delete(probeId);
      } catch {
        // The probe is already failing; cleanup must not hide the fail-closed result.
      }
      return {
        assurance: 'unsupported',
        detail: '当前浏览器不能安全持久化不可导出密钥；不会降级为明文保存。',
        supported: false,
      };
    }
  }

  public async pinTrustRoot(
    rootJkt: string,
    revision: bigint,
    onlineJkt: string,
    expiresAt: Date,
  ): Promise<void> {
    const expiresAtMs = expiresAt.getTime();
    if (
      !/^[A-Za-z0-9_-]{43}$/u.test(rootJkt) ||
      !/^[A-Za-z0-9_-]{43}$/u.test(onlineJkt) ||
      rootJkt === onlineJkt ||
      revision <= 0n ||
      !Number.isSafeInteger(expiresAtMs) ||
      expiresAtMs <= 0
    ) {
      throw new Error('Gateway trust pin is invalid');
    }
    const database = await this.open();
    const transaction = database.transaction(TRUST_PIN_STORE, 'readwrite');
    try {
      const objectStore = transaction.objectStore(TRUST_PIN_STORE);
      const current = await requestResult(
        objectStore.get(GATEWAY_ROOT_PIN) as IDBRequest<
          {
            expiresAtMs?: number;
            id: string;
            onlineJkt?: string;
            revision: string;
            rootJkt: string;
          } | undefined
        >,
      );
      if (current !== undefined) {
        if (current.rootJkt !== rootJkt) {
          throw new Error('Gateway root key changed unexpectedly');
        }
        if (BigInt(current.revision) > revision) {
          throw new Error('Gateway trust manifest revision rolled back');
        }
        if (
          BigInt(current.revision) === revision &&
          ((current.onlineJkt !== undefined && current.onlineJkt !== onlineJkt) ||
            (current.expiresAtMs !== undefined && current.expiresAtMs > expiresAtMs))
        ) {
          throw new Error('Gateway trust manifest changed within one revision');
        }
      }
      objectStore.put({
        expiresAtMs,
        id: GATEWAY_ROOT_PIN,
        onlineJkt,
        revision: revision.toString(),
        rootJkt,
      });
      await transactionComplete(transaction);
    } catch (error) {
      try {
        transaction.abort();
      } catch {
        // A completed transaction needs no rollback.
      }
      throw error;
    } finally {
      database.close();
    }
  }

  public localVault(): EncryptedLocalVault { return new EncryptedLocalVault(this.factory, `${this.databaseName}-content-v1`); }

  public async putInboxFrame(frame: InboxFrame): Promise<'duplicate' | 'stored'> {
    validateInboxFrame(frame);
    const id = inboxFrameId(frame.sessionId, frame.direction, frame.sequence);
    const sealed = await this.localVault().seal(inboxCipherScope(id, frame.contentHash, frame.messageId), frame.plaintext);
    const database = await this.open();
    const transaction = database.transaction(SESSION_INBOX_STORE, 'readwrite');
    try {
      const objectStore = transaction.objectStore(SESSION_INBOX_STORE);
      const existing = await requestResult(
        objectStore.get(id) as IDBRequest<StoredInboxFrame | undefined>,
      );
      if (existing !== undefined) {
        if (!equalBytes(existing.messageId, frame.messageId) || !equalBytes(existing.contentHash, frame.contentHash)) {
          throw new Error('HC inbox sequence conflict');
        }
        if (existing.plaintext !== undefined) {
          const { plaintext: _legacy, ...metadata } = existing;
          objectStore.put({ ...metadata, sealed });
        }
        await transactionComplete(transaction);
        return 'duplicate';
      }
      const count = await requestResult(objectStore.count());
      if (count >= MAX_INBOX_FRAMES) {
        throw new Error('HC inbox capacity reached');
      }
      objectStore.put({
        direction: frame.direction, receivedAt: frame.receivedAt, sessionId: frame.sessionId,
        contentHash: frame.contentHash.slice(),
        id,
        messageId: frame.messageId.slice(),
        sealed,
        sequence: frame.sequence.toString(),
      } satisfies StoredInboxFrame);
      await transactionComplete(transaction);
      return 'stored';
    } catch (error) {
      try {
        transaction.abort();
      } catch {
        // A completed transaction needs no rollback.
      }
      throw error;
    } finally {
      database.close();
    }
  }

  /** Read a bounded page. Cursors are sequences in one direction, never a display-message count. */
  public async readSessionInbox(sessionId: string, afterSequence = 0n, limit = 128, direction: 1 | 2 = 2): Promise<readonly InboxFrame[]> {
    if (!/^[0-9a-f]{32}$/u.test(sessionId) || afterSequence < 0n || afterSequence >= 0xffff_ffff_ffff_ffffn || !Number.isInteger(limit) || limit < 1 || limit > 128 || ![1, 2].includes(direction)) throw new Error('Invalid inbox query');
    const database = await this.open();
    let values: StoredInboxFrame[];
    try {
      const tx = database.transaction(SESSION_INBOX_STORE, 'readonly');
      const range = IDBKeyRange.bound(inboxFrameId(sessionId, direction, afterSequence + 1n), inboxFrameId(sessionId, direction, 0xffff_ffff_ffff_ffffn));
      values = await requestResult(tx.objectStore(SESSION_INBOX_STORE).getAll(range, limit) as IDBRequest<StoredInboxFrame[]>);
      await transactionComplete(tx);
    } finally { database.close(); }
    const result: InboxFrame[] = [];
    for (const value of values) {
      const sequence = BigInt(value.sequence);
      if (value.sessionId !== sessionId || value.direction !== direction || value.id !== inboxFrameId(sessionId, direction, sequence)) throw new Error('Stored inbox binding mismatch');
      const plaintext = value.sealed === undefined ? value.plaintext : await this.localVault().unseal(inboxCipherScope(value.id, value.contentHash, value.messageId), value.sealed);
      if (plaintext === undefined) throw new Error('Local inbox content unavailable');
      const frame: InboxFrame = { sessionId: value.sessionId, direction: value.direction, sequence, contentHash: value.contentHash, messageId: value.messageId, receivedAt: value.receivedAt, plaintext };
      validateInboxFrame(frame);
      if (value.plaintext !== undefined) await this.putInboxFrame(frame);
      result.push(frame);
    }
    return result;
  }

  /** Migrate existing plaintext before the browser is reported ready. No key export or network access. */
  public async migrateLegacyInbox(): Promise<void> {
    let cursor: IDBValidKey | null = null;
    for (;;) {
      const database = await this.open();
      let values: StoredInboxFrame[];
      try {
        const tx = database.transaction(SESSION_INBOX_STORE, 'readonly');
        values = await requestResult(tx.objectStore(SESSION_INBOX_STORE).getAll(cursor === null ? undefined : IDBKeyRange.lowerBound(cursor, true), 16) as IDBRequest<StoredInboxFrame[]>);
        await transactionComplete(tx);
      } finally { database.close(); }
      if (values.length === 0) return;
      for (const value of values) {
        if (value.plaintext !== undefined) {
          const { id, sequence, sealed: _sealed, ...legacy } = value;
          const parsed = BigInt(sequence);
          if (id !== inboxFrameId(value.sessionId, value.direction, parsed)) throw new Error('Legacy inbox binding mismatch');
          await this.putInboxFrame({ ...legacy, plaintext: value.plaintext, sequence: parsed });
        }
        cursor = value.id;
      }
    }
  }

  public async deleteSessionInbox(sessionId: string): Promise<void> {
    if (!/^[0-9a-f]{32}$/u.test(sessionId)) {
      throw new Error('HC inbox session ID is invalid');
    }
    const database = await this.open();
    const transaction = database.transaction(SESSION_INBOX_STORE, 'readwrite');
    try {
      const objectStore = transaction.objectStore(SESSION_INBOX_STORE);
      const keys = await requestResult(objectStore.index('sessionId').getAllKeys(sessionId));
      for (const key of keys) {
        objectStore.delete(key);
      }
      await transactionComplete(transaction);
    } finally {
      database.close();
    }
  }

  private async put(identity: EndpointIdentity): Promise<void> {
    const database = await this.open();
    try {
      const transaction = database.transaction(IDENTITY_STORE, 'readwrite');
      transaction.objectStore(IDENTITY_STORE).put(identity);
      await transactionComplete(transaction);
    } finally {
      database.close();
    }
  }
}

function validateInboxFrame(frame: InboxFrame): void {
  if (
    !/^[0-9a-f]{32}$/u.test(frame.sessionId) ||
    ![1, 2].includes(frame.direction) ||
    frame.sequence <= 0n || frame.sequence > 0xffff_ffff_ffff_ffffn ||
    frame.messageId.length !== 16 ||
    frame.messageId.every((byte) => byte === 0) ||
    frame.contentHash.length !== 32 ||
    frame.contentHash.every((byte) => byte === 0) ||
    frame.plaintext.length === 0 ||
    frame.plaintext.length > 1_048_560 ||
    !Number.isFinite(Date.parse(frame.receivedAt))
  ) {
    throw new Error('HC inbox frame is invalid');
  }
}

function inboxFrameId(sessionId: string, direction: 1 | 2, sequence: bigint): string {
  return `${sessionId}:${direction}:${sequence.toString().padStart(20, '0')}`;
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.length === right.length && left.every((value, index) => value === right[index]);
}

function inboxCipherScope(id: string, hash: Uint8Array, messageId: Uint8Array): string {
  const hex = (bytes: Uint8Array) => Array.from(bytes, (value) => value.toString(16).padStart(2, '0')).join('');
  return `inbox/${id}/${hex(hash)}/${hex(messageId)}`;
}
