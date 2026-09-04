import type { EndpointIdentity } from '@harness/hc-core';
import { useState } from 'react';
import {
  HcApiError,
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
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const issue = async () => {
    setBusy(true);
    setError(null);
    try {
      setTicket(await issueWebSocketTicket(identity, registration));
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
        <span className={`status-pill ${ticket === null ? 'pending' : 'success'}`}>
          {ticket === null ? '需要 Ticket' : 'TICKET ISSUED'}
        </span>
      </div>
      <p className="platform-copy">
        {ticket === null
          ? 'Gateway 将先签发 Server Nonce，再验证 DPoP proof 并生成 30 秒单次 Ticket。'
          : `Ticket 已签发，有效至 ${new Date(ticket.expiresAt).toLocaleTimeString()}；原值未显示或持久化。`}
      </p>
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      <button className="primary-button gateway-button" type="button" disabled={busy} onClick={() => void issue()}>
        {busy ? '正在验证 DPoP…' : ticket === null ? '获取一次性连接 Ticket' : '重新签发 Ticket'}
      </button>
    </section>
  );
}
