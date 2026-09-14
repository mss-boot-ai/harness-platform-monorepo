import { useState } from 'react';
import type { ConversationEntryView } from './conversation-manager';
import { Dialog } from '../chat/Dialog';

export function RecoveryActions({ conversation, blocked, terminal, online, busy, onCheckCreation, onInspect, onClose, onContinue, onReconnect, onNew }: {
  readonly conversation: ConversationEntryView | null; readonly blocked: boolean; readonly terminal: boolean;
  readonly online: boolean; readonly busy: boolean; readonly onCheckCreation: (cancel: boolean) => void;
  readonly onInspect: () => void; readonly onClose: () => void; readonly onContinue: () => void; readonly onReconnect: () => void; readonly onNew: () => void;
}) {
  const [confirmClose, setConfirmClose] = useState(false);
  if (conversation === null || (conversation.creation === null && !blocked && !terminal && conversation.notice === null && online)) return null;
  return <section className="recovery-actions" aria-label="对话恢复">
    {conversation.notice !== null ? <p>{conversation.notice}</p> : null}
    <div className="recovery-buttons">
      {conversation.creation !== null ? <>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => onCheckCreation(false)}>检查原创建结果</button>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => onCheckCreation(true)}>{conversation.creation.cancelRequested ? '确认取消结果' : '取消这次创建'}</button>
      </> : conversation.activeRunId !== null ? <>
        <button className="secondary-button" type="button" disabled={busy} onClick={onInspect}>检查执行状态</button>
        {terminal ? <button className="primary-button" type="button" disabled={busy} onClick={onContinue}>继续此对话</button>
          : <button className="secondary-button" type="button" disabled={busy} onClick={() => setConfirmClose(true)}>结束失效运行</button>}
      </> : null}
      {!online ? <button className="secondary-button" type="button" onClick={onReconnect}>重新连接</button> : null}
      <button className="secondary-button" type="button" onClick={onNew}>新建另一对话</button>
    </div>
    <Dialog open={confirmClose} onClose={() => setConfirmClose(false)} title="结束这次运行？">
      <p>将停止这次运行并保留对话历史。已经完成的项目操作不会撤销；结果不确定时，请同时核对项目状态。</p>
      <button className="secondary-button" type="button" onClick={() => setConfirmClose(false)}>返回</button>
      <button className="primary-button" type="button" disabled={busy} onClick={() => { setConfirmClose(false); onClose(); }}>确认结束运行</button>
    </Dialog>
  </section>;
}
