package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PutKeyPackageOutcome string

const (
	PutKeyPackageStored    PutKeyPackageOutcome = "STORED"
	PutKeyPackageDuplicate PutKeyPackageOutcome = "DUPLICATE"
)

type PutKeyPackageResult struct {
	Outcome PutKeyPackageOutcome
	Package domain.SessionKeyPackage
}

func (store *Store) PutSessionKeyPackage(
	ctx context.Context,
	owner string,
	tenant string,
	value domain.SessionKeyPackage,
) (PutKeyPackageResult, error) {
	if err := requireM1Scope(store, ctx, owner); err != nil {
		return PutKeyPackageResult{}, err
	}
	if err := value.Validate(domain.MaxKeyPackageEncapsulatedKeyBytes, domain.MaxKeyPackageCiphertextBytes); err != nil {
		return PutKeyPackageResult{}, err
	}
	if value.Status != domain.KeyPackageStatusPending {
		return PutKeyPackageResult{}, domain.NewProblem(domain.CodeInvalidState, "new key package must be pending", nil)
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)
	const sqliteAttempts = 8
	for attempt := 0; attempt < sqliteAttempts; attempt++ {
		result, err := store.putSessionKeyPackageOnce(ctx, owner, tenant, value)
		if err == nil {
			return result, nil
		}
		if store.db.Dialector.Name() != "sqlite" || !isSQLiteConcurrencyError(err) || attempt == sqliteAttempts-1 {
			return PutKeyPackageResult{}, normalizeConcurrencyError("key package changed concurrently", err)
		}
		if err := waitForSQLiteRetry(ctx, attempt); err != nil {
			return PutKeyPackageResult{}, err
		}
	}
	return PutKeyPackageResult{}, domain.NewProblem(domain.CodeConflict, "key package changed concurrently", nil)
}

func (store *Store) putSessionKeyPackageOnce(
	ctx context.Context,
	owner string,
	tenant string,
	value domain.SessionKeyPackage,
) (PutKeyPackageResult, error) {
	row := keyPackageToRow(value)
	var resultValue PutKeyPackageResult
	var semanticConflict error
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		session, err := lockKeyPackageSession(tx, value.SessionID, owner, tenant)
		if err != nil {
			return err
		}
		sessionValue, err := sessionFromRow(session)
		if err != nil {
			return err
		}
		if session.ABAEndpointID != value.IssuerABAEndpointID.String() || session.HCEndpointID != value.RecipientHCEndpointID.String() {
			return domain.NewProblem(domain.CodeSecurityViolation, "key package endpoints do not match the session", nil)
		}
		if err := sessionValue.ValidateNextKeyPackageGeneration(value.Generation); err != nil {
			return err
		}
		if err := fenceKeyPackageSession(tx, session, owner, tenant); err != nil {
			return err
		}

		issuer, err := lockKeyPackageEndpoint(tx, value.IssuerABAEndpointID, owner, tenant)
		if err != nil {
			return err
		}
		if issuer.Type != string(domain.EndpointTypeABA) {
			return domain.NewProblem(domain.CodeSecurityViolation, "key package issuer is not an ABA endpoint", nil)
		}
		if err := requireKeyPackageEndpointActive("issuer ABA", issuer.Status); err != nil {
			return err
		}

		recipient, err := lockKeyPackageEndpoint(tx, value.RecipientHCEndpointID, owner, tenant)
		if err != nil {
			return err
		}
		if recipient.Type != string(domain.EndpointTypeHCWeb) && recipient.Type != string(domain.EndpointTypeHCReference) {
			return domain.NewProblem(domain.CodeSecurityViolation, "key package recipient is not an HC endpoint", nil)
		}
		if err := requireKeyPackageEndpointActive("recipient HC", recipient.Status); err != nil {
			return err
		}

		credential, err := lockKeyPackageIssuerCredential(tx, value.IssuerCredentialID, value.IssuerABAEndpointID)
		if err != nil {
			return err
		}
		if credential.Status != string(domain.CredentialStatusActive) || credential.RevokedAt != nil ||
			!credential.ExpiresAt.After(value.CreatedAt) || value.CreatedAt.Before(credential.CreatedAt) ||
			credential.FamilyID != issuer.CredentialFamilyID || credential.SigningJKT != issuer.SigningJKT {
			return domain.NewProblem(domain.CodeSecurityViolation, "key package issuer credential is unavailable", nil)
		}

		insert := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if insert.Error != nil {
			return classifyPersistence(insert.Error, "store key package")
		}
		if insert.RowsAffected == 1 {
			resultValue = PutKeyPackageResult{Outcome: PutKeyPackageStored, Package: value}
			return nil
		}
		var existing keyPackageRow
		if err := tx.Where("id = ?", row.ID).Or(
			"session_id = ? AND generation = ? AND recipient_hc_endpoint_id = ?",
			row.SessionID, row.Generation, row.RecipientHCEndpointID,
		).First(&existing).Error; err != nil {
			return notFoundOr("read conflicting key package", "key package conflict could not be resolved", err)
		}
		if !sameKeyPackage(existing, row) {
			semanticConflict = domain.NewProblem(domain.CodeConflict, "key package idempotency key was reused with different encrypted content", nil)
			return nil
		}
		existingValue, err := keyPackageFromRow(existing)
		if err != nil {
			return err
		}
		resultValue = PutKeyPackageResult{Outcome: PutKeyPackageDuplicate, Package: existingValue}
		return nil
	})
	if err != nil {
		return PutKeyPackageResult{}, err
	}
	if semanticConflict != nil {
		return PutKeyPackageResult{}, semanticConflict
	}
	return resultValue, nil
}

