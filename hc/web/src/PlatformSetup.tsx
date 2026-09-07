import type { EndpointIdentity } from '@harness/hc-core';
import { useState, type FormEvent } from 'react';
import {
  HcApiError,
  loginBrowserSession,
  refreshEndpointSession,
  registerBrowserEndpoint,
  type RegistrationSession,
} from './api';

interface PlatformSetupProps {
  readonly identity: EndpointIdentity;
  readonly onRegistered: (session: RegistrationSession) => void;
  readonly registration: RegistrationSession | null;
}

export function PlatformSetup({ identity, onRegistered, registration }: PlatformSetupProps) {
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (username.trim() === '' || password === '') {
      setError('请输入 Platform 用户名和密码。');
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await loginBrowserSession(username.trim(), password);
      setPassword('');
      onRegistered(await registerBrowserEndpoint(identity, 'H5 browser'));
    } catch (cause) {
      setPassword('');
      if (cause instanceof HcApiError) {
        setError(`${cause.message}（${cause.code}）`);
      } else {
        setError('Platform 注册失败，请确认 Platform 服务与网络状态。');
      }
    } finally {
      setBusy(false);
    }
  };

  const restore = async () => {
    setBusy(true);
    setError(null);
    try {
      onRegistered(await refreshEndpointSession(identity));
    } catch (cause) {
      if (cause instanceof HcApiError) {
        setError(`${cause.message}（${cause.code}）`);
      } else {
        setError('端点恢复失败，请重新登录注册。');
      }
    } finally {
      setBusy(false);
    }
  };

  if (registration !== null) {
    return (
      <section className="platform-card registered-card" aria-labelledby="platform-title">
        <div>
          <p className="eyebrow">STEP 2 OF 4</p>
          <h2 id="platform-title">Platform 注册完成</h2>
          <p>Endpoint {registration.endpointId.slice(0, 10)}… 已绑定；Access Token 仅驻留当前页面内存。</p>
        </div>
        <span className="status-pill success">REGISTERED</span>
      </section>
    );
  }

  return (
    <section className="platform-card" aria-labelledby="platform-title">
      <div className="section-heading">
        <div>
          <p className="eyebrow">STEP 2 OF 4</p>
          <h2 id="platform-title">登录并注册到 Platform</h2>
        </div>
        <span className="status-pill pending">需要登录</span>
      </div>
      <p className="platform-copy">密码仅发送到同源 Admin Session 登录接口，不保存到浏览器存储。</p>
      <form className="login-form" onSubmit={(event) => void submit(event)}>
        <label>
          <span>用户名</span>
          <input
            autoComplete="username"
            value={username}
            onChange={(event) => setUsername(event.target.value)}
            disabled={busy}
          />
        </label>
        <label>
          <span>密码</span>
          <input
            autoComplete="current-password"
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            disabled={busy}
          />
        </label>
        {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
        <button className="primary-button" type="submit" disabled={busy}>
          {busy ? '正在建立身份…' : '登录并注册此端点'}
        </button>
        <button className="secondary-button restore-button" type="button" disabled={busy} onClick={() => void restore()}>
          恢复已注册端点
        </button>
      </form>
    </section>
  );
}
