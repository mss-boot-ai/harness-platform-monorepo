import { getRequestStatus } from '@mss-boot-io/admin-web/runtime';

export type HarnessPageState = 'empty' | 'error' | 'forbidden' | 'loading' | 'ready';

export function classifyHarnessPageState(input: {
  empty?: boolean;
  error?: unknown;
  loading?: boolean;
}): HarnessPageState {
  if (input.loading) return 'loading';
  if (input.error) return getRequestStatus(input.error) === 403 ? 'forbidden' : 'error';
  return input.empty ? 'empty' : 'ready';
}

export function isEmptyOverview(value: {
  activeEndpoints: number;
  activeSessions: number;
  conflictFrames: number;
  pendingEnrollments: number;
  unacknowledgedFrames: number;
}): boolean {
  return Object.values(value).every((count) => count === 0);
}
