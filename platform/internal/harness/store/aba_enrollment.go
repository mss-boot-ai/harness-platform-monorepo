package store

import (
	"context"
	"crypto/subtle"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (store *Store) GetEnrollmentByDeviceCode(
	ctx context.Context,
	id domain.ID,
	deviceCodeHash [32]byte,
	now time.Time,
) (domain.Enrollment, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Enrollment{}, err
	}
	if id.IsZero() || zeroHash(deviceCodeHash) || now.IsZero() {
		return domain.Enrollment{}, domain.NewProblem(domain.CodeInvalidArgument, "enrollment lookup is invalid", nil)
	}
	var row enrollmentRow
	if err := store.db.WithContext(ctx).Where("id = ?", id.String()).Take(&row).Error; err != nil {
		return domain.Enrollment{}, notFoundOr("read ABA enrollment", "enrollment is unavailable", err)
	}
	value, err := enrollmentFromRow(row)
	if err != nil {
		return domain.Enrollment{}, err
	}
	if subtle.ConstantTimeCompare(value.DeviceCodeHash[:], deviceCodeHash[:]) != 1 {
		return domain.Enrollment{}, domain.NewProblem(domain.CodeNotFound, "enrollment is unavailable", nil)
	}
	if value.Status != domain.EnrollmentStatusConsumed && !now.Before(value.ExpiresAt) {
		value.Status = domain.EnrollmentStatusExpired
	}
	return value, nil
}

func (store *Store) ConsumeABAEnrollment(
	ctx context.Context,
	enrollmentID domain.ID,
	deviceCodeHash [32]byte,
	endpoint domain.Endpoint,
	access domain.EndpointCredential,
	refresh domain.RefreshCredential,
	audit domain.SecurityAuditEvent,
	now time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if enrollmentID.IsZero() || zeroHash(deviceCodeHash) || endpoint.Type != domain.EndpointTypeABA ||
		strings.TrimSpace(endpoint.OwnerUserID) == "" || now.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "ABA enrollment consumption is invalid", nil)
	}
	if err := validatePersistedEndpoint(endpoint); err != nil {
		return err
	}
	if err := validateCredential(access, endpoint, now); err != nil {
		return err
	}
	if err := refresh.Validate(endpoint, now); err != nil {
		return err
	}
	if err := validateABAEnrollmentAudit(audit, endpoint); err != nil {
		return err
	}
	metadata, err := audit.CanonicalMetadataJSON()
	if err != nil {
		return err
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row enrollmentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ?", enrollmentID.String()).Take(&row).Error; err != nil {
			return notFoundOr("lock ABA enrollment", "enrollment is unavailable", err)
		}
		enrollment, err := enrollmentFromRow(row)
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(enrollment.DeviceCodeHash[:], deviceCodeHash[:]) != 1 {
			return domain.NewProblem(domain.CodeNotFound, "enrollment is unavailable", nil)
		}
		if enrollment.Status == domain.EnrollmentStatusConsumed {
			return domain.NewProblem(domain.CodeConflict, "enrollment was already consumed", nil)
		}
		if enrollment.Status != domain.EnrollmentStatusApproved || enrollment.OwnerUserID != endpoint.OwnerUserID ||
			enrollment.TenantID != endpoint.TenantID || enrollment.EndpointType != endpoint.Type ||
			enrollment.SigningJKT != endpoint.SigningJKT || enrollment.KEMJKT != endpoint.KEMJKT ||
			!now.Before(enrollment.ExpiresAt) {
			return domain.NewProblem(domain.CodeSecurityViolation, "approved enrollment binding is invalid", nil)
		}
		previous := enrollment.RowVersion
		if err := enrollment.Consume(endpoint.ID, now); err != nil {
			return err
		}
		endpointRow := endpointToRow(endpoint)
		accessRow, err := credentialToRow(access)
		if err != nil {
			return err
		}
		refreshRow := refreshCredentialToRow(refresh)
		auditRow := auditToRow(audit, metadata)
		if err := tx.Create(&endpointRow).Error; err != nil {
			return classifyPersistence(err, "create ABA endpoint")
		}
		if err := tx.Create(&accessRow).Error; err != nil {
			return classifyPersistence(err, "create ABA access credential")
		}
		if err := tx.Create(&refreshRow).Error; err != nil {
			return classifyPersistence(err, "create ABA refresh credential")
		}
		if err := tx.Create(&auditRow).Error; err != nil {
			return classifyPersistence(err, "append ABA enrollment audit")
		}
		result := tx.Model(new(enrollmentRow)).Where(
			"id = ? AND row_version = ? AND status = ?", row.ID, previous, string(domain.EnrollmentStatusApproved),
		).Updates(map[string]any{
			"status": string(enrollment.Status), "endpoint_id": endpoint.ID.String(),
			"consumed_at": now, "updated_at": now, "row_version": enrollment.RowVersion,
		})
		if result.Error != nil {
			return classifyPersistence(result.Error, "consume ABA enrollment")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "ABA enrollment changed concurrently", nil)
		}
		return nil
	})
}

func validateABAEnrollmentAudit(audit domain.SecurityAuditEvent, endpoint domain.Endpoint) error {
	if err := audit.Validate(); err != nil {
		return err
	}
	if audit.OwnerUserID != endpoint.OwnerUserID || audit.TenantID != endpoint.TenantID ||
		audit.ActorType != domain.AuditActorEndpoint || audit.ActorID != endpoint.ID.String() ||
		audit.Action != "aba.endpoint.enroll" || audit.ObjectType != "endpoint" ||
		audit.ObjectID != endpoint.ID.String() || audit.Result != "success" {
		return domain.NewProblem(domain.CodeSecurityViolation, "ABA enrollment audit binding is invalid", nil)
	}
	return nil
}
