import { useState, type FormEvent } from 'react';
import { HcApiError, type RegistrationSession } from './api';
import type { EndpointAccess } from './remote/endpoint-access';
interface PlatformSetupProps {
  readonly access: EndpointAccess;
  readonly registration: RegistrationSession | null;
  readonly onReauthenticate: () => Promise<void>;
  readonly onReplaceIdentity: () => Promise<void>;
}
export function PlatformSetup({ access, registration, onReauthenticate, onReplaceIdentity }: PlatformSetupProps) {
  const [username, setUsername] = useState(''); const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false); const [error, setError] = useState<string | null>(null);
  const [revoked, setRevoked] = useState(false);
  const [confirmReplacement, setConfirmReplacement] = useState(false);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    if (username.trim() === '' || password === '') { setError('请输入用户名和密码。'); return; }
    setBusy(true); setError(null);
    try {
      await access.login(username.trim(), password); setPassword('');
    } catch (cause) {
      setRevoked(cause instanceof HcApiError && cause.code === 'HARNESS_REVOKED');
      setPassword(''); setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '暂时无法登录，请检查账户和网络后重试。');
    } finally { setBusy(false); }
  };
  const restore = async () => {
    if (busy) return;
    setBusy(true); setError(null);
    try { await access.refresh(); }
    catch (cause) { setError(cause instanceof HcApiError ? `${cause.message}（${cause.code}）` : '登录已失效，请重新登录。'); }
    finally { setBusy(false); }
  };
  if (registration !== null) return <section className="platform-card"><div className="section-heading"><h3>账户</h3><span className="status-pill success">已连接</span></div><p>当前浏览器已绑定账户。</p><button className="secondary-button" type="button" onClick={() => void onReauthenticate()}>重新登录</button><details className="technical-details"><summary>注册详情</summary><p>Endpoint {registration.endpointId}</p></details></section>;
  return <section className="platform-card" aria-labelledby="platform-title"><h3 id="platform-title">2. 登录 Harness</h3><p>使用你已有的 Platform 账户。</p>
    <form className="login-form" onSubmit={(event) => void submit(event)}>
      <label>用户名<input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} disabled={busy} required /></label>
      <label>密码<input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} disabled={busy} required /></label>
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      <button className="primary-button" type="submit" disabled={busy}>{busy ? '正在登录…' : '登录并连接'}</button>
      <button className="secondary-button" type="button" disabled={busy} onClick={() => void restore()}>恢复已有登录</button>
      <p className="fine-print">密码只发送到同源登录接口，不保存到浏览器存储。</p>
    </form>
    {revoked ? <section className="identity-recovery" role="status"><p>此浏览器身份已被吊销。登记新身份后可以开始新对话；旧身份和加密记录会保留，不会自动恢复已吊销的访问权限。</p>
      {!confirmReplacement ? <button className="secondary-button" type="button" onClick={() => setConfirmReplacement(true)}>登记新的浏览器身份</button>
        : <><button className="primary-button" type="button" disabled={busy} onClick={() => { setBusy(true); void onReplaceIdentity().finally(() => setBusy(false)); }}>确认登记新身份</button><button className="secondary-button" type="button" onClick={() => setConfirmReplacement(false)}>取消</button></>}
    </section> : null}
  </section>;
}
