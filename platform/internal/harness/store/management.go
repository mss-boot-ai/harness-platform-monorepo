package store

import (
	"context"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxManagementLimit = 200

type Overview struct {
	PendingEnrollments int64
	ActiveEndpoints    int64
	ActiveSessions     int64
	Unacknowledged     int64
	ConflictFrames     int64
}

type DeliverySnapshot struct {
	Session domain.Session
	Frames  []domain.EncryptedFrame
	ACKs    []domain.AckCursor
}

func (store *Store) Overview(ctx context.Context, owner, tenant string) (Overview, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return Overview{}, err
	}
	var out Overview
	counts := []struct {
		model any
		where string
		args  []any
		out   *int64
	}{
		{new(enrollmentRow), "owner_user_id = ? AND status IN ?", []any{owner, []string{string(domain.EnrollmentStatusPending), string(domain.EnrollmentStatusApproved)}}, &out.PendingEnrollments},
		{new(endpointRow), "owner_user_id = ? AND status = ?", []any{owner, string(domain.EndpointStatusActive)}, &out.ActiveEndpoints},
		{new(sessionRow), "owner_user_id = ? AND status IN ?", []any{owner, []string{string(domain.SessionStatusCreating), string(domain.SessionStatusWaitingKey), string(domain.SessionStatusActive), string(domain.SessionStatusRekeyRequired), string(domain.SessionStatusDraining), string(domain.SessionStatusUncertain)}}, &out.ActiveSessions},
	}
	for _, count := range counts {
		query := scoped(store.db.WithContext(ctx).Model(count.model).Where(count.where, count.args...), tenant)
		if err := query.Count(count.out).Error; err != nil {
			return Overview{}, err
		}
	}
	frameQuery := func(statuses []string, output *int64) error {
		query := store.db.WithContext(ctx).Model(new(frameRow)).
			Joins("JOIN harness_sessions ON harness_sessions.id = harness_frames.session_id").
			Where("harness_sessions.owner_user_id = ?", owner).
			Where("harness_frames.status IN ?", statuses)
		if strings.TrimSpace(tenant) != "" {
			query = query.Where("harness_sessions.tenant_id = ?", strings.TrimSpace(tenant))
		}
		return query.Count(output).Error
	}
	if err := frameQuery([]string{string(domain.FrameStatusStored), string(domain.FrameStatusRouted)}, &out.Unacknowledged); err != nil {
		return Overview{}, err
	}
	if err := frameQuery([]string{string(domain.FrameStatusConflict)}, &out.ConflictFrames); err != nil {
		return Overview{}, err
	}
	return out, nil
}

func (store *Store) ListEnrollments(ctx context.Context, owner, tenant string, limit int) ([]domain.Enrollment, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return nil, err
	}
	var rows []enrollmentRow
	if err := scoped(store.db.WithContext(ctx).Where("owner_user_id = ?", owner), tenant).
		Order("created_at DESC").Limit(managementLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.Enrollment, 0, len(rows))
	for _, row := range rows {
		value, err := enrollmentFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) ApproveEnrollmentWithCode(ctx context.Context, id domain.ID, code [32]byte, owner, tenant string, now time.Time) (domain.Enrollment, error) {
	return store.decideEnrollment(ctx, id, code, owner, tenant, now, true)
}

func (store *Store) DenyEnrollmentWithCode(ctx context.Context, id domain.ID, code [32]byte, owner, tenant string, now time.Time) (domain.Enrollment, error) {
	return store.decideEnrollment(ctx, id, code, owner, tenant, now, false)
}

func (store *Store) decideEnrollment(ctx context.Context, id domain.ID, code [32]byte, owner, tenant string, now time.Time, approve bool) (domain.Enrollment, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return domain.Enrollment{}, err
	}
	if id.IsZero() || zeroHash(code) || now.IsZero() {
		return domain.Enrollment{}, domain.NewProblem(domain.CodeInvalidArgument, "enrollment decision is invalid", nil)
	}
	var updated domain.Enrollment
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row enrollmentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row,
			"id = ? AND user_code_hash = ?", id.String(), hashString(code)).Error; err != nil {
			return notFoundOr("lock enrollment", "enrollment was not found", err)
		}
		value, err := enrollmentFromRow(row)
		if err != nil {
			return err
		}
		if value.OwnerUserID != "" && value.OwnerUserID != owner {
			return domain.NewProblem(domain.CodeNotFound, "enrollment was not found", nil)
		}
		if value.TenantID != "" && tenant != "" && value.TenantID != tenant {
			return domain.NewProblem(domain.CodeNotFound, "enrollment was not found", nil)
		}
		previous := value.RowVersion
		if approve {
			err = value.Approve(owner, now)
		} else {
			err = value.Deny(owner, now)
		}
		if err != nil {
			return err
		}
		value.OwnerUserID = owner
		if value.TenantID == "" {
			value.TenantID = strings.TrimSpace(tenant)
		}
		result := tx.Model(new(enrollmentRow)).Where("id = ? AND row_version = ? AND status = ?", row.ID, previous, string(domain.EnrollmentStatusPending)).Updates(map[string]any{
			"owner_user_id": value.OwnerUserID, "tenant_id": value.TenantID,
			"status": string(value.Status), "approved_by": value.ApprovedBy,
			"approved_at": value.ApprovedAt, "updated_at": value.UpdatedAt,
			"row_version": value.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "decide enrollment")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "enrollment changed concurrently", nil)
		}
		updated = value
		return nil
	})
	return updated, err
}

