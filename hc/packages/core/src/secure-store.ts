import {
  assertIdentityUsable,
  createEndpointIdentity,
  type EndpointIdentity,
} from './identity';

const DATABASE_VERSION = 2;
const IDENTITY_STORE = 'endpoint-identities';
const TRUST_PIN_STORE = 'trust-pins';
const GATEWAY_ROOT_PIN = 'gateway-root';

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
