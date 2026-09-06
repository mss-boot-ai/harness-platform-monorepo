import {
  assertIdentityUsable,
  createEndpointIdentity,
  type EndpointIdentity,
} from './identity';

const DATABASE_VERSION = 3;
const IDENTITY_STORE = 'endpoint-identities';
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

interface StoredInboxFrame extends Omit<InboxFrame, 'sequence'> {
  readonly id: string;
  readonly sequence: string;
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

  public async pinTrustRoot(rootJkt: string, revision: bigint): Promise<void> {
    if (!/^[A-Za-z0-9_-]{43}$/u.test(rootJkt) || revision <= 0n) {
      throw new Error('Gateway trust pin is invalid');
    }
    const database = await this.open();
    const transaction = database.transaction(TRUST_PIN_STORE, 'readwrite');
    try {
      const objectStore = transaction.objectStore(TRUST_PIN_STORE);
      const current = await requestResult(
        objectStore.get(GATEWAY_ROOT_PIN) as IDBRequest<
          { id: string; revision: string; rootJkt: string } | undefined
        >,
      );
      if (current !== undefined) {
        if (current.rootJkt !== rootJkt) {
          throw new Error('Gateway root key changed unexpectedly');
        }
        if (BigInt(current.revision) > revision) {
          throw new Error('Gateway trust manifest revision rolled back');
        }
      }
      objectStore.put({ id: GATEWAY_ROOT_PIN, revision: revision.toString(), rootJkt });
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

  public async putInboxFrame(frame: InboxFrame): Promise<'duplicate' | 'stored'> {
    validateInboxFrame(frame);
    const database = await this.open();
    const transaction = database.transaction(SESSION_INBOX_STORE, 'readwrite');
    try {
      const objectStore = transaction.objectStore(SESSION_INBOX_STORE);
      const id = inboxFrameId(frame.sessionId, frame.direction, frame.sequence);
      const existing = await requestResult(
        objectStore.get(id) as IDBRequest<StoredInboxFrame | undefined>,
      );
      if (existing !== undefined) {
        if (!equalBytes(existing.messageId, frame.messageId) || !equalBytes(existing.contentHash, frame.contentHash)) {
          throw new Error('HC inbox sequence conflict');
        }
        await transactionComplete(transaction);
        return 'duplicate';
      }
      const count = await requestResult(objectStore.count());
      if (count >= MAX_INBOX_FRAMES) {
        throw new Error('HC inbox capacity reached');
      }
      objectStore.put({
        ...frame,
        contentHash: frame.contentHash.slice(),
        id,
        messageId: frame.messageId.slice(),
        plaintext: frame.plaintext.slice(),
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
    frame.sequence <= 0n ||
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
