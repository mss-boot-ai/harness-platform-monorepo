import { useState } from 'react';
import { Dialog } from '../chat/Dialog';
import type { WorkspaceOccupant } from './conversation-manager';

export function WorkspaceOccupancy({ occupants, busy, onSelect, onRelease }: {
  readonly occupants: readonly WorkspaceOccupant[]; readonly busy: boolean;
  readonly onSelect: (id: string) => void; readonly onRelease: (occupant: WorkspaceOccupant) => void;
}) {
  const [selected, setSelected] = useState<WorkspaceOccupant | null>(null);
  if (occupants.length === 0) return null;
  return <section className="recovery-actions" aria-label="项目占用">
    <p>此项目由下列对话保留。即使 Agent 空闲，仍需确认结束旧运行才能创建新的运行；归档不会释放项目。</p>
    {occupants.map((occupant) => <div className="recovery-buttons" key={occupant.runId ?? occupant.conversationId}>
      <strong>{occupant.title}</strong><span>{occupant.status === 'DRAINING' ? '等待执行端释放' : occupant.runId === null ? '创建待确认' : '运行尚未结束'}</span>
      {occupant.conversationId === null ? null : <button className="secondary-button" type="button" onClick={() => onSelect(occupant.conversationId!)}>返回占用对话</button>}
      {occupant.runId === null ? null : <button className="secondary-button" type="button" disabled={busy} onClick={() => setSelected(occupant)}>结束并释放项目</button>}
    </div>)}
    <Dialog open={selected !== null} onClose={() => setSelected(null)} title="确认释放项目">
      <p>将结束“{selected?.title}”的运行，保留对话历史。已完成的操作不会撤销；新的运行将使用全新的 Agent 上下文。未收到执行端清理确认前，项目仍视为占用。</p>
      <button className="secondary-button" type="button" onClick={() => setSelected(null)}>返回</button>
      <button className="primary-button" type="button" disabled={busy || selected === null || !occupants.some((item) => item.runId === selected.runId)}
        onClick={() => { if (selected !== null) { onRelease(selected); setSelected(null); } }}>确认结束并释放</button>
    </Dialog>
  </section>;
}
