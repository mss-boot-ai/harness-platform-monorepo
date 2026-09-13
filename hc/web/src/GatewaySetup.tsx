import { connectGateway, IndexedDbSecureStore, type EndpointIdentity, type ReadyGatewayConnection } from '@harness/hc-core';
import { useCallback, useEffect, useRef, useState } from 'react';
import { HcApiError, fetchTrustManifest, issueWebSocketTicket, refreshEndpointSession, type RegistrationSession } from './api';
export function GatewaySetup({ identity, onRegistration, onReady }: {
  readonly identity: EndpointIdentity; readonly onRegistration: (registration: RegistrationSession) => void;
  readonly onReady: (ready: ReadyGatewayConnection | null) => void;
}) {
  const [connection, setConnection] = useState<ReadyGatewayConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const socketRef = useRef<WebSocket | null>(null);
  const attemptRef = useRef(0);
  const mounted = useRef(false);
  useEffect(() => {
    mounted.current = true;
    return () => { mounted.current = false; attemptRef.current += 1; socketRef.current?.close(1000, 'HC page closed'); };
  }, []);
  const issue = useCallback(async () => {
    const attempt = ++attemptRef.current;
    const current = () => mounted.current && attempt === attemptRef.current;
    setBusy(true); setError(null);
    socketRef.current?.close(1000, 'HC reconnecting'); socketRef.current = null; setConnection(null);
    try {
      const registration = await refreshEndpointSession(identity);
      const issued = await issueWebSocketTicket(identity, registration);
      const trust = await fetchTrustManifest();
      if (!current()) return;
      if (globalThis.indexedDB === undefined) throw new Error('Trust pin storage unavailable');
      await new IndexedDbSecureStore(globalThis.indexedDB).pinTrustRoot(trust.rootJkt, trust.revision, trust.onlineJkt, trust.expiresAt);
      if (!current()) return;
      const ready = await connectGateway(identity, { credentialId: registration.credentialId,
        endpointId: registration.endpointId, ticket: issued.ticket, websocketUrl: issued.websocketUrl }, trust);
      if (!current()) { ready.socket.close(1000, 'Superseded connection'); return; }
      ready.socket.addEventListener('close', () => {
        if (socketRef.current === ready.socket) { socketRef.current = null; setConnection(null); }
      }, { once: true });
      socketRef.current = ready.socket; setConnection(ready); onRegistration(registration); onReady(ready);
    } catch (cause) {
      if (current()) setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '暂时无法建立安全连接，请检查服务和网络后重试。');
    } finally { if (current()) setBusy(false); }
  }, [identity, onRegistration, onReady]);
  useEffect(() => {
    // Defer to avoid duplicate refresh/token rotation during React StrictMode's initial effect probe.
    const timer = setTimeout(() => { void issue(); }, 0);
    return () => clearTimeout(timer);
  }, [issue]);
  const disconnect = () => {
    attemptRef.current += 1;
    socketRef.current?.close(1000, 'HC user disconnected'); socketRef.current = null;
    setConnection(null); setBusy(false); setError(null);
  };
  return <section className="gateway-card" aria-labelledby="gateway-title">
    <div className="section-heading"><h3 id="gateway-title">安全连接</h3><span className={`status-pill ${connection === null ? '' : 'success'}`}>{busy ? '连接中' : connection === null ? '未连接' : '已连接'}</span></div>
    <p>{connection === null ? '登录后会自动连接。断线时，本页会话和草稿不会被清空。' : '已完成安全验证，可以返回对话。'}</p>
    {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
    <button className="primary-button" type="button" disabled={busy} onClick={() => void issue()}>{busy ? '正在连接…' : connection === null ? '重新连接' : '重新建立连接'}</button>
    {connection !== null ? <><button className="secondary-button" type="button" disabled={busy} onClick={disconnect}>断开连接（保留本页会话）</button><details className="technical-details"><summary>连接诊断</summary><p>Generation {connection.connectionGeneration.toString()}<br />Root {connection.rootJkt}</p></details></> : null}
  </section>;
}
