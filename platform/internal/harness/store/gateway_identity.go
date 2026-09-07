package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) AuthenticateAccessToken(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (domain.Endpoint, domain.EndpointCredential, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	if zeroHash(tokenHash) || now.IsZero() {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint credential is invalid", nil)
	}
	var credentialRecord credentialRow
	if err := store.db.WithContext(ctx).Where("token_hash = ?", hashString(tokenHash)).Take(&credentialRecord).Error; err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, gatewayCredentialError(err)
	}
	credential, err := credentialFromRow(credentialRecord)
	if err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	var endpointRecord endpointRow
	if err := store.db.WithContext(ctx).Where("id = ?", credentialRecord.EndpointID).Take(&endpointRecord).Error; err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, gatewayCredentialError(err)
	}
	endpoint, err := endpointFromRow(endpointRecord)
	if err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	if endpoint.Status != domain.EndpointStatusActive || endpoint.RevokedAt != nil ||
		credential.Status == domain.CredentialStatusRevoked || credential.Status == domain.CredentialStatusRotated || credential.RevokedAt != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeRevoked, "endpoint credential is unavailable", nil)
	}
	if credential.Status == domain.CredentialStatusExpired || !now.Before(credential.ExpiresAt) {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeExpired, "endpoint credential is expired", nil)
	}
	if credential.Status != domain.CredentialStatusActive || credential.EndpointID != endpoint.ID ||
		credential.FamilyID != endpoint.CredentialFamilyID || credential.SigningJKT != endpoint.SigningJKT {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeSecurityViolation, "endpoint credential binding is invalid", nil)
	}
	return endpoint, credential, nil
}

func (store *Store) GetEndpointCredential(
	ctx context.Context,
	endpointID domain.ID,
	credentialID domain.ID,
	now time.Time,
) (domain.Endpoint, domain.EndpointCredential, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	if endpointID.IsZero() || credentialID.IsZero() || now.IsZero() {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint credential lookup is invalid", nil)
	}
	var credentialRecord credentialRow
	if err := store.db.WithContext(ctx).Where("id = ? AND endpoint_id = ?", credentialID.String(), endpointID.String()).Take(&credentialRecord).Error; err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, gatewayCredentialError(err)
	}
	credential, err := credentialFromRow(credentialRecord)
	if err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	var endpointRecord endpointRow
	if err := store.db.WithContext(ctx).Where("id = ?", endpointID.String()).Take(&endpointRecord).Error; err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, gatewayCredentialError(err)
	}
	endpoint, err := endpointFromRow(endpointRecord)
	if err != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, err
	}
	if endpoint.Status != domain.EndpointStatusActive || credential.Status != domain.CredentialStatusActive ||
		endpoint.RevokedAt != nil || credential.RevokedAt != nil {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeRevoked, "endpoint credential is unavailable", nil)
	}
	if !now.Before(credential.ExpiresAt) {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeExpired, "endpoint credential is expired", nil)
	}
	if credential.FamilyID != endpoint.CredentialFamilyID || credential.SigningJKT != endpoint.SigningJKT {
		return domain.Endpoint{}, domain.EndpointCredential{}, domain.NewProblem(domain.CodeSecurityViolation, "endpoint credential binding is invalid", nil)
	}
	return endpoint, credential, nil
}

func (store *Store) InspectTicket(
	ctx context.Context,
	tokenHash [32]byte,
	now time.Time,
) (domain.WSTicket, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.WSTicket{}, err
	}
	if zeroHash(tokenHash) || now.IsZero() {
		return domain.WSTicket{}, domain.NewProblem(domain.CodeInvalidArgument, "WebSocket ticket lookup is invalid", nil)
	}
	var row ticketRow
	if err := store.db.WithContext(ctx).Where("token_hash = ?", hashString(tokenHash)).Take(&row).Error; err != nil {
		return domain.WSTicket{}, notFoundOr("inspect WebSocket ticket", "WebSocket ticket is unavailable", err)
	}
	ticket, err := ticketFromRow(row)
	if err != nil {
		return domain.WSTicket{}, err
	}
	switch ticket.Status {
	case domain.TicketStatusIssued:
	case domain.TicketStatusConsumed:
		return domain.WSTicket{}, domain.NewProblem(domain.CodeConflict, "WebSocket ticket was already consumed", nil)
	case domain.TicketStatusRevoked:
		return domain.WSTicket{}, domain.NewProblem(domain.CodeRevoked, "WebSocket ticket is revoked", nil)
	default:
		return domain.WSTicket{}, domain.NewProblem(domain.CodeExpired, "WebSocket ticket is expired", nil)
	}
	if !now.Before(ticket.ExpiresAt) {
		return domain.WSTicket{}, domain.NewProblem(domain.CodeExpired, "WebSocket ticket is expired", nil)
	}
	return ticket, nil
}

