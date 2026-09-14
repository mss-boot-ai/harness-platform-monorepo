import { ownEndpoint } from './endpoint-owner';
function fakeLocks() {
  let held = false;
  const queue: (() => void)[] = [];
  const calls: LockOptions[] = [];
  const request: LockManager['request'] = ((name: string, options: LockOptions, callback: LockGrantedCallback<void>) => {
    calls.push(options);
    return new Promise<void>((resolve, reject) => {
      const run = () => {
        if (options.signal?.aborted) { reject(new Error('aborted')); next(); return; }
        held = true;
        Promise.resolve(callback({ name, mode: 'exclusive' })).then(resolve, reject).finally(() => { held = false; next(); });
      };
      const next = () => { if (!held) queue.shift()?.(); };
      queue.push(run); next();
    });
  }) as LockManager['request'];
  return { request, calls };
}
describe('same-installation tab ownership', () => {
  it('does not rotate or connect a second tab until the first releases the endpoint', async () => {
    const locks = fakeLocks(); const one: string[] = []; const two: string[] = [];
    const first = ownEndpoint(locks, (state) => one.push(state));
    const second = ownEndpoint(locks, (state) => two.push(state));
    expect(one).toEqual(['waiting', 'owned']); expect(two).toEqual(['waiting']);
    expect(locks.calls.every((item) => item.steal !== true && item.mode === 'exclusive')).toBe(true);
    first.release(); await first.done; await new Promise((resolve) => setTimeout(resolve, 0));
    expect(two).toEqual(['waiting', 'owned']); second.release(); await second.done;
  });
  it('cancels a waiting tab and fails closed when locks are unavailable', async () => {
    const states: string[] = []; const unsupported = ownEndpoint(undefined, (value) => states.push(value));
    await unsupported.done; expect(states).toEqual(['unsupported']);
    const locks = fakeLocks(); const first = ownEndpoint(locks, () => undefined); const waiting: string[] = [];
    const second = ownEndpoint(locks, (state) => waiting.push(state)); second.release(); first.release();
    await Promise.all([first.done, second.done]); expect(waiting).not.toContain('owned');
  });
});

it('handles a synchronous Web Locks rejection without attempting ownership', async () => {
  const states: string[] = [];
  const locks = { request: (() => { throw new Error('SecurityError'); }) as LockManager['request'] };
  await ownEndpoint(locks, (state) => states.push(state)).done;
  expect(states).toEqual(['waiting', 'unsupported']);
});