func lockKeyPackageSession(tx *gorm.DB, id domain.ID, owner, tenant string) (sessionRow, error) {
	var session sessionRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
		&session,
		"id = ? AND owner_user_id = ? AND tenant_id = ?",
		id.String(), owner, tenant,
	).Error; err != nil {
		return sessionRow{}, notFoundOr("lock key package session", "session was not found", err)
	}
	return session, nil
}

func fenceKeyPackageSession(tx *gorm.DB, session sessionRow, owner, tenant string) error {
	if tx.Dialector.Name() != "sqlite" {
		return nil
	}
	result := tx.Model(new(sessionRow)).Where(
		"id = ? AND owner_user_id = ? AND tenant_id = ? AND row_version = ? AND status = ?",
		session.ID, owner, tenant, session.RowVersion, session.Status,
	).UpdateColumn("row_version", gorm.Expr("row_version"))
	if result.Error != nil {
		return fmt.Errorf("fence key package session: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return domain.NewProblem(domain.CodeConflict, "key package session changed concurrently", nil)
	}
	return nil
}

func lockKeyPackageEndpoint(tx *gorm.DB, id domain.ID, owner, tenant string) (endpointRow, error) {
	var endpoint endpointRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
		&endpoint,
		"id = ? AND owner_user_id = ? AND tenant_id = ?",
		id.String(), owner, tenant,
	).Error; err != nil {
		return endpointRow{}, notFoundOr("lock key package endpoint", "key package endpoint was not found", err)
	}
	return endpoint, nil
}

func lockKeyPackageIssuerCredential(tx *gorm.DB, id, issuerEndpointID domain.ID) (credentialRow, error) {
	var credential credentialRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
		&credential,
		"id = ? AND endpoint_id = ?",
		id.String(), issuerEndpointID.String(),
	).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return credentialRow{}, domain.NewProblem(domain.CodeSecurityViolation, "key package issuer credential is unavailable", err)
		}
		return credentialRow{}, fmt.Errorf("lock key package issuer credential: %w", err)
	}
	return credential, nil
}

func requireKeyPackageEndpointActive(role, status string) error {
	value := domain.EndpointStatus(status)
	if value.AllowsSessionKeyPackage() {
		return nil
	}
	if value == domain.EndpointStatusRevoked {
		return domain.NewProblem(domain.CodeRevoked, "key package "+role+" endpoint is revoked", nil)
	}
	return domain.NewProblem(domain.CodeSecurityViolation, "key package "+role+" endpoint is not active", nil)
}

func (store *Store) ListSessionKeyPackages(
	ctx context.Context,
	sessionID domain.ID,
	owner string,
	tenant string,
	limit int,
) ([]domain.SessionKeyPackage, error) {
	if err := requireM1Scope(store, ctx, owner); err != nil {
		return nil, err
	}
	if sessionID.IsZero() {
		return nil, domain.NewProblem(domain.CodeInvalidArgument, "session ID is required", nil)
	}
	var sessionCount int64
	if err := store.db.WithContext(ctx).Model(new(sessionRow)).Where(
		"id = ? AND owner_user_id = ? AND tenant_id = ?",
		sessionID.String(), strings.TrimSpace(owner), strings.TrimSpace(tenant),
	).Count(&sessionCount).Error; err != nil {
		return nil, err
	}
	if sessionCount != 1 {
		return nil, domain.NewProblem(domain.CodeNotFound, "session was not found", nil)
	}
	var rows []keyPackageRow
	if err := store.db.WithContext(ctx).Where("session_id = ?", sessionID.String()).
		Order("generation DESC, created_at DESC").Limit(managementLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.SessionKeyPackage, 0, len(rows))
	for _, row := range rows {
		value, err := keyPackageFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) AcknowledgeSessionKeyPackage(
	ctx context.Context,
	id domain.ID,
	recipient domain.ID,
	owner string,
	tenant string,
	now time.Time,
) (domain.SessionKeyPackage, error) {
	if err := requireM1Scope(store, ctx, owner); err != nil {
		return domain.SessionKeyPackage{}, err
	}
	if id.IsZero() || recipient.IsZero() || now.IsZero() {
		return domain.SessionKeyPackage{}, domain.NewProblem(domain.CodeInvalidArgument, "key package acknowledgment is invalid", nil)
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)
	var updated domain.SessionKeyPackage
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Read immutable routing fields first, then acquire mutable locks in the
		// shared Session -> Endpoint -> Package order used by revocation.
		var candidate keyPackageRow
		if err := tx.First(&candidate, "id = ? AND recipient_hc_endpoint_id = ?", id.String(), recipient.String()).Error; err != nil {
			return notFoundOr("read key package", "key package was not found", err)
		}
		sessionID, err := parseID(candidate.SessionID)
		if err != nil {
			return err
		}
		session, err := lockKeyPackageSession(tx, sessionID, owner, tenant)
		if err != nil {
			return domain.NewProblem(domain.CodeNotFound, "key package was not found", err)
		}
		if session.HCEndpointID != recipient.String() {
			return domain.NewProblem(domain.CodeSecurityViolation, "key package recipient does not match the session", nil)
		}
		recipientEndpoint, err := lockKeyPackageEndpoint(tx, recipient, owner, tenant)
		if err != nil {
			return domain.NewProblem(domain.CodeNotFound, "key package was not found", err)
		}
		if err := requireKeyPackageEndpointActive("recipient HC", recipientEndpoint.Status); err != nil {
			return err
		}
		var row keyPackageRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
			&row,
			"id = ? AND session_id = ? AND recipient_hc_endpoint_id = ?",
			id.String(), candidate.SessionID, recipient.String(),
		).Error; err != nil {
			return notFoundOr("lock key package", "key package was not found", err)
		}
		value, err := keyPackageFromRow(row)
		if err != nil {
			return err
		}
		if err := value.Acknowledge(recipient, now); err != nil {
			return err
		}
		if row.Status == string(domain.KeyPackageStatusAcknowledged) {
			updated = value
			return nil
		}
		result := tx.Model(new(keyPackageRow)).Where("id = ? AND status IN ?", row.ID, []string{
			string(domain.KeyPackageStatusPending), string(domain.KeyPackageStatusDelivered),
		}).Updates(map[string]any{
			"status":          string(value.Status),
			"acknowledged_at": value.AcknowledgedAt,
		})
		if result.Error != nil {
			return fmt.Errorf("acknowledge key package: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "key package changed concurrently", nil)
		}
		updated = value
		return nil
	})
	return updated, normalizeConcurrencyError("key package acknowledgment changed concurrently", err)
}

