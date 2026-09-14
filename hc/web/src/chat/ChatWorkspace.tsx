import { useEffect, useRef, useState, type ReactNode } from 'react';
import { Dialog } from './Dialog';
import { Icon, type IconName } from './Icon';
import { CopyButton, Markdown } from './Markdown';
import { MAX_REPLY_CHARACTERS, shouldSendOnEnter, type ChatMessage, type ConversationItem } from './model';
export interface ChatWorkspaceProps {
  readonly draft: string; readonly onDraftChange: (value: string) => void;
  readonly onSubmit: () => void; readonly onNewChat: () => void; readonly onEndChat: (() => void) | null;
  readonly onOpenSettings: () => void; readonly onSelectConversation: (id: string) => void;
  readonly conversations: readonly ConversationItem[]; readonly selectedConversationId: string | null;
  readonly messages: readonly ChatMessage[]; readonly title: string; readonly agent: string;
  readonly online: boolean; readonly connected: boolean; readonly busy: boolean; readonly responding: boolean;
  readonly canSubmit: boolean; readonly readOnly: boolean; readonly hasActiveSession: boolean;
  readonly notice: string | null; readonly error: string | null; readonly targetSettings: ReactNode;
  readonly onCancelTurn?: () => void; readonly cancelPending?: boolean;
  readonly newChatDisabled?: boolean; readonly composerDisabled?: boolean; readonly draftSaved?: boolean;
  readonly renderTurnActivity?: (turnId: string) => ReactNode;
}
const suggestions: readonly { readonly icon: IconName; readonly title: string; readonly detail: string; readonly prompt: string }[] = [
  { icon: 'code', title: '审查代码', detail: '找到问题，给出改进建议', prompt: '请帮我审查当前工作区的代码，先说明你会重点检查哪些问题。' },
  { icon: 'terminal', title: '排查问题', detail: '从现象一步步定位原因', prompt: '我想排查一个问题，请先帮我整理需要提供的日志和环境信息。' },
  { icon: 'spark', title: '设计方案', detail: '把想法变成可执行的计划', prompt: '我有一个新功能想法，请先和我确认需求，再制定实现方案。' },
];
export function ChatWorkspace(props: ChatWorkspaceProps) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [collapsed, setCollapsed] = useState(false);
  const [searching, setSearching] = useState(false);
  const [query, setQuery] = useState('');
  const [confirm, setConfirm] = useState(false);
  const [atBottom, setAtBottom] = useState(true);
  const viewport = useRef<HTMLDivElement>(null);
  const textarea = useRef<HTMLTextAreaElement>(null);
  const nearBottom = useRef(true);
  const composing = useRef(false);
  const compositionEnded = useRef(-Infinity);
  const empty = props.messages.length === 0 && !props.readOnly;
  const filtered = props.conversations.filter((item) => item.title.toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  useEffect(() => {
    const element = textarea.current;
    if (element === null) return;
    element.style.height = '0px';
    element.style.height = `${Math.min(Math.max(element.scrollHeight, 52), 200)}px`;
  }, [props.draft]);
  useEffect(() => {
    if (nearBottom.current && viewport.current !== null) viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [props.messages, props.responding]);
  useEffect(() => {
    nearBottom.current = true; setAtBottom(true);
    if (viewport.current !== null) viewport.current.scrollTop = viewport.current.scrollHeight;
  }, [props.selectedConversationId]);
  useEffect(() => {
    const onKey = (event: KeyboardEvent) => { if (event.key === 'Escape') setSidebarOpen(false); };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  const newChat = () => {
    setSidebarOpen(false);
    props.onNewChat();
  };
  const submit = () => {
    if (props.draft.trim() === '' || !props.canSubmit || props.busy || props.responding) return;
    nearBottom.current = true; setAtBottom(true); props.onSubmit();
  };
  const composer = <div className="composer-wrap">
    {props.readOnly ? <div className="readonly-note">此对话当前只读。请核对会话或端点状态；历史消息不会自动重新执行。</div> : <>
      <form className="composer" onSubmit={(event) => { event.preventDefault(); submit(); }}>
        <label className="sr-only" htmlFor="chat-prompt">消息</label>
        <textarea id="chat-prompt" ref={textarea} rows={1} value={props.draft} maxLength={16_000} disabled={props.composerDisabled}
          placeholder={props.connected ? '给你的 Agent 发送消息…' : '有什么想交给 Agent 的？'}
          onChange={(event) => props.onDraftChange(event.target.value)}
          onCompositionStart={() => { composing.current = true; }}
          onCompositionEnd={() => { composing.current = false; compositionEnded.current = performance.now(); }}
          onKeyDown={(event) => {
            if (shouldSendOnEnter({ key: event.key, shiftKey: event.shiftKey, altKey: event.altKey,
              isComposing: composing.current || event.nativeEvent.isComposing || performance.now() - compositionEnded.current < 50,
              keyCode: event.nativeEvent.keyCode, repeat: event.repeat })) { event.preventDefault(); submit(); }
          }} />
        <div className="composer-actions"><span className="composer-context"><Icon name="lock" />{props.connected ? props.online ? '加密对话' : '连接已断开' : '连接自己的 Agent'}</span>
          {props.responding && props.onCancelTurn !== undefined ? <button className="send-button stop-button" type="button" aria-label={props.cancelPending ? '等待停止确认' : '停止本轮'} title="停止当前轮次，保留会话；已执行的动作不会撤销" disabled={props.busy || props.cancelPending} onClick={props.onCancelTurn}><Icon name="stop" /></button>
            : props.responding && props.onEndChat !== null ? <button className="send-button stop-button" type="button" aria-label="结束当前会话" title="结束会话；已执行的操作不会撤销" disabled={props.busy} onClick={() => setConfirm(true)}><Icon name="stop" /></button>
            : <button className="send-button" type="submit" aria-label={props.connected ? '发送消息' : '连接 Agent'} title={props.connected ? '发送消息' : '先连接 Agent，草稿会保留'} disabled={!props.canSubmit || props.busy || props.draft.trim() === ''}><Icon name="arrow" /></button>}
        </div>
      </form>
      <p className="composer-hint">{props.responding ? props.onCancelTurn !== undefined ? props.cancelPending ? '已请求停止，等待执行端确认；会话仍然保留。' : 'Agent 正在回复；方形按钮停止本轮。' : 'Agent 正在回复；方形按钮将结束整个会话。' : props.busy ? '正在建立会话或发送消息…' : 'Enter 发送 · Shift + Enter 换行'}<span role="status">{props.draftSaved === true ? '草稿已加密保存' : props.draftSaved === false ? '草稿保存中' : '请核对重要内容'}</span></p>
    </>}
  </div>;
  return <div className={`chat-shell ${collapsed ? 'sidebar-collapsed' : ''} ${sidebarOpen ? 'sidebar-open' : ''}`}>
    <a className="skip-link" href="#chat-prompt">跳到输入框</a>
    {sidebarOpen ? <button type="button" className="sidebar-backdrop" aria-label="关闭会话导航" onClick={() => setSidebarOpen(false)} /> : null}
    <aside className="chat-sidebar" aria-label="会话导航">
      <div className="sidebar-brand"><span className="brand-mark">H</span><strong>Harness</strong><button type="button" className="icon-button desktop-only" aria-label="收起侧边栏" onClick={() => setCollapsed(true)}><Icon name="menu" /></button><button type="button" className="icon-button mobile-only" aria-label="关闭侧边栏" onClick={() => setSidebarOpen(false)}><Icon name="close" /></button></div>
      <button type="button" className="new-chat-button" disabled={props.newChatDisabled} onClick={newChat}><Icon name="plus" /><span>新建对话</span></button>
      <button type="button" className="sidebar-action" onClick={() => setSearching((value) => !value)} aria-expanded={searching}><Icon name="search" />搜索本地会话</button>
      {searching ? <input className="conversation-search" autoFocus type="search" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="搜索对话…" aria-label="搜索本地会话" /> : null}
      <div className="sidebar-section-label">本地会话</div>
      <nav className="conversation-list" aria-label="对话列表">
        {filtered.map((item) => <button type="button" key={item.id} className={`conversation-item ${item.id === props.selectedConversationId ? 'selected' : ''}`} aria-current={item.id === props.selectedConversationId ? 'page' : undefined}
          onClick={() => { props.onSelectConversation(item.id); setSidebarOpen(false); }}><span>{item.title}</span><small>{item.detail}</small></button>)}
        {filtered.length === 0 ? <p className="sidebar-empty">{query !== '' ? '没有匹配的对话' : '开始一段对话，它会出现在这里。'}</p> : null}
      </nav>
      <div className="sidebar-footer"><p>{props.connected ? '历史与草稿在此浏览器中加密保存。' : '连接后可加密保存会话历史与草稿。'}<br />恢复需要原端点密钥和有效授权。</p><button type="button" className="account-button" onClick={() => { props.onOpenSettings(); setSidebarOpen(false); }}><span className="account-avatar"><Icon name="settings" /></span><span><strong>连接与设置</strong><small>{props.online ? '安全连接已就绪' : props.connected ? '连接已断开' : '连接你的工作环境'}</small></span><Icon name="chevron" /></button></div>
    </aside>
    <main className="chat-main">
      <header className="chat-header"><div className="header-left"><button type="button" className="icon-button sidebar-toggle" aria-label="展开会话导航" aria-expanded={sidebarOpen} onClick={() => { setCollapsed(false); setSidebarOpen(true); }}><Icon name="menu" /></button>
        {props.targetSettings !== null ? <details className="agent-picker"><summary><strong>{props.agent || '选择 Agent'}</strong><Icon name="chevron" /></summary><div className="agent-popover">{props.targetSettings}</div></details>
          : <button className="header-agent" type="button" onClick={props.onOpenSettings}>Harness<Icon name="chevron" /></button>}
      </div><div className="header-left">{props.onEndChat !== null && props.hasActiveSession ? <button type="button" className="secondary-button" disabled={props.busy || !props.online} onClick={() => setConfirm(true)}>结束会话</button> : null}<button className={`connection-badge ${props.online ? 'connected' : ''}`} type="button" onClick={props.onOpenSettings}><span className="connection-dot" />{props.online ? '已连接' : '连接 Agent'}</button></div></header>
      {props.notice !== null ? <div className="chat-notice" role="status">{props.notice}</div> : null}
      {props.error !== null ? <div className="chat-error" role="alert">{props.error}</div> : null}
      <div className={`chat-scroll ${empty ? 'is-empty' : ''}`} ref={viewport} onScroll={() => {
        const element = viewport.current;
        if (element !== null) { nearBottom.current = element.scrollHeight - element.scrollTop - element.clientHeight < 100; setAtBottom(nearBottom.current); }
      }}>
        {empty ? <section className="welcome"><div className="welcome-emblem"><Icon name="spark" /></div><p className="welcome-kicker">你的想法，你的 Agent</p><h1>今天想完成什么？</h1><p className="welcome-description">从一个问题开始，把接下来的工作交给 Harness。</p>{composer}<div className="suggestion-grid">{suggestions.map((item) => <button type="button" className="suggestion" key={item.title} onClick={() => { props.onDraftChange(item.prompt); textarea.current?.focus(); }}><Icon name={item.icon} /><strong>{item.title}</strong><span>{item.detail}</span></button>)}</div></section>
          : <section className="transcript" role="log" aria-label={props.title || '对话消息'} aria-live="off">
            {props.messages.map((message) => <article key={message.id} className={`message message-${message.role}`} aria-label={message.role === 'user' ? '你' : message.role === 'assistant' ? 'Agent' : '系统通知'}>
              {message.role === 'assistant' ? <div className="assistant-avatar" aria-hidden="true">H</div> : null}
              <div className="message-body">{message.role === 'assistant' ? <>
                <div className="message-author">{props.agent || 'Agent'}</div>
                {props.renderTurnActivity?.(message.id.replace(/^assistant-/u, ''))}
                {message.text !== '' ? <Markdown text={message.text} /> : message.state === 'streaming' ? <div className="thinking"><span /><span /><span /><span className="sr-only">正在等待 Agent 回复</span></div> : <p className="message-muted">{message.state === 'uncertain' ? '回复中断，请检查执行状态，不要直接重发。' : '本次回复未返回文本。'}</p>}
                {message.text.length >= MAX_REPLY_CHARACTERS ? <p className="message-muted">回复超过本页显示上限，请在执行端查看完整结果。</p> : null}
                {message.state === 'cancelled' ? <p className="message-muted">本轮已停止，可以继续对话；已发生的操作不受影响。</p> : null}
                {message.state === 'uncertain' ? <p className="uncertain-label">执行结果待确认</p> : null}
                {message.text !== '' && message.state !== 'streaming' ? <CopyButton text={message.text} label="复制回复" /> : null}
              </> : <p className="plain-message">{message.text}</p>}</div>
            </article>)}
          </section>}
      </div>
      {!empty ? <div className="composer-dock">{!atBottom ? <button className="latest-button" type="button" onClick={() => { nearBottom.current = true; setAtBottom(true); viewport.current?.scrollTo({ top: viewport.current.scrollHeight, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' }); }}><Icon name="down" />回到最新</button> : null}{composer}</div> : null}
      <span className="sr-only" role="status">{props.responding ? 'Agent 正在回复' : '可以继续操作'}</span>
    </main>
    <Dialog open={confirm} onClose={() => setConfirm(false)} title="结束当前会话？"><p>将向执行端发送关闭当前会话的请求。其他会话继续保留，已经执行的操作不会撤销。</p><div className="dialog-actions"><button className="secondary-button" type="button" onClick={() => setConfirm(false)}>继续当前会话</button><button className="primary-button" type="button" onClick={() => { setConfirm(false); props.onEndChat?.(); }}>确认结束</button></div></Dialog>
  </div>;
}
