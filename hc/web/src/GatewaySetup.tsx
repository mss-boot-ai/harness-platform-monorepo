import { connectGateway, type EndpointIdentity, type ReadyGatewayConnection } from '@harness/hc-core';
import { useEffect, useRef, useState } from 'react';
import {
  HcApiError,
  fetchTrustManifest,
  issueWebSocketTicket,
  type RegistrationSession,
  type WebSocketTicket,
} from './api';

export function GatewaySetup({
  identity,
  registration,
}: {
  readonly identity: EndpointIdentity;
  readonly registration: RegistrationSession;
}) {
  const [ticket, setTicket] = useState<WebSocketTicket | null>(null);
  const [connection, setConnection] = useState<ReadyGatewayConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const socketRef = useRef<WebSocket | null>(null);

  useEffect(() => () => socketRef.current?.close(1000, 'HC page closed'), []);

  const issue = async () => {
    setBusy(true);
    setError(null);
    try {
      socketRef.current?.close(1000, 'HC reconnecting');
      const issued = await issueWebSocketTicket(identity, registration);
      setTicket(issued);
      const trust = await fetchTrustManifest();
      const ready = await connectGateway(identity, {
        credentialId: registration.credentialId,
        endpointId: registration.endpointId,
        ticket: issued.ticket,
        websocketUrl: issued.websocketUrl,
      }, trust);
      socketRef.current = ready.socket;
      setConnection(ready);
    } catch (cause) {
      if (cause instanceof HcApiError) {
        setError(`${cause.message}（${cause.code}）`);
      } else {
        setError('Gateway Ticket 获取失败，请确认本地 Gateway 已启动。');
      }
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="gateway-card" aria-labelledby="gateway-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">STEP 3 OF 4</p>
          <h2 id="gateway-title">准备安全连接</h2>
        </div>
        <span className={`status-pill ${connection === null ? 'pending' : 'success'}`}>
          {connection === null ? (ticket === null ? '需要 Ticket' : '正在握手') : 'CONNECTION READY'}
        </span>
      </div>
      <p className="platform-copy">
        {connection !== null
          ? `连接已通过双向签名挑战，Generation ${connection.connectionGeneration}；Root ${connection.rootJkt.slice(0, 10)}…`
          : ticket === null
          ? 'Gateway 将先签发 Server Nonce，再验证 DPoP proof 并生成 30 秒单次 Ticket。'
          : `Ticket 已签发，有效至 ${new Date(ticket.expiresAt).toLocaleTimeString()}；正在完成二进制签名挑战。`}
      </p>
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      <button className="primary-button gateway-button" type="button" disabled={busy} onClick={() => void issue()}>
        {busy ? '正在验证并握手…' : connection === null ? '获取 Ticket 并安全连接' : '重新建立安全连接'}
      </button>
    </section>
  );
}