func (store *Store) NextConnectionGeneration(
	ctx context.Context,
	endpointID domain.ID,
	now time.Time,
) (uint64, error) {
	if err := requireStore(store, ctx); err != nil {
		return 0, err
	}
	if endpointID.IsZero() || now.IsZero() {
		return 0, domain.NewProblem(domain.CodeInvalidArgument, "connection generation input is invalid", nil)
	}
	row := connectionGenerationRow{EndpointID: endpointID.String(), Generation: 1, UpdatedAt: now}
	result := store.db.WithContext(ctx).Clauses(
		clause.OnConflict{
			Columns: []clause.Column{{Name: "endpoint_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"generation": gorm.Expr("? + 1", clause.Column{Table: "harness_connection_generations", Name: "generation"}), "updated_at": now,
			}),
		},
		clause.Returning{Columns: []clause.Column{{Name: "generation"}}},
	).Create(&row)
	if result.Error != nil {
		return 0, normalizeConcurrencyError("allocate connection generation", result.Error)
	}
	if row.Generation == 0 {
		return 0, domain.NewProblem(domain.CodeConflict, "connection generation was not returned", nil)
	}
	return row.Generation, nil
}

func (store *Store) MarkEndpointSeen(ctx context.Context, endpointID domain.ID, now time.Time) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if endpointID.IsZero() || now.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "endpoint presence input is invalid", nil)
	}
	result := store.db.WithContext(ctx).Model(new(endpointRow)).Where(
		"id = ? AND status = ? AND revoked_at IS NULL",
		endpointID.String(), string(domain.EndpointStatusActive),
	).Updates(map[string]any{
		"last_seen_at": now, "updated_at": now, "row_version": gorm.Expr("row_version + 1"),
	})
	if result.Error != nil {
		return classifyPersistence(result.Error, "mark endpoint presence")
	}
	if result.RowsAffected != 1 {
		return domain.NewProblem(domain.CodeNotFound, "active endpoint was not found", nil)
	}
	return nil
}

func (store *Store) UseDPoPReplay(
	ctx context.Context,
	jkt string,
	jti string,
	now time.Time,
	expiresAt time.Time,
	maxEntries int64,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if strings.TrimSpace(jkt) == "" || strings.TrimSpace(jti) == "" || now.IsZero() ||
		!expiresAt.After(now) || maxEntries <= 0 {
		return domain.NewProblem(domain.CodeInvalidArgument, "DPoP replay record is invalid", nil)
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("expires_at <= ?", now).Delete(new(dpopReplayRow)).Error; err != nil {
			return fmt.Errorf("delete expired DPoP replay records: %w", err)
		}
		var existing int64
		if err := tx.Model(new(dpopReplayRow)).Where("jkt = ? AND jti = ?", strings.TrimSpace(jkt), strings.TrimSpace(jti)).Count(&existing).Error; err != nil {
			return fmt.Errorf("check DPoP replay record: %w", err)
		}
		if existing != 0 {
			return domain.NewProblem(domain.CodeConflict, "DPoP proof was already used", nil)
		}
		var count int64
		if err := tx.Model(new(dpopReplayRow)).Count(&count).Error; err != nil {
			return fmt.Errorf("count DPoP replay records: %w", err)
		}
		if count >= maxEntries {
			return domain.NewProblem(domain.CodeResourceLimit, "DPoP replay cache reached its limit", nil)
		}
		row := dpopReplayRow{JKT: strings.TrimSpace(jkt), JTI: strings.TrimSpace(jti), ExpiresAt: expiresAt, CreatedAt: now}
		if err := tx.Create(&row).Error; err != nil {
			return classifyPersistence(err, "store DPoP replay record")
		}
		return nil
	})
}

