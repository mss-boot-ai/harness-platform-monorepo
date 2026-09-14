import type { ABAEndpointSummary } from '../api';

export interface ExecutionTarget {
  readonly abaEndpointId: string;
  readonly workspaceId: string;
  readonly runtimeProfileId: string;
}
export interface CatalogRuntime { readonly id: string; readonly displayName: string }
export interface CatalogWorkspace { readonly id: string; readonly displayName: string; readonly runtimeIds: readonly string[] }
export interface ExecutionCatalog {
  readonly status: 'ready' | 'offline' | 'not-published' | 'stale' | 'unavailable';
  readonly revision: string;
  readonly runtimes: readonly CatalogRuntime[];
  readonly workspaces: readonly CatalogWorkspace[];
}
const statuses = new Set(['ready', 'offline', 'not-published', 'stale', 'unavailable']);
const object = (value: unknown): Record<string, unknown> | null => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null;
const validId = (value: unknown): value is string => typeof value === 'string' && /^[A-Za-z0-9_.-]{1,64}$/u.test(value);
const validName = (value: unknown): value is string => typeof value === 'string' && value.trim() !== '' && value.length <= 128 && !Array.from(value).some((character) => character.charCodeAt(0) < 32 || character.charCodeAt(0) === 127);

export function parseExecutionCatalog(raw: unknown): ExecutionCatalog {
  const catalog = object(raw);
  if (catalog === null || !statuses.has(String(catalog.status)) || typeof catalog.revision !== 'string' ||
      (catalog.revision !== '' && !/^[0-9a-f]{64}$/u.test(catalog.revision)) ||
      !Array.isArray(catalog.runtimes) || catalog.runtimes.length > 64 || !Array.isArray(catalog.workspaces) || catalog.workspaces.length > 64) throw new Error('Execution catalog is invalid');
  const runtimeIds = new Set<string>();
  const runtimes = catalog.runtimes.map((raw) => {
    const item = object(raw);
    if (item === null || !validId(item.id) || !validName(item.displayName) || runtimeIds.has(item.id)) throw new Error('Runtime catalog is invalid');
    runtimeIds.add(item.id); return { id: item.id, displayName: item.displayName };
  });
  const workspaceIds = new Set<string>();
  const workspaces = catalog.workspaces.map((raw) => {
    const item = object(raw);
    if (item === null || !validId(item.id) || !validName(item.displayName) || workspaceIds.has(item.id) ||
      !Array.isArray(item.runtimeIds) || item.runtimeIds.length === 0 || item.runtimeIds.length > 64 ||
      item.runtimeIds.some((id) => !validId(id) || !runtimeIds.has(id)) || new Set(item.runtimeIds).size !== item.runtimeIds.length) throw new Error('Project catalog is invalid');
    workspaceIds.add(item.id); return { id: item.id, displayName: item.displayName, runtimeIds: item.runtimeIds as string[] };
  });
  if (catalog.status === 'ready' && catalog.revision === '') throw new Error('Published catalog revision is missing');
  return { status: catalog.status as ExecutionCatalog['status'], revision: catalog.revision, runtimes, workspaces };
}

export function firstExecutionTarget(endpoints: readonly ABAEndpointSummary[]): ExecutionTarget | null {
  for (const endpoint of endpoints) {
    if (endpoint.catalog.status !== 'ready') continue;
    const workspace = endpoint.catalog.workspaces[0];
    if (workspace !== undefined && workspace.runtimeIds[0] !== undefined) return {
      abaEndpointId: endpoint.id, workspaceId: workspace.id, runtimeProfileId: workspace.runtimeIds[0],
    };
  }
  return null;
}

export function executionTargetState(endpoints: readonly ABAEndpointSummary[], target: ExecutionTarget | null) {
  const endpoint = endpoints.find((item) => item.id === target?.abaEndpointId);
  const workspace = endpoint?.catalog.workspaces.find((item) => item.id === target?.workspaceId);
  const runtime = endpoint?.catalog.runtimes.find((item) => item.id === target?.runtimeProfileId && workspace?.runtimeIds.includes(item.id));
  return { endpoint, workspace, runtime, available: endpoint?.catalog.status === 'ready' && workspace !== undefined && runtime !== undefined };
}

export function isExecutionTarget(raw: unknown): raw is ExecutionTarget {
  const value = object(raw);
  return value !== null && typeof value.abaEndpointId === 'string' && /^[0-9a-f]{32}$/u.test(value.abaEndpointId) &&
    validId(value.workspaceId) && validId(value.runtimeProfileId);
}
