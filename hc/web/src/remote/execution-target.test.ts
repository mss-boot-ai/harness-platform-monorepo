import { describe, expect, it } from 'vitest';
import { executionTargetState, firstExecutionTarget, parseExecutionCatalog } from './execution-target';
import { controllerFixture } from './conversation-fixture';

const catalog = () => ({ status: 'ready', revision: 'a'.repeat(64), runtimes: [{ id: 'writer', displayName: 'Writer' }, { id: 'reader', displayName: 'Reader' }],
  workspaces: [{ id: 'app', displayName: 'Application', runtimeIds: ['writer'] }, { id: 'docs', displayName: 'Documentation', runtimeIds: ['reader'] }] });
describe('actual execution targets', () => {
  it('selects an announced project and never crosses the local runtime association', async () => {
    const f = await controllerFixture(); const endpoint = { ...f.value.aba, catalog: parseExecutionCatalog(catalog()) };
    const target = firstExecutionTarget([endpoint]);
    expect(target).toEqual({ abaEndpointId: endpoint.id, workspaceId: 'app', runtimeProfileId: 'writer' });
    expect(executionTargetState([endpoint], target).available).toBe(true);
    expect(executionTargetState([endpoint], { ...target!, runtimeProfileId: 'reader' }).available).toBe(false);
    expect(executionTargetState([{ ...endpoint, catalog: { ...endpoint.catalog, status: 'offline' as const } }], target).available).toBe(false);
    expect(executionTargetState([endpoint], { ...target!, workspaceId: 'removed' }).workspace).toBeUndefined();
  });
  it('rejects invented relationships, duplicates, oversized catalogs and absent current-version catalog data', () => {
    expect(() => parseExecutionCatalog(undefined)).toThrow();
    expect(() => parseExecutionCatalog({ ...catalog(), workspaces: [{ id: 'p', displayName: 'P', runtimeIds: ['unknown'] }] })).toThrow();
    expect(() => parseExecutionCatalog({ ...catalog(), runtimes: [...catalog().runtimes, catalog().runtimes[0]] })).toThrow();
    expect(() => parseExecutionCatalog({ ...catalog(), workspaces: Array.from({ length: 65 }, (_, n) => ({ id: `p${n}`, displayName: 'P', runtimeIds: ['writer'] })) })).toThrow();
    expect(() => parseExecutionCatalog({ ...catalog(), revision: '' })).toThrow();
  });
});