func (store *Store) PutEndpointNonce(
	ctx context.Context,
	endpointID domain.ID,
	nonceHash [32]byte,
	now time.Time,
	expiresAt time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if endpointID.IsZero() || zeroHash(nonceHash) || now.IsZero() || !expiresAt.After(now) {
		return domain.NewProblem(domain.CodeInvalidArgument, "endpoint nonce is invalid", nil)
	}
	row := endpointNonceRow{
		EndpointID: endpointID.String(), NonceHash: hashString(nonceHash),
		IssuedAt: now, ExpiresAt: expiresAt,
	}
	if err := store.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "endpoint_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"nonce_hash": row.NonceHash, "issued_at": now, "expires_at": expiresAt,
			"row_version": gorm.Expr("? + 1", clause.Column{Table: "harness_endpoint_nonces", Name: "row_version"}),
		}),
	}).Create(&row).Error; err != nil {
		return classifyPersistence(err, "store endpoint nonce")
	}
	return nil
}

func (store *Store) GetEndpointNonceHash(
	ctx context.Context,
	endpointID domain.ID,
	now time.Time,
) ([32]byte, error) {
	if err := requireStore(store, ctx); err != nil {
		return [32]byte{}, err
	}
	if endpointID.IsZero() || now.IsZero() {
		return [32]byte{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint nonce lookup is invalid", nil)
	}
	var row endpointNonceRow
	if err := store.db.WithContext(ctx).Where("endpoint_id = ?", endpointID.String()).Take(&row).Error; err != nil {
		return [32]byte{}, notFoundOr("read endpoint nonce", "endpoint nonce is unavailable", err)
	}
	if !now.Before(row.ExpiresAt) {
		return [32]byte{}, domain.NewProblem(domain.CodeExpired, "endpoint nonce is expired", nil)
	}
	value, err := parseHash(row.NonceHash)
	if err != nil {
		return [32]byte{}, fmt.Errorf("decode endpoint nonce hash: %w", err)
	}
	return value, nil
}

func (store *Store) RotateEndpointNonce(
	ctx context.Context,
	endpointID domain.ID,
	expectedHash [32]byte,
	nextHash [32]byte,
	now time.Time,
	nextExpiresAt time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if endpointID.IsZero() || zeroHash(expectedHash) || zeroHash(nextHash) || now.IsZero() || !nextExpiresAt.After(now) {
		return domain.NewProblem(domain.CodeInvalidArgument, "endpoint nonce rotation is invalid", nil)
	}
	result := store.db.WithContext(ctx).Model(new(endpointNonceRow)).Where(
		"endpoint_id = ? AND nonce_hash = ? AND expires_at > ?",
		endpointID.String(), hashString(expectedHash), now,
	).Updates(map[string]any{
		"nonce_hash": hashString(nextHash), "issued_at": now, "expires_at": nextExpiresAt,
		"row_version": gorm.Expr("row_version + 1"),
	})
	if result.Error != nil {
		return classifyPersistence(result.Error, "rotate endpoint nonce")
	}
	if result.RowsAffected != 1 {
		return domain.NewProblem(domain.CodeConflict, "endpoint nonce changed concurrently", nil)
	}
	return nil
}

func credentialFromRow(row credentialRow) (domain.EndpointCredential, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.EndpointCredential{}, err
	}
	endpointID, err := parseID(row.EndpointID)
	if err != nil {
		return domain.EndpointCredential{}, err
	}
	familyID, err := parseID(row.FamilyID)
	if err != nil {
		return domain.EndpointCredential{}, err
	}
	rotatedFrom, err := parseOptionalID(row.RotatedFrom)
	if err != nil {
		return domain.EndpointCredential{}, err
	}
	tokenHash, err := parseHash(row.TokenHash)
	if err != nil {
		return domain.EndpointCredential{}, fmt.Errorf("decode endpoint credential hash: %w", err)
	}
	var scopes []string
	if err := json.Unmarshal([]byte(row.ScopesJSON), &scopes); err != nil {
		return domain.EndpointCredential{}, fmt.Errorf("decode endpoint credential scopes: %w", err)
	}
	return domain.EndpointCredential{
		ID: id, EndpointID: endpointID, FamilyID: familyID, TokenHash: tokenHash,
		SigningJKT: row.SigningJKT, Scopes: scopes, Status: domain.CredentialStatus(row.Status),
		ExpiresAt: row.ExpiresAt, RotatedFrom: rotatedFrom, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, RevokedAt: cloneTime(row.RevokedAt),
	}, nil
}

func parseOptionalID(value string) (domain.ID, error) {
	if strings.TrimSpace(value) == "" {
		return domain.ID{}, nil
	}
	return parseID(value)
}

func gatewayCredentialError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewProblem(domain.CodeNotFound, "endpoint credential is unavailable", nil)
	}
	return fmt.Errorf("authenticate endpoint credential: %w", err)
}
