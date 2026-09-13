// Synthetic component fixture, available only in the Vite source server; not a production build entry.
// It never connects to an Agent, registers an endpoint, stores credentials or exercises crypto.
import { useState } from 'react';
import { createRoot } from 'react-dom/client';
import { ChatWorkspace } from '../src/chat/ChatWorkspace';
import { Dialog } from '../src/chat/Dialog';
import { receiveAcp, startTurn, type ChatMessage } from '../src/chat/model';
import '../src/styles.css';
const response = '可以。先把最小闭环跑通，再增加复杂能力。\n\n### 一个简单的 Go 健康检查\n\n```go\nfunc health(w http.ResponseWriter, r *http.Request) {\n    w.WriteHeader(http.StatusOK)\n    _, _ = w.Write([]byte("ok"))\n}\n```\n\n- **先验证输入和输出**，不要一次改动所有模块。\n- 为关键路径添加测试，再接入真实环境。\n\n这段代码只作为界面测试示例，不代表对你的项目进行了实际修改。';
function initialMessages(): readonly ChatMessage[] {
  const messages = startTurn([], 'fixture', '帮我写一个 Go 健康检查，并说明实现思路。');
  const result = receiveAcp(messages, { method: 'session/update', params: { update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: response } } } }, 'fixture');
  return receiveAcp(result.messages, { id: 'fixture', result: {} }, 'fixture').messages;
}
function Fixture() {
  const scenario = new URLSearchParams(window.location.search).get('scenario');
  const [draft, setDraft] = useState('');
  const [messages, setMessages] = useState<readonly ChatMessage[]>(() => scenario === 'welcome' ? [] : scenario === 'long' ? Array.from({ length: 12 }, (_, index) => initialMessages().map((message) => ({ ...message, id: `${index}-${message.id}` }))).flat() : initialMessages());
  const [responding, setResponding] = useState(false);
  const [settings, setSettings] = useState(false);
  const [ended, setEnded] = useState(false);
  const send = () => {
    const id = crypto.randomUUID();
    setMessages((current) => startTurn(current, id, draft)); setDraft(''); setResponding(true);
    setTimeout(() => {
      setMessages((current) => receiveAcp(current, [
        { method: 'session/update', params: { update: { sessionUpdate: 'agent_message_chunk', content: { type: 'text', text: '已收到。这是组件测试回复。' } } } },
        { id, result: {} },
      ], id).messages);
      setResponding(false);
    }, 700);
  };
  return <><ChatWorkspace draft={draft} onDraftChange={setDraft} onSubmit={send}
    onNewChat={() => { setMessages([]); setEnded(false); setDraft(''); }} onEndChat={() => { setResponding(false); setEnded(true); }}
    onOpenSettings={() => setSettings(true)} onSelectConversation={() => undefined}
    conversations={messages.length === 0 ? [] : [{ id: 'current', title: 'Go 健康检查与实现思路', detail: '本页会话' }]}
    selectedConversationId={messages.length === 0 ? null : 'current'} messages={messages} title="Go 健康检查与实现思路" agent="我的开发 Agent"
    online={scenario !== 'offline'} connected busy={false} responding={responding} canSubmit={!ended && scenario !== 'offline'} readOnly={ended} hasActiveSession={!ended && messages.length > 0}
    notice={scenario === 'offline' ? '连接已断开，草稿与本页记录已保留。请重新连接，不会自动重发。' : null} error={null}
    targetSettings={<div className="target-form"><h2>执行环境</h2><p>测试场景，不连接真实服务。</p><label>执行设备<select><option>我的开发机</option></select></label></div>} />
    <Dialog open={settings} onClose={() => setSettings(false)} title="连接与设置"><p>组件测试设置。关闭后保留会话和草稿。</p></Dialog></>;
}
const root = document.getElementById('root');
if (root === null) throw new Error('Fixture root missing');
createRoot(root).render(<Fixture />);