func (store *Store) ListEndpoints(ctx context.Context, owner, tenant string, limit int) ([]domain.Endpoint, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return nil, err
	}
	var rows []endpointRow
	if err := scoped(store.db.WithContext(ctx).Where("owner_user_id = ?", owner), tenant).
		Order("created_at DESC").Limit(managementLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.Endpoint, 0, len(rows))
	for _, row := range rows {
		value, err := endpointFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) UpdateEndpointForOwner(ctx context.Context, id domain.ID, owner, tenant string, mutate func(*domain.Endpoint) error) (domain.Endpoint, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return domain.Endpoint{}, err
	}
	if id.IsZero() || mutate == nil {
		return domain.Endpoint{}, domain.NewProblem(domain.CodeInvalidArgument, "endpoint update is invalid", nil)
	}
	var updated domain.Endpoint
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row endpointRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id.String()).Error; err != nil {
			return notFoundOr("lock endpoint", "endpoint was not found", err)
		}
		value, err := endpointFromRow(row)
		if err != nil {
			return err
		}
		if value.OwnerUserID != owner || (tenant != "" && value.TenantID != "" && value.TenantID != tenant) {
			return domain.NewProblem(domain.CodeNotFound, "endpoint was not found", nil)
		}
		previous := value.RowVersion
		if err := mutate(&value); err != nil {
			return err
		}
		result := tx.Model(new(endpointRow)).Where("id = ? AND row_version = ?", row.ID, previous).Updates(map[string]any{
			"status": string(value.Status), "last_seen_at": value.LastSeenAt,
			"revoked_at": value.RevokedAt, "updated_at": value.UpdatedAt,
			"row_version": value.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "update endpoint")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "endpoint changed concurrently", nil)
		}
		updated = value
		return nil
	})
	return updated, err
}

func (store *Store) ListSessions(ctx context.Context, owner, tenant string, limit int) ([]domain.Session, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return nil, err
	}
	var rows []sessionRow
	if err := scoped(store.db.WithContext(ctx).Where("owner_user_id = ?", owner), tenant).
		Order("created_at DESC").Limit(managementLimit(limit)).Find(&rows).Error; err != nil {
		return nil, err
	}
	values := make([]domain.Session, 0, len(rows))
	for _, row := range rows {
		value, err := sessionFromRow(row)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func (store *Store) CloseSessionForOwner(ctx context.Context, id domain.ID, owner, tenant string, now time.Time) (domain.Session, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return domain.Session{}, err
	}
	return store.UpdateSession(ctx, id, func(value *domain.Session) error {
		if value.OwnerUserID != owner || (tenant != "" && value.TenantID != "" && value.TenantID != tenant) {
			return domain.NewProblem(domain.CodeNotFound, "session was not found", nil)
		}
		return value.Close(now)
	})
}

func (store *Store) Delivery(ctx context.Context, sessionID domain.ID, owner, tenant string, limit int) (DeliverySnapshot, error) {
	if err := requireOwnerStore(store, ctx, owner); err != nil {
		return DeliverySnapshot{}, err
	}
	if sessionID.IsZero() {
		return DeliverySnapshot{}, domain.NewProblem(domain.CodeInvalidArgument, "session ID is required", nil)
	}
	var row sessionRow
	if err := scoped(store.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", sessionID.String(), owner), tenant).First(&row).Error; err != nil {
		return DeliverySnapshot{}, notFoundOr("read delivery session", "session was not found", err)
	}
	session, err := sessionFromRow(row)
	if err != nil {
		return DeliverySnapshot{}, err
	}
	var frameRows []frameRow
	if err := store.db.WithContext(ctx).Where("session_id = ?", sessionID.String()).Order("received_at DESC").Limit(managementLimit(limit)).Find(&frameRows).Error; err != nil {
		return DeliverySnapshot{}, err
	}
	frames := make([]domain.EncryptedFrame, 0, len(frameRows))
	for _, row := range frameRows {
		value, err := frameFromRow(row)
		if err != nil {
			return DeliverySnapshot{}, err
		}
		frames = append(frames, value)
	}
	var ackRows []ackRow
	if err := store.db.WithContext(ctx).Where("session_id = ?", sessionID.String()).Order("updated_at DESC").Find(&ackRows).Error; err != nil {
		return DeliverySnapshot{}, err
	}
	acks := make([]domain.AckCursor, 0, len(ackRows))
	for _, row := range ackRows {
		value, err := ackFromRow(row)
		if err != nil {
			return DeliverySnapshot{}, err
		}
		acks = append(acks, value)
	}
	return DeliverySnapshot{Session: session, Frames: frames, ACKs: acks}, nil
}

func requireOwnerStore(store *Store, ctx context.Context, owner string) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if strings.TrimSpace(owner) == "" {
		return domain.NewProblem(domain.CodeInvalidArgument, "owner is required", nil)
	}
	return nil
}

func scoped(query *gorm.DB, tenant string) *gorm.DB {
	if tenant = strings.TrimSpace(tenant); tenant != "" {
		return query.Where("tenant_id = ?", tenant)
	}
	return query
}

func managementLimit(limit int) int {
	if limit <= 0 {
		return 50
	}
	if limit > maxManagementLimit {
		return maxManagementLimit
	}
	return limit
}
