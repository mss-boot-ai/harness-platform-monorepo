import { IndexedDbSecureStore, type EndpointIdentity, type ReadyGatewayConnection, type SecureStoreProbe } from '@harness/hc-core';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { PlatformSetup } from './PlatformSetup';
import { GatewaySetup } from './GatewaySetup';
import { SessionSetup } from './SessionSetup';
import { refreshEndpointSession, type RegistrationSession } from './api';
import { ChatWorkspace } from './chat/ChatWorkspace';
import { Dialog } from './chat/Dialog';

const PRIMARY_INSTALLATION_ID = 'primary-browser-installation';
export function App() {
  const store = useMemo(() => globalThis.indexedDB === undefined ? null : new IndexedDbSecureStore(globalThis.indexedDB), []);
  const [probe, setProbe] = useState<SecureStoreProbe | null>(null);
  const [identity, setIdentity] = useState<EndpointIdentity | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [registration, setRegistration] = useState<RegistrationSession | null>(null);
  // Keep the last ready transport object across disconnects: unmounting SessionSetup destroys its keys.
  const [connection, setConnection] = useState<ReadyGatewayConnection | null>(null);
  const [online, setOnline] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const openSettings = useCallback(() => setSettingsOpen(true), []);
  const acceptConnection = useCallback((value: ReadyGatewayConnection | null) => { if (value !== null) setConnection(value); }, []);
  useEffect(() => {
    let active = true;
    const inspect = async () => {
      if (globalThis.crypto?.subtle === undefined || store === null) {
        if (active) setProbe({ assurance: 'unsupported', detail: '此浏览器缺少安全存储能力。请使用支持 WebCrypto 和 IndexedDB 的浏览器。', supported: false });
        return;
      }
      const existing = await store.load(PRIMARY_INSTALLATION_ID);
      const result = await store.probe();
      if (!active) return;
      setProbe(result); setIdentity(existing);
      if (existing !== null && result.supported) {
        // Recovery uses the existing HttpOnly cookie + endpoint proof; no localStorage token.
        try { const restored = await refreshEndpointSession(existing); if (active) setRegistration(restored); }
        catch { /* Expired login is handled by the explicit sign-in form. Never retry a prompt here. */ }
      }
    };
    void inspect().catch(() => { if (active) setProbe({ assurance: 'unsupported', detail: '无法验证安全存储，不会降级保存私钥。请检查浏览器设置。', supported: false }); });
    return () => { active = false; };
  }, [store]);
  useEffect(() => {
    if (connection === null) return;
    const socket = connection.socket;
    const update = () => setOnline(socket.readyState === WebSocket.OPEN);
    update(); socket.addEventListener('close', update); socket.addEventListener('error', update);
    return () => { socket.removeEventListener('close', update); socket.removeEventListener('error', update); };
  }, [connection]);
  const createIdentity = async () => {
    if (store === null || probe?.supported !== true || busy) return;
    setBusy(true); setError(null);
    try { setIdentity(await store.create(PRIMARY_INSTALLATION_ID, 'This browser')); }
    catch { setError('无法创建安全浏览器身份。没有导出或明文保存私钥。'); }
    finally { setBusy(false); }
  };
  return <>
    {identity !== null && registration !== null && connection !== null ? <SessionSetup connection={connection} identity={identity}
      onRegistration={setRegistration} registration={registration} secureStore={store} draft={draft} onDraftChange={setDraft} onOpenSettings={openSettings} online={online} />
      : <ChatWorkspace draft={draft} onDraftChange={setDraft} onSubmit={openSettings} onNewChat={() => setDraft('')}
        onEndChat={null} onOpenSettings={openSettings} onSelectConversation={() => undefined} conversations={[]} selectedConversationId={null}
        messages={[]} title="新对话" agent="" online={false} connected={false} busy={false} responding={false} canSubmit readOnly={false}
        hasActiveSession={false} error={null} notice={null} targetSettings={null} />}
    <Dialog open={settingsOpen} onClose={() => setSettingsOpen(false)} title="连接与设置">
      <p className="settings-intro">连接一次，就可以在这里与自己的 Agent 对话。安全设置不会占用聊天空间。</p>
      {identity === null ? <section className="setup-card"><div className="section-heading"><h3>1. 准备当前浏览器</h3><span className="status-pill">{probe === null ? '检查中' : probe.supported ? '可使用' : '不支持'}</span></div>
        <p>{probe?.detail ?? '正在检查浏览器的安全存储…'}</p>
        <button className="primary-button" type="button" disabled={busy || probe?.supported !== true} onClick={() => void createIdentity()}>{busy ? '正在准备…' : '启用安全浏览器身份'}</button>
        <p className="fine-print">私钥仅在此浏览器生成，不以明文保存。</p>{error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      </section> : <>
        <section className="identity-card"><div className="section-heading"><h3>浏览器身份</h3><span className="status-pill success">已就绪</span></div><details className="technical-details"><summary>查看安全详情</summary><dl><dt>保护等级</dt><dd>{identity.assurance}</dd><dt>签名指纹</dt><dd>{identity.signing.thumbprint}</dd><dt>加密指纹</dt><dd>{identity.kem.thumbprint}</dd></dl></details></section>
        <PlatformSetup identity={identity} registration={registration} onRegistered={setRegistration} />
        {registration !== null ? <GatewaySetup identity={identity} onRegistration={setRegistration} onReady={acceptConnection} /> : null}
      </>}
      <div className="settings-privacy"><strong>关于对话隐私</strong><p>会话内容在浏览器和执行端之间加密。侧边栏标题与本页阅读记录不保存到 Local Storage；刷新页面后不保证可恢复。</p></div>
      {online ? <button type="button" className="primary-button settings-done" onClick={() => setSettingsOpen(false)}>返回对话</button> : null}
    </Dialog>
  </>;
}
