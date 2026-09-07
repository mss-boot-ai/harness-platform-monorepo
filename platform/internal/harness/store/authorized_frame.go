package store

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PutAuthorizedEndpointFrame validates the current session, both endpoint
// identities and the sender credential in the same transaction that stores the
// immutable encrypted frame. The Session -> Endpoint -> Credential lock order
// matches endpoint revocation and prevents a frame from committing after a
// close or revocation has already committed.
func (store *Store) PutAuthorizedEndpointFrame(
	ctx context.Context,
	owner string,
	tenant string,
	credentialID domain.ID,
	frame domain.EncryptedFrame,
	now time.Time,
) (bool, error) {
	if err := requireM1Scope(store, ctx, owner); err != nil {
		return false, err
	}
	if credentialID.IsZero() || now.IsZero() {
		return false, domain.NewProblem(domain.CodeInvalidArgument, "authorized frame credential and time are required", nil)
	}
	if err := frame.Validate(1 << 20); err != nil {
		return false, err
	}
	if frame.Status != domain.FrameStatusStored || frame.ReceivedAt.IsZero() || !frame.ExpiresAt.After(now) {
		return false, domain.NewProblem(domain.CodeInvalidState, "new authorized frame state is invalid", nil)
	}
	owner = strings.TrimSpace(owner)
	tenant = strings.TrimSpace(tenant)

	const sqliteAttempts = 8
	for attempt := 0; attempt < sqliteAttempts; attempt++ {
		outcome, err := store.putAuthorizedEndpointFrameOnce(ctx, owner, tenant, credentialID, frame, now)
		if err == nil {
			return outcome == PutFrameDuplicate, nil
		}
		if store.db.Dialector.Name() != "sqlite" || !isSQLiteConcurrencyError(err) || attempt == sqliteAttempts-1 {
			return false, normalizeConcurrencyError("encrypted frame authorization changed concurrently", err)
		}
		if err := waitForSQLiteRetry(ctx, attempt); err != nil {
			return false, err
		}
	}
	return false, domain.NewProblem(domain.CodeConflict, "encrypted frame authorization changed concurrently", nil)
}

func (store *Store) putAuthorizedEndpointFrameOnce(
	ctx context.Context,
	owner string,
	tenant string,
	credentialID domain.ID,
	frame domain.EncryptedFrame,
	now time.Time,
) (PutFrameOutcome, error) {
	row := frameToRow(frame)
	var outcome PutFrameOutcome
	var semanticConflict error
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session sessionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
			&session,
			"id = ? AND owner_user_id = ? AND tenant_id = ?",
			frame.SessionID.String(), owner, tenant,
		).Error; err != nil {
			return notFoundOr("lock encrypted frame session", "session was not found", err)
		}
		if session.Status != string(domain.SessionStatusActive) || session.CurrentKeyGeneration != frame.KeyGeneration {
			return domain.NewProblem(domain.CodeInvalidState, "encrypted frame session is not active for this generation", nil)
		}
		if err := fenceAuthorizedFrameSession(tx, session, owner, tenant); err != nil {
			return err
		}

		sender, receiver, err := lockAuthorizedFrameRouteEndpoints(
			tx, session, frame.Direction, owner, tenant,
		)
		if err != nil {
			return err
		}
		if err := validateAuthorizedFrameRoute(session, sender, receiver, frame); err != nil {
			return err
		}

		var credential credentialRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
			&credential,
			"id = ? AND endpoint_id = ?",
			credentialID.String(), sender.ID,
		).Error; err != nil {
			return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame sender credential is unavailable", err)
		}
		if credential.Status != string(domain.CredentialStatusActive) || credential.RevokedAt != nil ||
			credential.ExpiresAt.Before(now) || credential.ExpiresAt.Equal(now) || credential.CreatedAt.After(now) ||
			credential.FamilyID != sender.CredentialFamilyID || credential.SigningJKT != sender.SigningJKT {
			return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame sender credential is unavailable", nil)
		}

		var putErr error
		outcome, semanticConflict, putErr = putAuthorizedFrameRow(tx, row)
		return putErr
	})
	if err != nil {
		return "", err
	}
	if semanticConflict != nil {
		return "", semanticConflict
	}
	return outcome, nil
}

func lockAuthorizedFrameRouteEndpoints(
	tx *gorm.DB,
	session sessionRow,
	direction domain.Direction,
	owner string,
	tenant string,
) (endpointRow, endpointRow, error) {
	abaID, err := parseID(session.ABAEndpointID)
	if err != nil {
		return endpointRow{}, endpointRow{}, err
	}
	hcID, err := parseID(session.HCEndpointID)
	if err != nil {
		return endpointRow{}, endpointRow{}, err
	}
	aba, err := lockAuthorizedFrameEndpoint(tx, abaID, owner, tenant)
	if err != nil {
		return endpointRow{}, endpointRow{}, err
	}
	hc, err := lockAuthorizedFrameEndpoint(tx, hcID, owner, tenant)
	if err != nil {
		return endpointRow{}, endpointRow{}, err
	}
	switch direction {
	case domain.DirectionHCToABA:
		return hc, aba, nil
	case domain.DirectionABAToHC:
		return aba, hc, nil
	default:
		return endpointRow{}, endpointRow{}, domain.NewProblem(
			domain.CodeInvalidArgument, "encrypted frame direction is invalid", nil,
		)
	}
}

