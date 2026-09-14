export function hex(bytes: Uint8Array): string { return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join(''); }
export function idBytes(id: string): Uint8Array {
  if (!/^[0-9a-f]{32}$/u.test(id) || /^0+$/u.test(id)) throw new Error('Invalid conversation identity');
  return Uint8Array.from(id.match(/../gu) ?? [], (byte) => Number.parseInt(byte, 16));
}
