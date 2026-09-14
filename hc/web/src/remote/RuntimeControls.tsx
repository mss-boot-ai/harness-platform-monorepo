import { useEffect, useState } from 'react';
import { Dialog } from '../chat/Dialog';
import type { ConfigOption, Permission, RpcId, RuntimeState } from './runtime-state';

export interface PendingConfig { readonly id: string; readonly option: ConfigOption; readonly value: string }
export function RuntimeConfiguration({ state, pending, disabled, onChange, onRefresh }: {
  readonly state: RuntimeState; readonly pending: PendingConfig | null; readonly disabled: boolean;
  readonly onChange: (option: ConfigOption, value: string) => void; readonly onRefresh: () => void;
}) {
  const labels: Readonly<Record<string, string>> = { model: '模型', thought_level: '推理强度', mode: '会话权限模式' };
  return <section className="runtime-configuration" aria-label="会话配置">
    <div className="section-heading"><h3>会话配置</h3><span className="status-pill">{state.status === 'ready' ? `已确认 · ${state.configRevision}` : state.status === 'pending' ? '读取中' : '未就绪'}</span></div>
    {state.status === 'failed' ? <p>执行端状态未知或已停止。以下仅为历史配置，不能视为当前生效值。</p>
      : state.status !== 'ready' ? <p>正在读取执行端实际能力。未支持的配置不会显示为可选项。</p> : state.config.length === 0 ? <p>该 Agent 没有提供可修改的会话配置。</p> : null}
    {state.config.map((option) => <label key={option.id}>{labels[option.category] ?? option.name}
      <select aria-label={labels[option.category] ?? option.name} value={option.value} disabled={disabled || pending !== null || state.status !== 'ready'}
        onChange={(event) => onChange(option, event.target.value)}>
        {option.options.map((choice) => <option key={choice.value} value={choice.value}>{choice.group === null ? choice.name : `${choice.group} / ${choice.name}`}</option>)}
      </select>
      {option.description !== '' ? <small>{option.description}</small> : null}
      {pending?.option.id === option.id ? <small role="status">正在请求修改，显示值仍是最后一次执行端确认的配置。</small> : null}
    </label>)}
    {state.configError === null ? null : <p className="error-banner" role="alert">{state.configError}</p>}
    <button type="button" className="secondary-button" disabled={disabled || pending !== null} onClick={onRefresh}>重新读取配置</button>
    <p className="fine-print">权限模式受执行主机的本地许可和沙箱限制；名称不代表额外授予了系统权限。</p>
    <div className="context-usage" aria-label="上下文用量">{state.usage === null ? '上下文用量：运行时尚未上报' : <>
      <span>上下文：{state.usage.used.toLocaleString()} / {state.usage.size.toLocaleString()} tokens</span>
      <progress value={Math.min(state.usage.used, state.usage.size)} max={state.usage.size} aria-label="运行时上报的上下文占用" />
      <small>{(state.usage.used / state.usage.size * 100).toFixed(1)}% · 来源：当前运行时</small>
    </>}</div>
  </section>;
}
function permissionLabel(value: Permission['options'][number]): string {
  const labels = { allow_once: '允许此次', allow_always: '持续允许', reject_once: '拒绝此次', reject_always: '持续拒绝' };
  return `${labels[value.kind]} · ${value.name}`;
}
export function RuntimeActivity({ state, turnId, disabled, onDecision }: {
  readonly state: RuntimeState; readonly turnId: string; readonly disabled: boolean;
  readonly onDecision: (id: RpcId, optionId: string | null) => void;
}) {
  const [confirmation, setConfirmation] = useState<{ readonly permission: Permission; readonly optionId: string } | null>(null);
  const [now, setNow] = useState(Date.now());
  const permissions = state.permissions.filter((item) => item.turnId === turnId);
  useEffect(() => {
    if (!permissions.some((item) => item.status === 'pending')) return;
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [permissions.some((item) => item.status === 'pending')]);
  const toolLabels: Readonly<Record<string, string>> = { pending: '等待中', in_progress: '执行中', completed: '已完成', failed: '失败' };
  const tools = state.tools.filter((item) => item.turnId === turnId);
  const plan = state.planTurnId === turnId ? state.plan : [];
  if (permissions.length === 0 && tools.length === 0 && plan.length === 0) return null;
  return <div className="runtime-activity">
    {plan.length === 0 ? null : <details className="plan-card"><summary>执行计划 · {plan.filter((item) => item.status === 'completed').length}/{plan.length}</summary><ol>{plan.map((item, index) => <li key={`${index}-${item.content}`}><span className="status-pill">{item.status}</span> {item.content}</li>)}</ol></details>}
    {tools.map((tool) => <details className={`tool-card tool-${tool.status}`} key={tool.id}><summary><span>{tool.title}</span><span className="status-pill">{toolLabels[tool.status] ?? tool.status}</span></summary>
      {tool.input === '' ? null : <><h4>工具输入</h4><pre>{tool.input}</pre></>}
      {tool.content === '' ? null : <><h4>工具结果 / 变更</h4><pre>{tool.content}</pre></>}
    </details>)}
    {permissions.map((permission) => {
      const expired = now >= permission.receivedAt + 300_000;
      const actionable = !disabled && permission.status === 'pending' && !expired;
      return <section className="permission-card" key={permission.id} aria-label="工具权限请求">
        <div className="section-heading"><h3>工具权限请求</h3><span className="status-pill">{permission.status === 'closed' ? '已结束' : permission.status === 'submitted' ? '已提交，等待执行端' : expired ? '已过期，请检查执行端' : '等待你的决定'}</span></div>
        <strong>{permission.title}</strong><details><summary>查看原始动作与完整范围</summary><pre>{permission.detail}</pre></details>
        {!permission.complete ? <p role="alert">动作超出安全显示上限，不能在此批准。</p> : null}
        {permission.status === 'pending' ? <div className="permission-actions">
          {permission.options.map((option) => <button type="button" key={option.id} className={option.kind.startsWith('reject') ? 'secondary-button' : 'primary-button'}
            disabled={!actionable || (option.kind.startsWith('allow') && !permission.complete)}
            onClick={() => { if (option.kind.startsWith('allow')) setConfirmation({ permission, optionId: option.id }); else onDecision(permission.id, option.id); }}>{permissionLabel(option)}</button>)}
          <button className="secondary-button" type="button" disabled={!actionable} onClick={() => onDecision(permission.id, null)}>不授予权限</button>
        </div> : null}
      </section>;
    })}
    <Dialog open={confirmation !== null} title="确认工具授权" onClose={() => setConfirmation(null)}>
      <p>授权只发回这一个仍待处理的原始请求。它不代表工具已经执行成功。</p>
      {confirmation === null ? null : <><strong>{confirmation.permission.title}</strong><pre className="approval-detail">{confirmation.permission.detail}</pre>
        <p>{confirmation.permission.options.find((item) => item.id === confirmation.optionId)?.kind === 'allow_always' ? '注意：此选项是运行时提供的持续许可，不限于本次动作。' : '仅批准本次请求。'}</p>
        <div className="dialog-actions"><button type="button" className="secondary-button" onClick={() => setConfirmation(null)}>返回检查</button><button type="button" className="primary-button"
          disabled={disabled || !state.permissions.some((item) => item.id === confirmation.permission.id && item.status === 'pending') || now >= confirmation.permission.receivedAt + 300_000}
          onClick={() => { const selected = confirmation; setConfirmation(null); onDecision(selected.permission.id, selected.optionId); }}>确认授权</button></div></>}
    </Dialog>
  </div>;
}
