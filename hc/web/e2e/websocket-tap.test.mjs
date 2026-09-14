import assert from 'node:assert/strict';
import { test } from 'node:test';
import { once } from 'node:events';
import { websocketTap } from './websocket-tap.mjs';
function frame(value, masked = false) {
  const bytes = Buffer.from(value); const header = Buffer.from([0x82, bytes.length | (masked ? 128 : 0)]);
  if (!masked) return Buffer.concat([header, bytes]);
  const mask = Buffer.from([1, 2, 3, 4]);
  return Buffer.concat([header, mask, Buffer.from(bytes.map((value, index) => value ^ mask[index % 4]))]);
}
test('preserves segmented masked client frames byte-for-byte and drops only the selected message', async () => {
  const seen = []; const output = []; const tap = websocketTap((bytes) => { seen.push(bytes.toString()); return bytes.toString() !== 'drop'; });
  tap.on('data', (bytes) => output.push(bytes));
  const first = frame('keep', true); const second = frame('drop', true); const third = frame('last', true);
  const all = Buffer.concat([first, second, third]);
  tap.write(all.subarray(0, 3)); tap.write(all.subarray(3, 8)); tap.end(all.subarray(8)); await once(tap, 'end');
  assert.deepEqual(seen, ['keep', 'drop', 'last']); assert.deepEqual(Buffer.concat(output), Buffer.concat([first, third]));
});
test('forwards server messages and control frames without reconstruction', async () => {
  const output = []; const seen = []; const tap = websocketTap((bytes) => seen.push(bytes.toString()));
  tap.on('data', (bytes) => output.push(bytes)); const data = Buffer.concat([frame('reply'), Buffer.from([0x89, 0])]);
  tap.end(data); await once(tap, 'end'); assert.deepEqual(Buffer.concat(output), data); assert.deepEqual(seen, ['reply']);
});
