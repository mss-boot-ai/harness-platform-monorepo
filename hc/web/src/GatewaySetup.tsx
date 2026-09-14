import { type IndexedDbSecureStore, type ReadyGatewayConnection } from '@harness/hc-core';
import { useCallback, useEffect, useRef, useState } from 'react';
import { HcApiError } from './api';
import type { EndpointAccess } from './remote/endpoint-access';
export function GatewaySetup({ access, store, onReady }: {
  readonly access: EndpointAccess; readonly store: IndexedDbSecureStore;
  readonly onReady: (ready: ReadyGatewayConnection | null) => void;
}) {
  const [connection, setConnection] = useState<ReadyGatewayConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const socket = useRef<WebSocket | null>(null);
  const attempt = useRef(0);
  const mounted = useRef(false);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; attempt.current += 1; socket.current?.close(1000, 'HC page detached'); }; }, []);
  const issue = useCallback(async () => {
    const id = ++attempt.current; const current = () => mounted.current && id === attempt.current;
    setBusy(true); setError(null); socket.current?.close(1000, 'HC reconnecting'); socket.current = null; setConnection(null); onReady(null);
    try {
      const ready = await access.connect(store);
      if (!current()) { ready.socket.close(1000, 'Superseded connection'); return; }
      ready.socket.addEventListener('close', () => {
        if (mounted.current && socket.current === ready.socket) { socket.current = null; setConnection(null); onReady(null); }
      }, { once: true });
      socket.current = ready.socket; setConnection(ready); onReady(ready);
    } catch (cause) {
      if (current()) setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '无法建立安全连接，请检查网络、登录和浏览器端点状态。');
    } finally { if (current()) setBusy(false); }
  }, [access, store, onReady]);
  useEffect(() => { const timer = setTimeout(() => { void issue(); }, 0); return () => clearTimeout(timer); }, [issue]);
  const disconnect = () => {
    attempt.current += 1; socket.current?.close(1000, 'HC user disconnected'); socket.current = null;
    setConnection(null); onReady(null); setBusy(false); setError(null);
  };
  return <section className="gateway-card" aria-labelledby="gateway-title">
    <div className="section-heading"><h3 id="gateway-title">安全连接</h3><span className={`status-pill ${connection === null ? '' : 'success'}`}>{busy ? '连接中' : connection === null ? '未连接' : '已连接'}</span></div>
    <p>{connection === null ? '登录后会自动连接。断线时，加密历史和草稿仍保留在此浏览器。' : '已完成安全验证，可以返回对话。'}</p>
    {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
    <button className="primary-button" type="button" disabled={busy} onClick={() => void issue()}>{busy ? '正在连接…' : connection === null ? '重新连接' : '重新建立连接'}</button>
    {connection !== null ? <><button className="secondary-button" type="button" disabled={busy} onClick={disconnect}>断开连接（保留会话）</button><details className="technical-details"><summary>连接诊断</summary><p>Generation {connection.connectionGeneration.toString()}<br />Root {connection.rootJkt}</p></details></> : null}
  </section>;
}
