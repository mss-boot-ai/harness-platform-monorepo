/** Own the endpoint transport before refresh/token rotation; never steal a live tab's lock. */
export type EndpointOwnerState = 'waiting' | 'owned' | 'unsupported' | 'released';
export interface OwnershipHandle { readonly done: Promise<void>; release(): void }
export type RegisterEndpointShutdown = (shutdown: () => Promise<void>) => () => void;
export function ownEndpoint(locks: Pick<LockManager, 'request'> | undefined,
  changed: (state: EndpointOwnerState) => void): OwnershipHandle {
  if (locks === undefined) {
    changed('unsupported'); return { done: Promise.resolve(), release: () => undefined };
  }
  const abort = new AbortController();
  let release: () => void = () => undefined;
  let ended = false;
  changed('waiting');
  let request: Promise<unknown>;
  try { request = locks.request('mss-hc-primary-endpoint-transport-v1', { mode: 'exclusive', signal: abort.signal }, async () => {
    if (ended) return;
    await new Promise<void>((resolve) => {
      release = resolve;
      changed('owned');
    });
  }); } catch {
    changed('unsupported'); return { done: Promise.resolve(), release: () => undefined };
  }
  const done = request.then(() => { if (!ended) changed('released'); }, () => { if (!ended) changed('unsupported'); });
  return { done, release: () => { if (!ended) { ended = true; abort.abort(); release(); changed('released'); } } };
}
