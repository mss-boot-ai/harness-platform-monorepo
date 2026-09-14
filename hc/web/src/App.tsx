import { IndexedDbSecureStore, type EndpointIdentity, type ReadyGatewayConnection, type SecureStoreProbe } from '@harness/hc-core';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { PlatformSetup } from './PlatformSetup';
import { GatewaySetup } from './GatewaySetup';
import { SessionSetup } from './SessionSetup';
import type { RegistrationSession } from './api';
import { ChatWorkspace } from './chat/ChatWorkspace';
import { Dialog } from './chat/Dialog';
import { EndpointAccess } from './remote/endpoint-access';
import { ownEndpoint, type EndpointOwnerState, type RegisterEndpointShutdown } from './remote/endpoint-owner';

const PRIMARY_INSTALLATION_ID = 'primary-browser-installation';
function Welcome({ draft, onDraftChange, onSettings, notice = null, enabled = true }: {
  readonly draft: string; readonly onDraftChange: (value: string) => void; readonly onSettings: () => void;
  readonly notice?: string | null; readonly enabled?: boolean;
}) {
  return <ChatWorkspace draft={draft} onDraftChange={onDraftChange} onSubmit={onSettings} onNewChat={() => onDraftChange('')}
    onEndChat={null} onOpenSettings={onSettings} onSelectConversation={() => undefined} conversations={[]} selectedConversationId={null}
    messages={[]} title="新对话" agent="" online={false} connected={false} busy={false} responding={false} canSubmit={enabled} readOnly={false}
    hasActiveSession={false} error={null} notice={notice} targetSettings={null} />;
}
export function App() {
  const [ownership, setOwnership] = useState<EndpointOwnerState>('waiting');
  const [draft, setDraft] = useState('');
  const owned = useRef(false);
  const shutdowns = useRef(new Set<() => Promise<void>>());
  const isOwned = useCallback(() => owned.current, []);
  const registerShutdown = useCallback<RegisterEndpointShutdown>((shutdown) => {
    shutdowns.current.add(shutdown); return () => { shutdowns.current.delete(shutdown); };
  }, []);
  useEffect(() => {
    let active = true;
    const handle = ownEndpoint(navigator.locks, (state) => {
      if (!active) return;
      owned.current = state === 'owned'; setOwnership(state);
    });
    return () => {
      active = false; owned.current = false;
      // Fence APIs and transport, finish accepted storage writes, then hand the installation to another tab.
      void Promise.allSettled([...shutdowns.current].map((shutdown) => shutdown())).finally(() => handle.release());
    };
  }, []);
  if (ownership !== 'owned') return <Welcome draft={draft} onDraftChange={setDraft} onSettings={() => undefined} enabled={false}
    notice={ownership === 'unsupported' ? '此浏览器无法独占安全端点，已暂停登录与发送。请使用支持 Web Locks 的浏览器。'
      : '另一个标签页正在使用此浏览器的安全连接。关闭该标签页后，这里会先恢复已保存的记录，再接管连接。'} />;
  return <OwnedApp isOwned={isOwned} registerShutdown={registerShutdown} initialDraft={draft} />;
}
function OwnedApp({ isOwned, registerShutdown, initialDraft }: {
  readonly isOwned: () => boolean; readonly registerShutdown: RegisterEndpointShutdown; readonly initialDraft: string;
}) {
  const store = useMemo(() => globalThis.indexedDB === undefined ? null : new IndexedDbSecureStore(globalThis.indexedDB), []);
  const [probe, setProbe] = useState<SecureStoreProbe | null>(null);
  const [identity, setIdentity] = useState<EndpointIdentity | null>(null);
  const [access, setAccess] = useState<EndpointAccess | null>(null);
  const [registration, setRegistration] = useState<RegistrationSession | null>(null);
  const [connection, setConnection] = useState<ReadyGatewayConnection | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [draft, setDraft] = useState(initialDraft);
  const active = useRef(false);
  const accessRef = useRef<EndpointAccess | null>(null);
  const unregister = useRef<(() => void) | null>(null);
  const startupWork = useRef(new Set<Promise<unknown>>());
  const openSettings = useCallback(() => setSettingsOpen(true), []);
  const activate = useCallback((value: EndpointIdentity) => {
    if (!active.current || !isOwned()) return null;
    const client = new EndpointAccess(value, isOwned, (session) => { if (active.current && isOwned()) { setRegistration(session); setError(null); } });
    accessRef.current = client; unregister.current = registerShutdown(() => client.dispose());
    setIdentity(value); setAccess(client); return client;
  }, [isOwned, registerShutdown]);
  useEffect(() => registerShutdown(async () => { await Promise.allSettled(startupWork.current); }), [registerShutdown]);
  useEffect(() => {
    active.current = true; let current = true;
    const inspect = async () => {
      if (!isOwned()) return;
      if (globalThis.crypto?.subtle === undefined || store === null) {
        setProbe({ assurance: 'unsupported', detail: '此浏览器缺少安全存储能力。请使用支持 WebCrypto 和 IndexedDB 的浏览器。', supported: false }); return;
      }
      const existing = await store.load(PRIMARY_INSTALLATION_ID);
      if (!current || !isOwned()) return;
      const result = await store.probe();
      if (!current || !isOwned()) return;
      setProbe(result);
      if (existing !== null && result.supported) {
        const client = activate(existing);
        try { await client?.refresh(); }
        catch { if (current && isOwned()) setError('已有登录无法恢复。请在连接设置中重新登录；加密历史仍保留在当前浏览器。'); }
      }
    };
    const inspecting = inspect(); startupWork.current.add(inspecting);
    void inspecting.then(() => startupWork.current.delete(inspecting), () => startupWork.current.delete(inspecting));
    void inspecting.catch(() => { if (current && isOwned()) setProbe({ assurance: 'unsupported', detail: '无法验证安全存储。请检查浏览器设置，不会覆盖已有密钥或记录。', supported: false }); });
    return () => { current = false; active.current = false; unregister.current?.(); void accessRef.current?.dispose(); };
  }, [store, activate, isOwned]);
  const createIdentity = async () => {
    if (!isOwned() || store === null || probe?.supported !== true || busy) return;
    setBusy(true); setError(null);
    const creating = store.create(PRIMARY_INSTALLATION_ID, 'This browser'); startupWork.current.add(creating);
    try {
      const value = await creating;
      if (active.current && isOwned()) activate(value);
    } catch { if (active.current) setError('无法创建安全浏览器身份。请检查本地存储空间。'); }
    finally { startupWork.current.delete(creating); if (active.current) setBusy(false); }
  };
  const online = connection?.socket.readyState === 1;
  return <>
    {identity !== null && access !== null && registration !== null && store !== null
      ? <SessionSetup connection={connection} access={access} registration={registration} secureStore={store}
        initialDraft={draft} onOpenSettings={openSettings} registerShutdown={registerShutdown} />
      : <Welcome draft={draft} onDraftChange={setDraft} onSettings={openSettings} notice={error} />}
    <Dialog open={settingsOpen} onClose={() => setSettingsOpen(false)} title="连接与设置">
      <p className="settings-intro">连接一次，就可以在这里与自己的 Agent 对话。</p>
      {identity === null ? <section className="setup-card"><div className="section-heading"><h3>1. 准备当前浏览器</h3><span className="status-pill">{probe === null ? '检查中' : probe.supported ? '可使用' : '不支持'}</span></div>
        <p>{probe?.detail ?? '正在检查浏览器的安全存储…'}</p>
        <button className="primary-button" type="button" disabled={busy || probe?.supported !== true} onClick={() => void createIdentity()}>{busy ? '正在准备…' : '启用安全浏览器身份'}</button>
        <p className="fine-print">私钥仅在此浏览器生成，不以明文保存。</p>
      </section> : <>
        <section className="identity-card"><div className="section-heading"><h3>浏览器身份</h3><span className="status-pill success">已就绪</span></div><details className="technical-details"><summary>查看安全详情</summary><dl><dt>保护等级</dt><dd>{identity.assurance}</dd><dt>签名指纹</dt><dd>{identity.signing.thumbprint}</dd><dt>加密指纹</dt><dd>{identity.kem.thumbprint}</dd></dl></details></section>
        {access === null ? null : <PlatformSetup access={access} registration={registration} />}
        {access !== null && store !== null && registration !== null ? <GatewaySetup access={access} store={store} onReady={setConnection} /> : null}
      </>}
      {error === null ? null : <p className="error-banner" role="alert">{error}</p>}
      <div className="settings-privacy"><strong>关于对话隐私</strong><p>历史、草稿和恢复密钥在当前浏览器中加密保存。刷新后需验证同一端点的授权才能继续；清除浏览器数据或更换设备可能无法恢复。</p></div>
      {online ? <button type="button" className="primary-button settings-done" onClick={() => setSettingsOpen(false)}>返回对话</button> : null}
    </Dialog>
  </>;
}
