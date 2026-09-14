// Test-only RFC 6455 frame tap. It forwards original bytes, with optional bounded binary-message loss.
// It does not change AWP data, signatures, masking, handshakes or negotiated subprotocols.
import { Transform } from 'node:stream';
export function websocketTap(observe) {
  let buffered = Buffer.alloc(0);
  return new Transform({ transform(chunk, _encoding, done) {
    try {
      if (buffered.length + chunk.length > 2_097_180) throw new Error('Fixture WebSocket buffer exceeded');
      buffered = Buffer.concat([buffered, chunk]);
      while (buffered.length >= 2) {
        const masked = (buffered[1] & 128) !== 0; let length = buffered[1] & 127; let offset = 2;
        if (length === 126) { if (buffered.length < 4) break; length = buffered.readUInt16BE(2); offset = 4; }
        else if (length === 127) {
          if (buffered.length < 10) break;
          const value = buffered.readBigUInt64BE(2); if (value > 1_048_576n) throw new Error('Fixture WebSocket frame exceeded');
          length = Number(value); offset = 10;
        }
        if (length > 1_048_576) throw new Error('Fixture WebSocket frame exceeded');
        const start = offset + (masked ? 4 : 0); const end = start + length;
        if (buffered.length < end) break;
        const frame = buffered.subarray(0, end); let forward = true;
        if (frame[0] === 0x82) {
          const payload = Buffer.from(frame.subarray(start));
          if (masked) for (let index = 0; index < payload.length; index += 1) payload[index] ^= frame[offset + index % 4];
          forward = observe(payload) !== false;
        }
        if (forward) this.push(frame);
        buffered = buffered.subarray(end);
      }
      done();
    } catch (error) { done(error); }
  } });
}
