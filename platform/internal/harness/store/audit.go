package store

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func (store *Store) AppendAudit(ctx context.Context, event domain.SecurityAuditEvent) error {
	if err := requireM1Scope(store, ctx, event.OwnerUserID); err != nil {
		return err
	}
	metadata, err := event.CanonicalMetadataJSON()
	if err != nil {
		return err
	}
	row := auditToRow(event, metadata)
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "append audit event")
	}
	return nil
}

func (store *Store) ListAudit(
	ctx context.Context,
	owner string,
	tenant string,
	limit int,
) ([]domain.SecurityAuditEvent, error) {
	if err := requireM1Scope(store, ctx, owner); err != nil {
		return nil, err
	}
	var rows []auditRow
	if err := store.db.WithContext(ctx).Where(
		"owner_user_id = ? AND tenant_id = ?",
		strings.TrimSpace(owner), strings.TrimSpace(tenant),
	).Order("created_at DESC").Limit(managementLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.SecurityAuditEvent, 0, len(rows))
	for _, row := range rows {
		value, err := auditFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func auditToRow(value domain.SecurityAuditEvent, metadata []byte) auditRow {
	return auditRow{
		ID:           value.ID.String(),
		OwnerUserID:  strings.TrimSpace(value.OwnerUserID),
		TenantID:     strings.TrimSpace(value.TenantID),
		ActorType:    string(value.ActorType),
		ActorID:      strings.TrimSpace(value.ActorID),
		Action:       value.Action,
		ObjectType:   value.ObjectType,
		ObjectID:     value.ObjectID,
		Result:       value.Result,
		ErrorCode:    value.ErrorCode,
		MetadataJSON: bytes.Clone(metadata),
		CreatedAt:    value.CreatedAt,
	}
}

func auditFromRow(row auditRow) (domain.SecurityAuditEvent, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.SecurityAuditEvent{}, err
	}
	metadata := make(map[string]string)
	if err := json.Unmarshal(row.MetadataJSON, &metadata); err != nil {
		return domain.SecurityAuditEvent{}, fmt.Errorf("decode audit metadata: %w", err)
	}
	value := domain.SecurityAuditEvent{
		ID:          id,
		OwnerUserID: row.OwnerUserID,
		TenantID:    row.TenantID,
		ActorType:   domain.AuditActorType(row.ActorType),
		ActorID:     row.ActorID,
		Action:      row.Action,
		ObjectType:  row.ObjectType,
		ObjectID:    row.ObjectID,
		Result:      row.Result,
		ErrorCode:   row.ErrorCode,
		Metadata:    metadata,
		CreatedAt:   row.CreatedAt,
	}
	if err := value.Validate(); err != nil {
		return domain.SecurityAuditEvent{}, fmt.Errorf("decode stored audit event: %w", err)
	}
	return value, nil
}
