import { describe, expect, it } from 'vitest';

import { accessCredentialNeedsRefresh, type RegistrationSession } from './api';

function registration(accessExpiresAt: string): RegistrationSession {
  return {
    accessExpiresAt,
    accessToken: 'a'.repeat(43),
    credentialId: '1'.repeat(32),
    endpointId: '2'.repeat(32),
    kemJkt: 'kem-jkt',
    signingJkt: 'signing-jkt',
    tokenType: 'DPoP',
  };
}

describe('access credential refresh window', () => {
  it('refreshes expired, malformed, and near-expiry credentials', () => {
    const now = Date.parse('2030-01-01T00:00:00Z');
    expect(accessCredentialNeedsRefresh(registration('invalid'), now)).toBe(true);
    expect(accessCredentialNeedsRefresh(registration('2029-12-31T23:59:59Z'), now)).toBe(true);
    expect(accessCredentialNeedsRefresh(registration('2030-01-01T00:01:00Z'), now)).toBe(true);
  });

  it('keeps a credential with more than one minute remaining', () => {
    const now = Date.parse('2030-01-01T00:00:00Z');
    expect(accessCredentialNeedsRefresh(registration('2030-01-01T00:01:01Z'), now)).toBe(false);
  });
});
