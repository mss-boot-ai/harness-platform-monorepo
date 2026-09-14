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
  const stopped = useRef(false);
  const retries = useRef(0);
  const retryTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reconnect = useRef<(automatic?: boolean) => Promise<void>>(async () => undefined);
  const schedule = useCallback(() => {
    if (!mounted.current || stopped.current || retryTimer.current !== null) return;
    if (retries.current >= 6) { setError('自动重连暂未成功，历史与草稿仍然保留。请检查网络或重新登录后再连接。'); return; }
    const delay = Math.min(15_000, 500 * 2 ** retries.current++);
    setError('连接中断，正在自动恢复；不会重新发送新的任务。');
    retryTimer.current = setTimeout(() => { retryTimer.current = null; void reconnect.current(true); }, delay);
  }, []);
  useEffect(() => { mounted.current = true; return () => {
    mounted.current = false; stopped.current = true; attempt.current += 1;
    if (retryTimer.current !== null) clearTimeout(retryTimer.current);
    socket.current?.close(1000, 'HC page detached');
  }; }, []);
  const issue = useCallback(async (automatic = false) => {
    if (!automatic) { stopped.current = false; retries.current = 0; }
    if (retryTimer.current !== null) { clearTimeout(retryTimer.current); retryTimer.current = null; }
    const id = ++attempt.current; const current = () => mounted.current && id === attempt.current;
    setBusy(true); setError(null); socket.current?.close(1000, 'HC reconnecting'); socket.current = null; setConnection(null); onReady(null);
    try {
      const ready = await access.connect(store);
      if (!current()) { ready.socket.close(1000, 'Superseded connection'); return; }
      const connectedAt = Date.now();
      ready.socket.addEventListener('close', (event: CloseEvent) => {
        if (mounted.current && socket.current === ready.socket) {
          socket.current = null; setConnection(null); onReady(null);
          if (Date.now() - connectedAt >= 30_000) retries.current = 0;
          if (![1000, 1002, 1003, 1007].includes(event.code)) schedule();
        }
      }, { once: true });
      socket.current = ready.socket; setConnection(ready); onReady(ready);
    } catch (cause) {
      if (current()) setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '无法建立安全连接，请检查网络、登录和浏览器端点状态。');
      if (current() && (cause instanceof TypeError || cause instanceof HcApiError && (cause.status >= 500 || cause.status === 429))) schedule();
    } finally { if (current()) setBusy(false); }
  }, [access, store, onReady, schedule]);
  useEffect(() => { reconnect.current = issue; }, [issue]);
  useEffect(() => { const timer = setTimeout(() => { void issue(); }, 0); return () => clearTimeout(timer); }, [issue]);
  const disconnect = () => {
    stopped.current = true;
    if (retryTimer.current !== null) { clearTimeout(retryTimer.current); retryTimer.current = null; }
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
