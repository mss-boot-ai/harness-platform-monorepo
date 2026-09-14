import { createEndpointIdentity } from '@harness/hc-core';
import * as api from '../api';
import { EndpointAccess } from './endpoint-access';
vi.mock('../api', async (original) => ({ ...await original<typeof import('../api')>(),
  refreshEndpointSession: vi.fn(), loginBrowserSession: vi.fn(), registerBrowserEndpoint: vi.fn(),
  issueWebSocketTicket: vi.fn(), fetchTrustManifest: vi.fn() }));
async function fixture() {
  const identity = await createEndpointIdentity('owner', 'owner', 'web-software');
  const registration: api.RegistrationSession = { endpointId: '03'.repeat(16), credentialId: '04'.repeat(16),
    accessToken: 'synthetic-unit-test-only', accessExpiresAt: new Date(Date.now() + 3_600_000).toISOString(),
    signingJkt: identity.signing.thumbprint, kemJkt: identity.kem.thumbprint, tokenType: 'DPoP' };
  let owned = true; const changed = vi.fn(); const access = new EndpointAccess(identity, () => owned, changed);
  vi.mocked(api.refreshEndpointSession).mockResolvedValue(registration);
  return { access, identity, registration, changed, setOwned: (value: boolean) => { owned = value; } };
}
describe('owned endpoint credential lifecycle', () => {
  it('performs no refresh, login, registration, ticket or session operation without ownership', async () => {
    const f = await fixture(); f.setOwned(false); const operation = vi.fn();
    await expect(f.access.refresh()).rejects.toThrow('ownership');
    await expect(f.access.login('fixture', 'fixture')).rejects.toThrow('ownership');
    await expect(f.access.authorized(operation)).rejects.toThrow('ownership');
    for (const fn of [api.refreshEndpointSession, api.loginBrowserSession, api.registerBrowserEndpoint, api.issueWebSocketTicket, operation]) expect(fn).not.toHaveBeenCalled();
  });
  it('coalesces concurrent credential recovery and reuses the current registration', async () => {
    const f = await fixture(); const values = await Promise.all([f.access.current(), f.access.refresh(), f.access.current()]);
    expect(values).toEqual([f.registration, f.registration, f.registration]);
    expect(api.refreshEndpointSession).toHaveBeenCalledTimes(1);
    expect(await f.access.current()).toBe(f.registration); expect(f.changed).toHaveBeenCalledTimes(1);
  });
  it('fences a refresh that completes after ownership ends and drains it before disposal returns', async () => {
    const f = await fixture(); let resolve: (value: api.RegistrationSession) => void = () => undefined;
    vi.mocked(api.refreshEndpointSession).mockImplementation(() => new Promise((done) => { resolve = done; }));
    const refresh = f.access.refresh(); const failed = expect(refresh).rejects.toThrow('ownership');
    let disposed = false; const ending = f.access.dispose().then(() => { disposed = true; });
    await Promise.resolve(); expect(disposed).toBe(false); resolve(f.registration); await failed; await ending;
    expect(disposed).toBe(true); expect(f.changed).not.toHaveBeenCalled();
  });
  it('rejects different endpoint fingerprints before publishing refreshed credentials', async () => {
    const f = await fixture(); vi.mocked(api.refreshEndpointSession).mockResolvedValue({ ...f.registration, kemJkt: 'different' });
    await expect(f.access.refresh()).rejects.toThrow('identity'); expect(f.changed).not.toHaveBeenCalled();
  });
});
