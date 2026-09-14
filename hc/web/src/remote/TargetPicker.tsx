import type { ABAEndpointSummary } from '../api';
import { executionTargetState, type ExecutionTarget } from './execution-target';

const statusText = { offline: '设备已离线，连接后可选择项目。', 'not-published': '设备尚未发布项目，请在执行端启用项目发现并更新连接器。',
  stale: '项目目录已过期，等待设备重新发布。', unavailable: '暂时无法读取项目目录，请刷新后重试。', ready: '此设备尚未配置可使用的项目。' };

export function TargetPicker({ endpoints, target, disabled, onChange, onRefresh }: {
  readonly endpoints: readonly ABAEndpointSummary[]; readonly target: ExecutionTarget | null; readonly disabled: boolean;
  readonly onChange: (value: ExecutionTarget) => void; readonly onRefresh: () => void;
}) {
  const { endpoint, workspace } = executionTargetState(endpoints, target);
  const device = endpoint ?? endpoints[0];
  const catalog = device?.catalog;
  const projects = catalog?.workspaces ?? [];
  const runtimes = (catalog?.runtimes ?? []).filter((item) => workspace?.runtimeIds.includes(item.id));
  const choose = (device: ABAEndpointSummary, workspaceId: string) => {
    const project = device.catalog.workspaces.find((item) => item.id === workspaceId);
    if (project?.runtimeIds[0] !== undefined) onChange({ abaEndpointId: device.id, workspaceId, runtimeProfileId: project.runtimeIds[0] });
  };
  return <section className="target-picker" aria-label="执行项目">
    <h2>项目与 Agent</h2>
    <label>执行设备<select value={device?.id ?? ''} disabled={disabled || endpoints.length === 0} onChange={(event) => {
      const device = endpoints.find((item) => item.id === event.target.value);
      if (device !== undefined && device.catalog.workspaces[0] !== undefined) choose(device, device.catalog.workspaces[0].id);
    }}>{endpoints.length === 0 ? <option value="">暂无已授权设备</option> : endpoints.map((item) => <option key={item.id} value={item.id} disabled={item.catalog.status !== 'ready' || item.catalog.workspaces.length === 0}>{item.name}{item.catalog.status === 'offline' ? ' · 离线' : ''}</option>)}</select></label>
    <label>项目<select value={target?.workspaceId ?? ''} disabled={disabled || catalog?.status !== 'ready' || projects.length === 0}
      onChange={(event) => { if (device !== undefined) choose(device, event.target.value); }}>
      {workspace === undefined ? <option value={target?.workspaceId ?? ''}>{target === null ? '选择项目' : '原项目当前不可用'}</option> : null}
      {projects.map((item) => <option key={item.id} value={item.id}>{item.displayName}</option>)}
    </select></label>
    <label>Agent<select value={target?.runtimeProfileId ?? ''} disabled={disabled || catalog?.status !== 'ready' || runtimes.length === 0}
      onChange={(event) => { if (target !== null) onChange({ ...target, runtimeProfileId: event.target.value }); }}>
      {runtimes.every((item) => item.id !== target?.runtimeProfileId) ? <option value={target?.runtimeProfileId ?? ''}>选择可用 Agent</option> : null}
      {runtimes.map((item) => <option key={item.id} value={item.id}>{item.displayName}</option>)}
    </select></label>
    {catalog === undefined ? <p role="status">连接设备后会显示它发布的项目。</p> : catalog.status !== 'ready' || projects.length === 0 ? <p role="status">{statusText[catalog.status]}</p> : null}
    {!disabled ? <button className="secondary-button" type="button" onClick={onRefresh}>刷新项目</button> : <p>当前运行已绑定此项目；新建对话可选择其他项目。</p>}
  </section>;
}
