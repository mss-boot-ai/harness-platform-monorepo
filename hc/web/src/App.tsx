import {
  createEndpointIdentity,
  IndexedDbSecureStore,
  type EndpointIdentity,
  type ReadyGatewayConnection,
  type SecureStoreProbe,
} from '@harness/hc-core';
import { useEffect, useMemo, useState } from 'react';
import { PlatformSetup } from './PlatformSetup';
import { GatewaySetup } from './GatewaySetup';
import { SessionSetup } from './SessionSetup';
import type { RegistrationSession } from './api';

const PRIMARY_INSTALLATION_ID = 'primary-browser-installation';

type Readiness = 'checking' | 'ready' | 'unsupported';

function shortThumbprint(value: string): string {
  return `${value.slice(0, 10)}…${value.slice(-8)}`;
}

function IdentityCard({
  identity,
  registration,
}: {
  readonly identity: EndpointIdentity;
  readonly registration: RegistrationSession | null;
}) {
  return (
    <section className="identity-card" aria-labelledby="identity-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">LOCAL ENDPOINT</p>
          <h2 id="identity-title">浏览器身份已就绪</h2>
        </div>
        <span className="status-pill success">{identity.assurance}</span>
      </div>
      <dl className="identity-grid">
        <div>
          <dt>签名密钥 JKT</dt>
          <dd>{shortThumbprint(identity.signing.thumbprint)}</dd>
        </div>
        <div>
          <dt>KEM 密钥 JKT</dt>
          <dd>{shortThumbprint(identity.kem.thumbprint)}</dd>
        </div>
        <div>
          <dt>私钥策略</dt>
          <dd>不可导出 · 不写入 Local Storage</dd>
        </div>
        <div>
          <dt>Platform 状态</dt>
          <dd className={registration === null ? 'pending-text' : 'registered-text'}>
            {registration === null ? '尚未注册' : `已注册 · ${registration.endpointId.slice(0, 10)}…`}
          </dd>
        </div>
      </dl>
    </section>
  );
}

