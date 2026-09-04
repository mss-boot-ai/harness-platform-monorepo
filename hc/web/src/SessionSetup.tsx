import {
  createSessionKeyPackageAckPacket,
  openSessionKeyPackagePacket,
  type EndpointIdentity,
  type OpenedSessionKeyPackage,
  type ReadyGatewayConnection,
} from '@harness/hc-core';
import { useEffect, useRef, useState } from 'react';
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
  connection,
  identity,
  registration,
}: {
  readonly connection: ReadyGatewayConnection;
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
  const [keyReady, setKeyReady] = useState(false);
  const pendingPackets = useRef<Uint8Array[]>([]);
  const processing = useRef<Promise<void>>(Promise.resolve());
  const openedPackage = useRef<OpenedSessionKeyPackage | null>(null);
  const lastPackageSequence = useRef(0n);
  const outboundControlSequence = useRef(0n);

  useEffect(() => {
    const processPacket = async (encoded: Uint8Array) => {
      if (session === null) {
        pendingPackets.current.push(encoded);
        return;
      }
      const aba = endpoints.find((endpoint) => endpoint.id === session.abaEndpointId);
      if (aba === undefined) {
        throw new Error('Session ABA identity is unavailable');
      }
      const opened = await openSessionKeyPackagePacket(encoded, {
        abaEndpointId: aba.id,
        abaSigningPublicJwk: aba.signingPublicJwk,
        hcEndpointId: registration.endpointId,
        identity,
        sessionId: session.sessionId,
      });
      if (opened === null) {
        return;
      }
      if (opened.controlSequence <= lastPackageSequence.current) {
        throw new Error('SessionKeyPackage control sequence replayed');
      }
      zeroOpenedPackage(openedPackage.current);
      openedPackage.current = opened;
      lastPackageSequence.current = opened.controlSequence;
      setKeyReady(true);
      outboundControlSequence.current += 1n;
      const acknowledgment = await createSessionKeyPackageAckPacket(identity, {
        abaEndpointId: aba.id,
        controlSequence: outboundControlSequence.current,
        hcEndpointId: registration.endpointId,
        keyPackageId: opened.keyPackageId,
        sessionId: session.sessionId,
      });
      if (connection.socket.readyState !== WebSocket.OPEN) {
        throw new Error('Gateway connection closed before key acknowledgment');
      }
      connection.socket.send(new Uint8Array(acknowledgment).buffer);
      for (let attempt = 0; attempt < POLL_ATTEMPTS; attempt += 1) {
        const current = await getEndpointSession(session.sessionId);
        setSession(current);
        if (current.status === 'ACTIVE' || current.status === 'FAILED') {
          break;
        }
        await new Promise((resolve) => setTimeout(resolve, 250));
      }
    };
    const schedule = (encoded: Uint8Array) => {
      processing.current = processing.current
        .then(() => processPacket(encoded))
        .catch(() => {
          setError('Session Key Package 验证失败。');
        });
    };
    const message = (event: MessageEvent) => {
      if (event.data instanceof ArrayBuffer && event.data.byteLength <= 1_048_576) {
        schedule(new Uint8Array(event.data));
      }
    };
    connection.socket.addEventListener('message', message);
    if (session !== null) {
      const queued = pendingPackets.current.splice(0);
      for (const encoded of queued) {
        schedule(encoded);
      }
    }
    return () => connection.socket.removeEventListener('message', message);
  }, [connection.socket, endpoints, identity, registration.endpointId, session]);

  useEffect(() => () => zeroOpenedPackage(openedPackage.current), []);

  useEffect(() => {
    let active = true;
    void listABAEndpoints(identity, registration)
      .then((values) => {
        if (active) {
          setEndpoints(values);
          setSelectedABA(values[0]?.id ?? '');
        }
      })
      .catch((cause: unknown) => {
        if (active) {
          setError(
            cause instanceof HcApiError
              ? `${cause.message}（${cause.code}）`
              : '无法读取当前用户的 ABA Endpoint。',
          );
        }
      });
    return () => {
      active = false;
    };
  }, [identity, registration]);

  const create = async () => {
    if (selectedABA === '') {
      return;
    }
    setBusy(true);
    setError(null);
    setKeyReady(false);
    zeroOpenedPackage(openedPackage.current);
    openedPackage.current = null;
    lastPackageSequence.current = 0n;
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
        <span className={`status-pill ${keyReady ? 'success' : 'pending'}`}>
          {keyReady ? 'KEY READY' : session?.status ?? '需要本地策略'}
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
          {keyReady ? ' · HPKE KEY READY' : ''}
        </p>
      )}
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
    </section>
  );
}

function zeroOpenedPackage(value: OpenedSessionKeyPackage | null): void {
  if (value === null) {
    return;
  }
  value.hcToAbaKey.fill(0);
  value.abaToHcKey.fill(0);
  value.material.srk.fill(0);
  value.material.sessionNonce.fill(0);
}
