import type { EndpointIdentity } from '@harness/hc-core';
import { useEffect, useState } from 'react';
import {
  createEndpointSession,
  getEndpointSession,
  HcApiError,
  listABAEndpoints,
  type ABAEndpointSummary,
  type EndpointSessionSummary,
  type RegistrationSession,
} from './api';

const POLL_ATTEMPTS = 20;

export function SessionSetup({
  identity,
  registration,
}: {
  readonly identity: EndpointIdentity;
  readonly registration: RegistrationSession;
}) {
  const [endpoints, setEndpoints] = useState<readonly ABAEndpointSummary[]>([]);
  const [selectedABA, setSelectedABA] = useState('');
  const [runtimeProfileId, setRuntimeProfileId] = useState('test-agent');
  const [workspaceId, setWorkspaceId] = useState('harness-platform');
  const [session, setSession] = useState<EndpointSessionSummary | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let active = true;
    void listABAEndpoints()
      .then((values) => {
        if (active) {
          setEndpoints(values);
          setSelectedABA(values[0]?.id ?? '');
        }
      })
      .catch(() => {
        if (active) {
          setError('无法读取当前用户的 ABA Endpoint。');
        }
      });
    return () => {
      active = false;
    };
  }, []);

  const create = async () => {
    if (selectedABA === '') {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      let current = await createEndpointSession(identity, registration, {
        abaEndpointId: selectedABA,
        idempotencyKey: crypto.randomUUID(),
        runtimeProfileId: runtimeProfileId.trim(),
        workspaceId: workspaceId.trim(),
      });
      setSession(current);
      for (let attempt = 0; attempt < POLL_ATTEMPTS && current.status === 'CREATING'; attempt += 1) {
        await new Promise((resolve) => setTimeout(resolve, 250));
        current = await getEndpointSession(current.sessionId);
        setSession(current);
      }
    } catch (cause) {
      setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : 'Session 创建失败。');
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="session-card" aria-labelledby="session-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">STEP 4 OF 4</p>
          <h2 id="session-title">创建 ACP Session</h2>
        </div>
        <span className={`status-pill ${session?.status === 'WAITING_KEY' ? 'success' : 'pending'}`}>
          {session?.status ?? '需要本地策略'}
        </span>
      </div>
      <p className="platform-copy">
        Platform 只发送 ABA、Runtime 和 Workspace 的稳定 ID；真实命令、参数和路径只由 ABA 本地配置决定。
      </p>
      <div className="session-form">
        <label>
          ABA Endpoint
          <select value={selectedABA} onChange={(event) => setSelectedABA(event.target.value)}>
            {endpoints.map((endpoint) => (
              <option key={endpoint.id} value={endpoint.id}>{endpoint.name} · {endpoint.id.slice(0, 8)}</option>
            ))}
          </select>
        </label>
        <label>
          Runtime ID
          <input value={runtimeProfileId} onChange={(event) => setRuntimeProfileId(event.target.value)} />
        </label>
        <label>
          Workspace ID
          <input value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)} />
        </label>
        <button
          className="primary-button"
          type="button"
          disabled={busy || selectedABA === '' || runtimeProfileId.trim() === '' || workspaceId.trim() === ''}
          onClick={() => void create()}
        >
          {busy ? '正在等待 ABA 本地策略…' : '创建 Session'}
        </button>
      </div>
      {endpoints.length === 0 && error === null ? <p className="fine-print">没有可用的 ACTIVE ABA Endpoint。</p> : null}
      {session === null ? null : (
        <p className="session-result">
          Session {session.sessionId.slice(0, 10)}… · {session.status}
        </p>
      )}
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
    </section>
  );
}
