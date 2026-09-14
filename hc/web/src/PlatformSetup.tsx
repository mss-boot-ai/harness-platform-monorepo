import { useState, type FormEvent } from 'react';
import { HcApiError, type RegistrationSession } from './api';
import type { EndpointAccess } from './remote/endpoint-access';
interface PlatformSetupProps {
  readonly access: EndpointAccess;
  readonly registration: RegistrationSession | null;
}
export function PlatformSetup({ access, registration }: PlatformSetupProps) {
  const [username, setUsername] = useState(''); const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false); const [error, setError] = useState<string | null>(null);
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (busy) return;
    if (username.trim() === '' || password === '') { setError('请输入用户名和密码。'); return; }
    setBusy(true); setError(null);
    try {
      await access.login(username.trim(), password); setPassword('');
    } catch (cause) {
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
  if (registration !== null) return <section className="platform-card"><div className="section-heading"><h3>账户</h3><span className="status-pill success">已连接</span></div><p>当前浏览器已绑定账户，凭据仅在内存和安全 Cookie 中使用。</p><details className="technical-details"><summary>注册详情</summary><p>Endpoint {registration.endpointId}</p></details></section>;
  return <section className="platform-card" aria-labelledby="platform-title"><h3 id="platform-title">2. 登录 Harness</h3><p>使用你已有的 Platform 账户。</p>
    <form className="login-form" onSubmit={(event) => void submit(event)}>
      <label>用户名<input autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} disabled={busy} required /></label>
      <label>密码<input autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} disabled={busy} required /></label>
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      <button className="primary-button" type="submit" disabled={busy}>{busy ? '正在登录…' : '登录并连接'}</button>
      <button className="secondary-button" type="button" disabled={busy} onClick={() => void restore()}>恢复已有登录</button>
      <p className="fine-print">密码只发送到同源登录接口，不保存到浏览器存储。</p>
    </form>
  </section>;
}