func keyPackageToRow(value domain.SessionKeyPackage) keyPackageRow {
	return keyPackageRow{
		ID:                    value.ID.String(),
		SessionID:             value.SessionID.String(),
		Generation:            value.Generation,
		IssuerABAEndpointID:   value.IssuerABAEndpointID.String(),
		RecipientHCEndpointID: value.RecipientHCEndpointID.String(),
		CryptoSuite:           value.CryptoSuite,
		EncapsulatedKey:       bytes.Clone(value.EncapsulatedKey),
		Ciphertext:            bytes.Clone(value.Ciphertext),
		ContextHash:           hashString(value.ContextHash),
		IssuerSignature:       bytes.Clone(value.IssuerSignature),
		IssuerCredentialID:    value.IssuerCredentialID.String(),
		Status:                string(value.Status),
		ExpiresAt:             value.ExpiresAt,
		AcknowledgedAt:        cloneTime(value.AcknowledgedAt),
		CreatedAt:             value.CreatedAt,
	}
}

func keyPackageFromRow(row keyPackageRow) (domain.SessionKeyPackage, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	sessionID, err := parseID(row.SessionID)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	issuerID, err := parseID(row.IssuerABAEndpointID)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	recipientID, err := parseID(row.RecipientHCEndpointID)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	credentialID, err := parseID(row.IssuerCredentialID)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	contextHash, err := parseHash(row.ContextHash)
	if err != nil {
		return domain.SessionKeyPackage{}, err
	}
	return domain.SessionKeyPackage{
		ID:                    id,
		SessionID:             sessionID,
		Generation:            row.Generation,
		IssuerABAEndpointID:   issuerID,
		RecipientHCEndpointID: recipientID,
		CryptoSuite:           row.CryptoSuite,
		EncapsulatedKey:       bytes.Clone(row.EncapsulatedKey),
		Ciphertext:            bytes.Clone(row.Ciphertext),
		ContextHash:           contextHash,
		IssuerSignature:       bytes.Clone(row.IssuerSignature),
		IssuerCredentialID:    credentialID,
		Status:                domain.KeyPackageStatus(row.Status),
		ExpiresAt:             row.ExpiresAt,
		AcknowledgedAt:        cloneTime(row.AcknowledgedAt),
		CreatedAt:             row.CreatedAt,
	}, nil
}

func sameKeyPackage(left, right keyPackageRow) bool {
	return left.SessionID == right.SessionID && left.Generation == right.Generation &&
		left.IssuerABAEndpointID == right.IssuerABAEndpointID && left.RecipientHCEndpointID == right.RecipientHCEndpointID &&
		left.CryptoSuite == right.CryptoSuite && bytes.Equal(left.EncapsulatedKey, right.EncapsulatedKey) &&
		bytes.Equal(left.Ciphertext, right.Ciphertext) && left.ContextHash == right.ContextHash &&
		bytes.Equal(left.IssuerSignature, right.IssuerSignature) && left.IssuerCredentialID == right.IssuerCredentialID &&
		left.ExpiresAt.Equal(right.ExpiresAt)
}
