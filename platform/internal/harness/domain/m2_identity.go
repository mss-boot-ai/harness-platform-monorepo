package domain

import (
	"strings"
	"time"
)

type HCRegistrationChallengeStatus string

const (
	HCRegistrationChallengePending  HCRegistrationChallengeStatus = "PENDING"
	HCRegistrationChallengeConsumed HCRegistrationChallengeStatus = "CONSUMED"
	HCRegistrationChallengeExpired  HCRegistrationChallengeStatus = "EXPIRED"
)

type HCRegistrationChallenge struct {
	ID            ID
	OwnerUserID   string
	TenantID      string
	Origin        string
	ChallengeHash [32]byte
	Status        HCRegistrationChallengeStatus
	ExpiresAt     time.Time
	ConsumedAt    *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
	RowVersion    uint64
}

func (challenge HCRegistrationChallenge) ValidateNew(now time.Time, maxTTL time.Duration) error {
	if challenge.ID.IsZero() || strings.TrimSpace(challenge.OwnerUserID) == "" ||
		strings.TrimSpace(challenge.Origin) == "" || allZero(challenge.ChallengeHash[:]) ||
		challenge.Status != HCRegistrationChallengePending || challenge.CreatedAt.IsZero() ||
		challenge.UpdatedAt.IsZero() || !challenge.ExpiresAt.After(now) || maxTTL <= 0 ||
		challenge.ExpiresAt.Sub(now) > maxTTL || challenge.ConsumedAt != nil {
		return NewProblem(CodeInvalidArgument, "HC registration challenge is invalid", nil)
	}
	return nil
}

type RefreshCredential struct {
	ID          ID
	EndpointID  ID
	FamilyID    ID
	TokenHash   [32]byte
	SigningJKT  string
	Status      CredentialStatus
	ExpiresAt   time.Time
	RotatedFrom ID
	RotatedTo   ID
	UsedAt      *time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (credential RefreshCredential) Validate(endpoint Endpoint, now time.Time) error {
	if credential.ID.IsZero() || credential.EndpointID != endpoint.ID ||
		credential.FamilyID != endpoint.CredentialFamilyID ||
		credential.SigningJKT != endpoint.SigningJKT || allZero(credential.TokenHash[:]) ||
		credential.Status != CredentialStatusActive || !credential.ExpiresAt.After(now) ||
		credential.CreatedAt.IsZero() || credential.UpdatedAt.IsZero() ||
		credential.UsedAt != nil || credential.RevokedAt != nil || !credential.RotatedTo.IsZero() {
		return NewProblem(CodeSecurityViolation, "refresh credential binding is invalid", nil)
	}
	return nil
}