func putAuthorizedFrameRow(tx *gorm.DB, row frameRow) (PutFrameOutcome, error, error) {
	result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return "", nil, classifyPersistence(result.Error, "store encrypted frame")
	}
	if result.RowsAffected == 1 {
		return PutFrameStored, nil, nil
	}
	var existing frameRow
	if err := tx.Where("message_id = ?", row.MessageID).Or(
		"session_id = ? AND key_generation = ? AND sender_endpoint_id = ? AND sequence = ?",
		row.SessionID, row.KeyGeneration, row.SenderEndpointID, row.Sequence,
	).First(&existing).Error; err != nil {
		return "", nil, notFoundOr("read conflicting frame", "frame conflict could not be resolved", err)
	}
	if sameFrame(existing, row) {
		return PutFrameDuplicate, nil, nil
	}
	if err := tx.Model(new(frameRow)).Where("message_id = ?", existing.MessageID).
		Update("status", string(domain.FrameStatusConflict)).Error; err != nil {
		return "", nil, fmt.Errorf("mark frame conflict: %w", err)
	}
	return "", domain.NewProblem(
		domain.CodeConflict, "frame idempotency key was reused with different content", nil,
	), nil
}

func fenceAuthorizedFrameSession(tx *gorm.DB, session sessionRow, owner, tenant string) error {
	if tx.Dialector.Name() != "sqlite" {
		return nil
	}
	result := tx.Model(new(sessionRow)).Where(
		"id = ? AND owner_user_id = ? AND tenant_id = ? AND row_version = ? AND status = ? AND current_key_generation = ?",
		session.ID, owner, tenant, session.RowVersion, session.Status, session.CurrentKeyGeneration,
	).UpdateColumn("row_version", gorm.Expr("row_version"))
	if result.Error != nil {
		return fmt.Errorf("fence encrypted frame session: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return domain.NewProblem(domain.CodeConflict, "encrypted frame session changed concurrently", nil)
	}
	return nil
}

func lockAuthorizedFrameEndpoint(tx *gorm.DB, id domain.ID, owner, tenant string) (endpointRow, error) {
	var endpoint endpointRow
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(
		&endpoint,
		"id = ? AND owner_user_id = ? AND tenant_id = ?",
		id.String(), owner, tenant,
	).Error; err != nil {
		return endpointRow{}, notFoundOr("lock encrypted frame endpoint", "encrypted frame endpoint was not found", err)
	}
	if endpoint.Status != string(domain.EndpointStatusActive) || endpoint.RevokedAt != nil {
		if endpoint.Status == string(domain.EndpointStatusRevoked) {
			return endpointRow{}, domain.NewProblem(domain.CodeRevoked, "encrypted frame endpoint is revoked", nil)
		}
		return endpointRow{}, domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame endpoint is not active", nil)
	}
	return endpoint, nil
}

func validateAuthorizedFrameRoute(
	session sessionRow,
	sender endpointRow,
	receiver endpointRow,
	frame domain.EncryptedFrame,
) error {
	if sender.ID != frame.SenderEndpointID.String() || receiver.ID != frame.ReceiverEndpointID.String() {
		return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame endpoint binding is invalid", nil)
	}
	switch frame.Direction {
	case domain.DirectionHCToABA:
		if session.HCEndpointID != sender.ID || session.ABAEndpointID != receiver.ID ||
			(sender.Type != string(domain.EndpointTypeHCWeb) && sender.Type != string(domain.EndpointTypeHCReference)) ||
			receiver.Type != string(domain.EndpointTypeABA) {
			return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame route is invalid", nil)
		}
	case domain.DirectionABAToHC:
		if session.ABAEndpointID != sender.ID || session.HCEndpointID != receiver.ID ||
			sender.Type != string(domain.EndpointTypeABA) ||
			(receiver.Type != string(domain.EndpointTypeHCWeb) && receiver.Type != string(domain.EndpointTypeHCReference)) {
			return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame route is invalid", nil)
		}
	default:
		return domain.NewProblem(domain.CodeInvalidArgument, "encrypted frame direction is invalid", nil)
	}
	abaEndpointID, err := parseID(session.ABAEndpointID)
	if err != nil {
		return err
	}
	hcEndpointID, err := parseID(session.HCEndpointID)
	if err != nil {
		return err
	}
	expectedChannel, err := authorizedFrameChannelID(frame.SessionID, abaEndpointID, hcEndpointID)
	if err != nil || frame.ChannelID != expectedChannel {
		return domain.NewProblem(domain.CodeSecurityViolation, "encrypted frame channel is invalid", err)
	}
	return nil
}

func authorizedFrameChannelID(sessionID, abaEndpointID, hcEndpointID domain.ID) (domain.ID, error) {
	if sessionID.IsZero() || abaEndpointID.IsZero() || hcEndpointID.IsZero() || abaEndpointID == hcEndpointID {
		return domain.ID{}, fmt.Errorf("encrypted frame channel input is invalid")
	}
	digest := sha256.New()
	_, _ = digest.Write([]byte("mss-awp-channel-v1"))
	_, _ = digest.Write(sessionID[:])
	_, _ = digest.Write(abaEndpointID[:])
	_, _ = digest.Write(hcEndpointID[:])
	var id domain.ID
	copy(id[:], digest.Sum(nil)[:16])
	return id, nil
}
