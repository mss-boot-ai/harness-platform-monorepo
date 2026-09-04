import { createHarnessAPI, createHarnessIdempotencyKey } from './api';

const endpoint = {
  createdAt: '2026-09-04T10:00:00Z',
  id: '11111111111111111111111111111111',
  kemJkt: 'kem-thumbprint',
  name: 'Browser HC',
  platformName: 'web',
  signingJkt: 'signing-thumbprint',
  softwareVersion: '0.1.0',
  status: 'ACTIVE',
  type: 'HC_WEB',
};

describe('Harness API', () => {
  it('creates a fresh Idempotency-Key for every management write', async () => {
    const calls: Array<{ options: { headers?: Record<string, string> }; path: string }> = [];
    let sequence = 0;
    const api = createHarnessAPI(
      async (path, options) => {
        calls.push({ options, path });
        return endpoint;
      },
      () => `harness-test-key-${String(++sequence).padStart(4, '0')}`,
    );

    await api.changeEndpoint(endpoint.id, 'suspend');
    await api.changeEndpoint(endpoint.id, 'resume');

    expect(calls.map((call) => call.options.headers?.['Idempotency-Key'])).toEqual([
      'harness-test-key-0001',
      'harness-test-key-0002',
    ]);
    expect(calls.map((call) => call.path)).toEqual([
      `/harness/v1/endpoints/${endpoint.id}/suspend`,
      `/harness/v1/endpoints/${endpoint.id}/resume`,
    ]);
  });

  it('encodes resource IDs before placing them in request paths', async () => {
    const paths: string[] = [];
    const api = createHarnessAPI(async (path) => {
      paths.push(path);
      return endpoint;
    });

    await api.changeEndpoint('endpoint/with path', 'revoke');

    expect(paths).toEqual(['/harness/v1/endpoints/endpoint%2Fwith%20path/revoke']);
  });

  it('prefixes browser UUIDs with the Harness operation domain', () => {
    expect(createHarnessIdempotencyKey(() => '00000000-0000-4000-8000-000000000000')).toBe(
      'harness-00000000-0000-4000-8000-000000000000',
    );
  });
});
