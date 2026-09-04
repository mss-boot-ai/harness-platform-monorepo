import { request } from '@umijs/max';
import {
  type HarnessDelivery,
  type HarnessEndpoint,
  type HarnessEnrollment,
  type HarnessOverview,
  type HarnessSession,
  parseHarnessDelivery,
  parseHarnessEndpoint,
  parseHarnessEndpointList,
  parseHarnessEnrollment,
  parseHarnessEnrollmentList,
  parseHarnessOverview,
  parseHarnessSession,
  parseHarnessSessionList,
} from './contract';

export interface HarnessRequestOptions {
  data?: unknown;
  headers?: Record<string, string>;
  method: 'GET' | 'POST';
  params?: Record<string, number | string | undefined>;
  skipErrorHandler: true;
}

export type HarnessRequestClient = (
  path: string,
  options: HarnessRequestOptions,
) => Promise<unknown>;

export type HarnessIdempotencyKeyFactory = () => string;

export function createHarnessIdempotencyKey(
  randomUUID: () => string = () => crypto.randomUUID(),
): string {
  return `harness-${randomUUID()}`;
}

function resourcePath(resource: string, id: string, action?: string): string {
  const base = `/harness/v1/${resource}/${encodeURIComponent(id)}`;
  return action ? `${base}/${action}` : base;
}

export function createHarnessAPI(
  client: HarnessRequestClient,
  createKey: HarnessIdempotencyKeyFactory = createHarnessIdempotencyKey,
) {
  const mutation = (path: string, data: unknown) =>
    client(path, {
      data,
      headers: { 'Idempotency-Key': createKey() },
      method: 'POST',
      skipErrorHandler: true,
    });

  return {
    loadOverview: async (): Promise<HarnessOverview> =>
      parseHarnessOverview(
        await client('/harness/v1/overview', {
          method: 'GET',
          skipErrorHandler: true,
        }),
      ),
    listEnrollments: async (): Promise<HarnessEnrollment[]> =>
      parseHarnessEnrollmentList(
        await client('/harness/v1/enrollments', {
          method: 'GET',
          params: { limit: 200 },
          skipErrorHandler: true,
        }),
      ),
    decideEnrollment: async (
      id: string,
      decision: 'approve' | 'deny',
      userCode: string,
    ): Promise<HarnessEnrollment> =>
      parseHarnessEnrollment(
        await mutation(resourcePath('enrollments', id, decision), { userCode }),
      ),
    listEndpoints: async (): Promise<HarnessEndpoint[]> =>
      parseHarnessEndpointList(
        await client('/harness/v1/endpoints', {
          method: 'GET',
          params: { limit: 200 },
          skipErrorHandler: true,
        }),
      ),
    changeEndpoint: async (
      id: string,
      action: 'resume' | 'revoke' | 'suspend',
    ): Promise<HarnessEndpoint> =>
      parseHarnessEndpoint(await mutation(resourcePath('endpoints', id, action), {})),
    listSessions: async (): Promise<HarnessSession[]> =>
      parseHarnessSessionList(
        await client('/harness/v1/sessions', {
          method: 'GET',
          params: { limit: 200 },
          skipErrorHandler: true,
        }),
      ),
    closeSession: async (id: string): Promise<HarnessSession> =>
      parseHarnessSession(await mutation(resourcePath('sessions', id, 'close'), {})),
    loadDelivery: async (sessionId: string): Promise<HarnessDelivery> =>
      parseHarnessDelivery(
        await client(resourcePath('sessions', sessionId, 'delivery'), {
          method: 'GET',
          params: { limit: 200 },
          skipErrorHandler: true,
        }),
      ),
  };
}

export const harnessAPI = createHarnessAPI((path, options) => request<unknown>(path, options));