export function App() {
  const store = useMemo(
    () => (globalThis.indexedDB === undefined ? null : new IndexedDbSecureStore(globalThis.indexedDB)),
    [],
  );
  const [readiness, setReadiness] = useState<Readiness>('checking');
  const [probe, setProbe] = useState<SecureStoreProbe | null>(null);
  const [identity, setIdentity] = useState<EndpointIdentity | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [registration, setRegistration] = useState<RegistrationSession | null>(null);
  const [gatewayConnection, setGatewayConnection] = useState<ReadyGatewayConnection | null>(null);
  const gatewayReady = gatewayConnection !== null;

  useEffect(() => {
    let active = true;
    const inspect = async () => {
      if (globalThis.crypto?.subtle === undefined || store === null) {
        if (active) {
          setProbe({
            assurance: 'unsupported',
            detail: '缺少 WebCrypto 或 IndexedDB；不会创建可持久端点。',
            supported: false,
          });
          setReadiness('unsupported');
        }
        return;
      }

      const existing = await store.load(PRIMARY_INSTALLATION_ID);
      const result = await store.probe();
      if (active) {
        setIdentity(existing);
        setProbe(result);
        setReadiness(result.supported ? 'ready' : 'unsupported');
      }
    };

    void inspect().catch(() => {
      if (active) {
        setProbe({
          assurance: 'unsupported',
          detail: '安全能力检查失败；不会降级保存私钥。',
          supported: false,
        });
        setReadiness('unsupported');
      }
    });
    return () => {
      active = false;
    };
  }, [store]);

  const createPersistent = async () => {
    if (store === null || probe?.supported !== true) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setIdentity(await store.create(PRIMARY_INSTALLATION_ID, 'This browser'));
    } catch {
      setError('创建安全端点失败。没有导出或明文保存任何私钥。');
    } finally {
      setBusy(false);
    }
  };

  const createEphemeral = async () => {
    if (globalThis.crypto?.subtle === undefined) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      setIdentity(
        await createEndpointIdentity(crypto.randomUUID(), 'Temporary browser tab', 'web-ephemeral'),
      );
    } catch {
      setError('创建临时端点失败。');
    } finally {
      setBusy(false);
    }
  };

  return (
    <main className="app-shell">
      <header className="topbar">
        <a className="brand" href="/" aria-label="Harness HC 首页">
          <span className="brand-mark">H</span>
          <span>Harness HC</span>
        </a>
        <span className="environment-badge">H5 · LOCAL</span>
      </header>

      <section className="hero">
        <div className="hero-copy">
          <p className="eyebrow">SECURE ACP CONTROL</p>
          <h1>先建立可信端点，<br />再连接你的 Agent。</h1>
          <p className="hero-description">
            私钥只在当前浏览器生成。Harness Platform 可以路由连接与密文，不能读取你的 Prompt、源码或 Agent 响应。
          </p>
        </div>
        <div className="trust-card">
          <span className="trust-icon" aria-hidden="true">◇</span>
          <div>
            <strong>Opaque Mode</strong>
            <p>HC ↔ ABA 端到端加密</p>
          </div>
        </div>
      </section>

      <section className="workspace-grid">
        <div className="primary-column">
          {identity === null ? (
            <section className="setup-card" aria-labelledby="setup-title">
              <div className="section-heading">
                <div>
                  <p className="eyebrow">STEP 1 OF 4</p>
                  <h2 id="setup-title">创建此浏览器端点</h2>
                </div>
                <span className={`status-dot ${readiness}`} aria-label={readiness} />
              </div>

              <div className="capability-row">
                <div>
                  <strong>安全存储能力</strong>
                  <p>{probe?.detail ?? '正在验证 WebCrypto 与 IndexedDB…'}</p>
                </div>
                <span>{probe?.assurance ?? 'checking'}</span>
              </div>

              {error === null ? null : <p className="error-banner" role="alert">{error}</p>}

              <div className="action-stack">
                <button
                  className="primary-button"
                  type="button"
                  disabled={busy || readiness !== 'ready'}
                  onClick={() => void createPersistent()}
                >
                  {busy ? '正在生成…' : '创建安全浏览器端点'}
                </button>
                <button
                  className="secondary-button"
                  type="button"
                  disabled={busy || globalThis.crypto?.subtle === undefined}
                  onClick={() => void createEphemeral()}
                >
                  使用临时端点（关闭页面即失效）
                </button>
              </div>
              <p className="fine-print">不会把私钥、Token 或 Session Key 写入 Local Storage。</p>
            </section>
          ) : (
            <>
              <IdentityCard identity={identity} registration={registration} />
              <PlatformSetup
                identity={identity}
                registration={registration}
                onRegistered={setRegistration}
              />
              {registration === null ? null : (
                <GatewaySetup
                  identity={identity}
                  onRegistration={setRegistration}
                  onReady={setGatewayConnection}
                />
              )}
              {registration !== null && gatewayReady ? (
                <SessionSetup
                  connection={gatewayConnection}
                  identity={identity}
                  onRegistration={setRegistration}
                  registration={registration}
                />
              ) : null}
            </>
          )}

          <section className="next-card">
            <div>
              <p className="eyebrow">NEXT CHECKPOINT</p>
              <h2>{registration === null ? '注册 Platform Endpoint' : gatewayReady ? '创建 ACP Session' : '连接 Platform Gateway'}</h2>
              <p>
                {registration === null
                  ? '先完成 Human Session 与本地 Endpoint Key 的双重绑定。'
                  : gatewayReady
                    ? '安全连接已就绪；下一阶段选择 ABA、Runtime 与 Workspace。'
                    : '下一步使用内存中的 Access Token、DPoP 与一次性 WSS Ticket 建立安全连接。'}
              </p>
            </div>
            <span className={`status-pill ${gatewayReady ? 'success' : 'pending'}`}>{gatewayReady ? 'READY' : '未接通'}</span>
          </section>
        </div>

        <aside className="progress-card" aria-labelledby="progress-title">
          <p className="eyebrow">CONNECTION PATH</p>
          <h2 id="progress-title">端到端链路</h2>
          <ol className="progress-list">
            <li className={identity === null ? 'active' : 'complete'}>
              <span>{identity === null ? '1' : '✓'}</span>
              <div><strong>本地身份</strong><p>不可导出 P-256 密钥</p></div>
            </li>
            <li className={registration === null ? '' : 'complete'}>
              <span>{registration === null ? '2' : '✓'}</span>
              <div><strong>Platform 注册</strong><p>Human + Endpoint 身份</p></div>
            </li>
            <li className={gatewayReady ? 'complete' : ''}>
              <span>{gatewayReady ? '✓' : '3'}</span>
              <div><strong>安全连接</strong><p>DPoP + Ticket + Challenge</p></div>
            </li>
            <li>
              <span>4</span>
              <div><strong>ACP Session</strong><p>端到端密文 Prompt</p></div>
            </li>
          </ol>
          <div className="metadata-note">
            <strong>Platform 可见</strong>
            <p>连接、时间、大小和路由元数据</p>
          </div>
        </aside>
      </section>
    </main>
  );
}
