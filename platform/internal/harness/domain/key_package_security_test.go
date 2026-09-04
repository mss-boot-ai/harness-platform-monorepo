package domain

import (
	"bytes"
	"crypto/sha256"
	"math"
	"testing"
	"time"
)

func validSuiteTestKeyPackage(now time.Time) SessionKeyPackage {
	return SessionKeyPackage{
		ID:                    testID(101),
		SessionID:             testID(102),
		Generation:            1,
		IssuerABAEndpointID:   testID(103),
		RecipientHCEndpointID: testID(104),
		CryptoSuite:           CryptoSuiteMSSAWPSuite0001,
		EncapsulatedKey:       []byte{1, 2, 3},
		Ciphertext:            []byte{4, 5, 6},
		ContextHash:           sha256.Sum256([]byte("suite-context")),
		IssuerSignature:       bytes.Repeat([]byte{7}, 64),
		IssuerCredentialID:    testID(105),
		Status:                KeyPackageStatusPending,
		ExpiresAt:             now.Add(time.Minute),
		CreatedAt:             now,
	}
}

func TestSessionKeyPackageCryptoSuiteFailsClosed(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	tests := []struct {
		name  string
		suite uint16
		code  ErrorCode
	}{
		{name: "suite 0001", suite: CryptoSuiteMSSAWPSuite0001},
		{name: "zero", suite: 0, code: CodeInvalidArgument},
		{name: "unknown", suite: 2, code: CodeSecurityViolation},
		{name: "maximum uint16", suite: math.MaxUint16, code: CodeSecurityViolation},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := validSuiteTestKeyPackage(now)
			value.CryptoSuite = test.suite
			err := value.Validate(0, 0)
			if test.code == "" {
				if err != nil {
					t.Fatalf("Validate suite %d: %v", test.suite, err)
				}
				return
			}
			if !HasCode(err, test.code) {
				t.Fatalf("Validate suite %d error = %v, want %s", test.suite, err, test.code)
			}
		})
	}
}

func TestSessionKeyPackageAllowedStatesAndGeneration(t *testing.T) {
	tests := []struct {
		name       string
		status     SessionStatus
		current    uint64
		generation uint64
		allowed    bool
	}{
		{name: "initial waiting key", status: SessionStatusWaitingKey, current: 0, generation: 1, allowed: true},
		{name: "rekey next generation", status: SessionStatusRekeyRequired, current: 4, generation: 5, allowed: true},
		{name: "creating", status: SessionStatusCreating, generation: 1},
		{name: "active", status: SessionStatusActive, current: 1, generation: 2},
		{name: "closed", status: SessionStatusClosed, current: 1, generation: 2},
		{name: "aba revoked", status: SessionStatusABARevoked, current: 1, generation: 2},
		{name: "failed", status: SessionStatusFailed, generation: 1},
		{name: "waiting wrong generation", status: SessionStatusWaitingKey, generation: 2},
		{name: "rekey skips generation", status: SessionStatusRekeyRequired, current: 4, generation: 6},
		{name: "rekey overflow", status: SessionStatusRekeyRequired, current: math.MaxUint64, generation: math.MaxUint64},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session := Session{Status: test.status, CurrentKeyGeneration: test.current}
			err := session.ValidateNextKeyPackageGeneration(test.generation)
			if test.allowed {
				if err != nil {
					t.Fatalf("ValidateNextKeyPackageGeneration: %v", err)
				}
				return
			}
			if !HasCode(err, CodeInvalidState) {
				t.Fatalf("error = %v, want %s", err, CodeInvalidState)
			}
		})
	}
}

func TestEndpointKeyPackageEligibilityIsAllowListed(t *testing.T) {
	for _, status := range []EndpointStatus{
		EndpointStatusPending,
		EndpointStatusSuspended,
		EndpointStatusRevoked,
		EndpointStatus("EXPIRED"),
		EndpointStatus("COMPROMISED"),
		EndpointStatus("FUTURE_STATE"),
	} {
		if status.AllowsSessionKeyPackage() {
			t.Fatalf("status %q unexpectedly allows key packages", status)
		}
	}
	if !EndpointStatusActive.AllowsSessionKeyPackage() {
		t.Fatal("ACTIVE endpoint must allow key packages")
	}
}
