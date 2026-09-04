package domain

import "time"

const (
	MaxKeyPackageEncapsulatedKeyBytes = 512
	MaxKeyPackageCiphertextBytes      = 16 * 1024

	// CryptoSuiteMSSAWPSuite0001 is the only key-package suite accepted by AWP v1.
	CryptoSuiteMSSAWPSuite0001 uint16 = 1
)

func (value KeyPackageStatus) Valid() bool {
	switch value {
	case KeyPackageStatusPending, KeyPackageStatusDelivered, KeyPackageStatusAcknowledged, KeyPackageStatusExpired, KeyPackageStatusRevoked:
		return true
	default:
		return false
	}
}

// ValidateKeyPackageCryptoSuite rejects every suite other than the frozen AWP v1 suite.
// Unknown non-zero values are not treated as forward-compatible input.
func ValidateKeyPackageCryptoSuite(value uint16) error {
	switch value {
	case 0:
		return NewProblem(CodeInvalidArgument, "key package crypto suite is required", nil)
	case CryptoSuiteMSSAWPSuite0001:
		return nil
	default:
		return NewProblem(CodeSecurityViolation, "key package crypto suite is unsupported", nil)
	}
}

// AllowsSessionKeyPackage is deliberately an allow-list: any future or corrupt
// endpoint state remains ineligible until an Accepted design explicitly opts in.
func (value EndpointStatus) AllowsSessionKeyPackage() bool {
	return value == EndpointStatusActive
}

// ValidateNextKeyPackageGeneration binds package issuance to the Accepted
// WAITING_KEY and REKEY_REQUIRED states and to the next exact generation.
func (value Session) ValidateNextKeyPackageGeneration(generation uint64) error {
	if generation == 0 {
		return NewProblem(CodeInvalidArgument, "key package generation is required", nil)
	}
	switch value.Status {
	case SessionStatusWaitingKey:
		if value.CurrentKeyGeneration != 0 || generation != 1 {
			return NewProblem(CodeInvalidState, "initial key package must use generation 1", nil)
		}
	case SessionStatusRekeyRequired:
		if value.CurrentKeyGeneration == 0 || value.CurrentKeyGeneration == ^uint64(0) || generation != value.CurrentKeyGeneration+1 {
			return NewProblem(CodeInvalidState, "rekey package must use the next generation", nil)
		}
	default:
		return NewProblem(CodeInvalidState, "session does not accept a key package in status "+string(value.Status), nil)
	}
	return nil
}

func (value SessionKeyPackage) Validate(maxEncapsulatedKeyBytes, maxCiphertextBytes int) error {
	if value.ID.IsZero() || value.SessionID.IsZero() || value.IssuerABAEndpointID.IsZero() || value.RecipientHCEndpointID.IsZero() || value.IssuerCredentialID.IsZero() {
		return NewProblem(CodeInvalidArgument, "key package identifiers must be non-zero", nil)
	}
	if value.IssuerABAEndpointID == value.RecipientHCEndpointID {
		return NewProblem(CodeSecurityViolation, "key package issuer and recipient must be distinct", nil)
	}
	if value.Generation == 0 || !value.Status.Valid() {
		return NewProblem(CodeInvalidArgument, "key package generation or status is invalid", nil)
	}
	if err := ValidateKeyPackageCryptoSuite(value.CryptoSuite); err != nil {
		return err
	}
	if maxEncapsulatedKeyBytes <= 0 {
		maxEncapsulatedKeyBytes = MaxKeyPackageEncapsulatedKeyBytes
	}
	if maxCiphertextBytes <= 0 {
		maxCiphertextBytes = MaxKeyPackageCiphertextBytes
	}
	if len(value.EncapsulatedKey) == 0 || len(value.EncapsulatedKey) > maxEncapsulatedKeyBytes || len(value.Ciphertext) == 0 || len(value.Ciphertext) > maxCiphertextBytes {
		return NewProblem(CodeResourceLimit, "key package encrypted material is outside the allowed range", nil)
	}
	if allZero(value.ContextHash[:]) || len(value.IssuerSignature) != 64 || allZero(value.IssuerSignature) {
		return NewProblem(CodeInvalidArgument, "key package context hash or signature is invalid", nil)
	}
	if value.CreatedAt.IsZero() || !value.ExpiresAt.After(value.CreatedAt) {
		return NewProblem(CodeInvalidArgument, "key package timestamps are invalid", nil)
	}
	if value.Status == KeyPackageStatusAcknowledged && value.AcknowledgedAt == nil {
		return NewProblem(CodeInvalidState, "acknowledged key package requires acknowledged_at", nil)
	}
	if value.AcknowledgedAt != nil && value.AcknowledgedAt.Before(value.CreatedAt) {
		return NewProblem(CodeInvalidArgument, "key package acknowledgment precedes creation", nil)
	}
	return nil
}

func (value *SessionKeyPackage) Acknowledge(recipient ID, now time.Time) error {
	if value == nil {
		return NewProblem(CodeInvalidArgument, "key package is required", nil)
	}
	if recipient.IsZero() || recipient != value.RecipientHCEndpointID {
		return NewProblem(CodeSecurityViolation, "key package recipient does not match", nil)
	}
	if value.Status == KeyPackageStatusAcknowledged {
		return nil
	}
	if value.Status != KeyPackageStatusPending && value.Status != KeyPackageStatusDelivered {
		return NewProblem(CodeInvalidState, "key package cannot be acknowledged from "+string(value.Status), nil)
	}
	if now.IsZero() || !now.Before(value.ExpiresAt) {
		return NewProblem(CodeExpired, "key package is expired", nil)
	}
	value.Status = KeyPackageStatusAcknowledged
	value.AcknowledgedAt = &now
	return nil
}
