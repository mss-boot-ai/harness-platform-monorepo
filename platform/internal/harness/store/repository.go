package store

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-io/mss-boot-admin/mss-boot/pkg/migration"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const SchemaMigrationID migration.MigrationID = "20260904010000"

type Store struct {
	db *gorm.DB
}

func New(db *gorm.DB) (*Store, error) {
	if db == nil {
		return nil, errors.New("harness store database is required")
	}
	return &Store{db: db}, nil
}

type endpointRow struct {
	ID                 string     `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID        string     `gorm:"column:owner_user_id;size:128;not null;uniqueIndex:ux_harness_endpoint_owner_sign,priority:1;uniqueIndex:ux_harness_endpoint_owner_kem,priority:1;index:idx_harness_endpoint_owner_status,priority:1"`
	TenantID           string     `gorm:"column:tenant_id;size:128;not null;default:''"`
	Type               string     `gorm:"column:type;size:24;not null"`
	Name               string     `gorm:"column:name;size:120;not null"`
	SigningPublicJWK   []byte     `gorm:"column:signing_public_jwk;type:blob;not null"`
	KEMPublicJWK       []byte     `gorm:"column:kem_public_jwk;type:blob;not null"`
	SigningJKT         string     `gorm:"column:signing_jkt;size:64;not null;uniqueIndex:ux_harness_endpoint_owner_sign,priority:2"`
	KEMJKT             string     `gorm:"column:kem_jkt;size:64;not null;uniqueIndex:ux_harness_endpoint_owner_kem,priority:2"`
	Status             string     `gorm:"column:status;size:24;not null;index:idx_harness_endpoint_owner_status,priority:2"`
	CredentialFamilyID string     `gorm:"column:credential_family_id;type:char(32);not null"`
	SoftwareVersion    string     `gorm:"column:software_version;size:64;not null;default:''"`
	PlatformName       string     `gorm:"column:platform_name;size:64;not null;default:''"`
	LastSeenAt         *time.Time `gorm:"column:last_seen_at"`
	RevokedAt          *time.Time `gorm:"column:revoked_at"`
	RowVersion         uint64     `gorm:"column:row_version;not null;default:0"`
	CreatedAt          time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt          time.Time  `gorm:"column:updated_at;not null"`
}

func (endpointRow) TableName() string { return "harness_endpoints" }

type enrollmentRow struct {
	ID               string     `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID      string     `gorm:"column:owner_user_id;size:128;not null;default:'';index:idx_harness_enrollment_owner_status,priority:1"`
	TenantID         string     `gorm:"column:tenant_id;size:128;not null;default:''"`
	EndpointType     string     `gorm:"column:endpoint_type;size:24;not null"`
	EndpointName     string     `gorm:"column:endpoint_name;size:120;not null"`
	DeviceCodeHash   string     `gorm:"column:device_code_hash;type:char(64);not null;uniqueIndex"`
	UserCodeHash     string     `gorm:"column:user_code_hash;type:char(64);not null;uniqueIndex"`
	SigningPublicJWK []byte     `gorm:"column:signing_public_jwk;type:blob;not null"`
	KEMPublicJWK     []byte     `gorm:"column:kem_public_jwk;type:blob;not null"`
	SigningJKT       string     `gorm:"column:signing_jkt;size:64;not null"`
	KEMJKT           string     `gorm:"column:kem_jkt;size:64;not null"`
	Status           string     `gorm:"column:status;size:24;not null;index:idx_harness_enrollment_owner_status,priority:2;index:idx_harness_enrollment_expiry,priority:1"`
	ExpiresAt        time.Time  `gorm:"column:expires_at;not null;index:idx_harness_enrollment_expiry,priority:2"`
	ApprovedBy       string     `gorm:"column:approved_by;size:128;not null;default:''"`
	ApprovedAt       *time.Time `gorm:"column:approved_at"`
	ConsumedAt       *time.Time `gorm:"column:consumed_at"`
	EndpointID       string     `gorm:"column:endpoint_id;type:char(32);not null;default:''"`
	RowVersion       uint64     `gorm:"column:row_version;not null;default:0"`
	CreatedAt        time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt        time.Time  `gorm:"column:updated_at;not null"`
}

func (enrollmentRow) TableName() string { return "harness_enrollments" }

type credentialRow struct {
	ID          string     `gorm:"column:id;type:char(32);primaryKey"`
	EndpointID  string     `gorm:"column:endpoint_id;type:char(32);not null;index:idx_harness_credential_endpoint_status,priority:1"`
	FamilyID    string     `gorm:"column:family_id;type:char(32);not null"`
	TokenHash   string     `gorm:"column:token_hash;type:char(64);not null;uniqueIndex"`
	SigningJKT  string     `gorm:"column:signing_jkt;size:64;not null"`
	ScopesJSON  string     `gorm:"column:scopes_json;type:text;not null"`
	Status      string     `gorm:"column:status;size:24;not null;index:idx_harness_credential_endpoint_status,priority:2"`
	ExpiresAt   time.Time  `gorm:"column:expires_at;not null"`
	RotatedFrom string     `gorm:"column:rotated_from_id;type:char(32);not null;default:''"`
	CreatedAt   time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time  `gorm:"column:updated_at;not null"`
	RevokedAt   *time.Time `gorm:"column:revoked_at"`
}

func (credentialRow) TableName() string { return "harness_endpoint_credentials" }

type sessionRow struct {
	ID                   string     `gorm:"column:id;type:char(32);primaryKey"`
	OwnerUserID          string     `gorm:"column:owner_user_id;size:128;not null"`
	TenantID             string     `gorm:"column:tenant_id;size:128;not null;default:''"`
	ABAEndpointID        string     `gorm:"column:aba_endpoint_id;type:char(32);not null;index:idx_harness_session_aba_status,priority:1"`
	HCEndpointID         string     `gorm:"column:hc_endpoint_id;type:char(32);not null;index:idx_harness_session_hc_status,priority:1"`
	RuntimeProfileID     string     `gorm:"column:runtime_profile_id;size:64;not null"`
	WorkspaceID          string     `gorm:"column:workspace_id;size:64;not null"`
	CapabilitiesJSON     string     `gorm:"column:capabilities_json;type:text;not null"`
	Status               string     `gorm:"column:status;size:32;not null;index:idx_harness_session_aba_status,priority:2;index:idx_harness_session_hc_status,priority:2"`
	CurrentKeyGeneration uint64     `gorm:"column:current_key_generation;not null;default:0"`
	CreatedAt            time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;not null"`
	LastActivityAt       *time.Time `gorm:"column:last_activity_at"`
	ClosedAt             *time.Time `gorm:"column:closed_at"`
	RowVersion           uint64     `gorm:"column:row_version;not null;default:0"`
}

func (sessionRow) TableName() string { return "harness_sessions" }

type ticketRow struct {
	ID           string     `gorm:"column:id;type:char(32);primaryKey"`
	TokenHash    string     `gorm:"column:token_hash;type:char(64);not null;uniqueIndex"`
	EndpointID   string     `gorm:"column:endpoint_id;type:char(32);not null;index:idx_harness_ticket_endpoint_status,priority:1"`
	CredentialID string     `gorm:"column:credential_id;type:char(32);not null"`
	Purpose      string     `gorm:"column:purpose;size:64;not null"`
	Origin       string     `gorm:"column:origin;size:512;not null"`
	Protocol     string     `gorm:"column:protocol;size:64;not null"`
	Status       string     `gorm:"column:status;size:24;not null;index:idx_harness_ticket_endpoint_status,priority:2;index:idx_harness_ticket_expiry,priority:1"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null;index:idx_harness_ticket_expiry,priority:2"`
	ConsumedAt   *time.Time `gorm:"column:consumed_at"`
	CreatedAt    time.Time  `gorm:"column:created_at;not null"`
	RowVersion   uint64     `gorm:"column:row_version;not null;default:0"`
}

func (ticketRow) TableName() string { return "harness_ws_tickets" }

type frameRow struct {
	MessageID          string     `gorm:"column:message_id;type:char(32);primaryKey"`
	SessionID          string     `gorm:"column:session_id;type:char(32);not null;uniqueIndex:ux_harness_frame_sequence,priority:1"`
	ChannelID          string     `gorm:"column:channel_id;type:char(32);not null"`
	KeyGeneration      uint64     `gorm:"column:key_generation;not null;uniqueIndex:ux_harness_frame_sequence,priority:2"`
	SenderEndpointID   string     `gorm:"column:sender_endpoint_id;type:char(32);not null;uniqueIndex:ux_harness_frame_sequence,priority:3"`
	ReceiverEndpointID string     `gorm:"column:receiver_endpoint_id;type:char(32);not null;index:idx_harness_frame_receiver_status_sequence,priority:1"`
	Direction          uint8      `gorm:"column:direction;not null"`
	Sequence           uint64     `gorm:"column:sequence;not null;uniqueIndex:ux_harness_frame_sequence,priority:4;index:idx_harness_frame_receiver_status_sequence,priority:3"`
	KeyID              string     `gorm:"column:key_id;type:char(32);not null"`
	CreatedAtMS        int64      `gorm:"column:created_at_ms;not null"`
	AAD                []byte     `gorm:"column:aad;type:blob;not null"`
	Ciphertext         []byte     `gorm:"column:ciphertext;type:blob;not null"`
	Signature          []byte     `gorm:"column:signature;type:blob;not null"`
	ContentHash        string     `gorm:"column:content_hash;type:char(64);not null"`
	Status             string     `gorm:"column:status;size:24;not null;index:idx_harness_frame_receiver_status_sequence,priority:2"`
	ReceivedAt         time.Time  `gorm:"column:received_at;not null"`
	RoutedAt           *time.Time `gorm:"column:routed_at"`
	AcknowledgedAt     *time.Time `gorm:"column:acknowledged_at"`
	ExpiresAt          time.Time  `gorm:"column:expires_at;not null"`
}

func (frameRow) TableName() string { return "harness_frames" }

type ackRow struct {
	SessionID                 string    `gorm:"column:session_id;type:char(32);primaryKey"`
	KeyGeneration             uint64    `gorm:"column:key_generation;primaryKey"`
	Direction                 uint8     `gorm:"column:direction;primaryKey"`
	SenderEndpointID          string    `gorm:"column:sender_endpoint_id;type:char(32);primaryKey"`
	ReceiverEndpointID        string    `gorm:"column:receiver_endpoint_id;type:char(32);primaryKey"`
	HighestContiguousSequence uint64    `gorm:"column:highest_contiguous_sequence;not null"`
	UpdatedAt                 time.Time `gorm:"column:updated_at;not null"`
	RowVersion                uint64    `gorm:"column:row_version;not null;default:0"`
}

func (ackRow) TableName() string { return "harness_ack_cursors" }

var schemaModels = []any{
	new(endpointRow),
	new(enrollmentRow),
	new(credentialRow),
	new(sessionRow),
	new(ticketRow),
	new(frameRow),
	new(ackRow),
}

var requiredIndexes = []struct {
	model any
	name  string
}{
	{new(endpointRow), "ux_harness_endpoint_owner_sign"},
	{new(endpointRow), "ux_harness_endpoint_owner_kem"},
	{new(endpointRow), "idx_harness_endpoint_owner_status"},
	{new(enrollmentRow), "idx_harness_enrollment_owner_status"},
	{new(enrollmentRow), "idx_harness_enrollment_expiry"},
	{new(credentialRow), "idx_harness_credential_endpoint_status"},
	{new(sessionRow), "idx_harness_session_aba_status"},
	{new(sessionRow), "idx_harness_session_hc_status"},
	{new(ticketRow), "idx_harness_ticket_endpoint_status"},
	{new(ticketRow), "idx_harness_ticket_expiry"},
	{new(frameRow), "ux_harness_frame_sequence"},
	{new(frameRow), "idx_harness_frame_receiver_status_sequence"},
}

func RegisterMigrations(runner *migration.Migration) error {
	if runner == nil {
		return errors.New("harness migration runner is required")
	}
	return runner.Register(SchemaMigrationID, func(db *gorm.DB, version string) error {
		if version != SchemaMigrationID.String() {
			return errors.New("harness schema migration version mismatch")
		}
		if err := CreateSchema(db); err != nil {
			return err
		}
		return runner.CreateVersion(db, version)
	})
}

// CreateSchema is an explicit, additive migration. Production code registers
// it with the mss-boot migration runner; tests invoke it against an empty DB.
func CreateSchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness schema database is required")
	}
	for _, model := range schemaModels {
		if !db.Migrator().HasTable(model) {
			if err := db.Migrator().CreateTable(model); err != nil {
				return fmt.Errorf("create Harness table %T: %w", model, err)
			}
		}
	}
	for _, index := range requiredIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			if err := db.Migrator().CreateIndex(index.model, index.name); err != nil {
				return fmt.Errorf("create Harness index %s: %w", index.name, err)
			}
		}
	}
	return VerifySchema(db)
}

func VerifySchema(db *gorm.DB) error {
	if db == nil {
		return errors.New("harness schema database is required")
	}
	for _, model := range schemaModels {
		if !db.Migrator().HasTable(model) {
			return fmt.Errorf("Harness table %T is unavailable", model)
		}
	}
	for _, index := range requiredIndexes {
		if !db.Migrator().HasIndex(index.model, index.name) {
			return fmt.Errorf("Harness index %s is unavailable", index.name)
		}
	}
	return nil
}

func (store *Store) CreateEnrollment(ctx context.Context, enrollment domain.Enrollment) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if err := enrollment.Validate(); err != nil {
		return err
	}
	if enrollment.Status != domain.EnrollmentStatusPending {
		return domain.NewProblem(domain.CodeInvalidState, "new enrollment must be pending", nil)
	}
	row := enrollmentToRow(enrollment)
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create enrollment")
	}
	return nil
}

func (store *Store) ApproveEnrollment(
	ctx context.Context,
	id domain.ID,
	owner string,
	now time.Time,
) (domain.Enrollment, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Enrollment{}, err
	}
	if id.IsZero() || strings.TrimSpace(owner) == "" {
		return domain.Enrollment{}, domain.NewProblem(domain.CodeInvalidArgument, "enrollment ID and owner are required", nil)
	}
	var updated domain.Enrollment
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row enrollmentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id.String()).Error; err != nil {
			return notFoundOr("lock enrollment", "enrollment was not found", err)
		}
		enrollment, err := enrollmentFromRow(row)
		if err != nil {
			return err
		}
		if enrollment.OwnerUserID != "" && enrollment.OwnerUserID != owner {
			return domain.NewProblem(domain.CodeNotFound, "enrollment was not found", nil)
		}
		previous := enrollment.RowVersion
		if err := enrollment.Approve(owner, now); err != nil {
			return err
		}
		enrollment.OwnerUserID = owner
		result := tx.Model(&enrollmentRow{}).
			Where("id = ? AND row_version = ?", row.ID, previous).
			Updates(map[string]any{
				"owner_user_id": enrollment.OwnerUserID,
				"status":        string(enrollment.Status),
				"approved_by":   enrollment.ApprovedBy,
				"approved_at":   enrollment.ApprovedAt,
				"updated_at":    enrollment.UpdatedAt,
				"row_version":   enrollment.RowVersion,
			})
		if result.Error != nil {
			return classifyPersistence(result.Error, "approve enrollment")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "enrollment changed concurrently", nil)
		}
		updated = enrollment
		return nil
	})
	return updated, err
}

func (store *Store) ConsumeEnrollment(
	ctx context.Context,
	enrollmentID domain.ID,
	endpoint domain.Endpoint,
	credential domain.EndpointCredential,
	now time.Time,
) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if enrollmentID.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "enrollment ID is required", nil)
	}
	if err := validatePersistedEndpoint(endpoint); err != nil {
		return err
	}
	if err := validateCredential(credential, endpoint, now); err != nil {
		return err
	}
	return store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row enrollmentRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", enrollmentID.String()).Error; err != nil {
			return notFoundOr("lock enrollment", "enrollment was not found", err)
		}
		enrollment, err := enrollmentFromRow(row)
		if err != nil {
			return err
		}
		if enrollment.Status == domain.EnrollmentStatusConsumed {
			if enrollment.EndpointID == endpoint.ID {
				return nil
			}
			return domain.NewProblem(domain.CodeConflict, "enrollment was consumed for another endpoint", nil)
		}
		if enrollment.OwnerUserID == "" ||
			enrollment.OwnerUserID != endpoint.OwnerUserID ||
			enrollment.EndpointType != endpoint.Type ||
			enrollment.SigningJKT != endpoint.SigningJKT ||
			enrollment.KEMJKT != endpoint.KEMJKT ||
			!bytes.Equal(enrollment.SigningPublicJWK, endpoint.SigningPublicJWK) ||
			!bytes.Equal(enrollment.KEMPublicJWK, endpoint.KEMPublicJWK) {
			return domain.NewProblem(domain.CodeSecurityViolation, "endpoint does not match approved enrollment", nil)
		}
		previous := enrollment.RowVersion
		if err := enrollment.Consume(endpoint.ID, now); err != nil {
			return err
		}
		endpointRecord := endpointToRow(endpoint)
		credentialRecord, err := credentialToRow(credential)
		if err != nil {
			return err
		}
		if err := tx.Create(&endpointRecord).Error; err != nil {
			return classifyPersistence(err, "create endpoint")
		}
		if err := tx.Create(&credentialRecord).Error; err != nil {
			return classifyPersistence(err, "create endpoint credential")
		}
		result := tx.Model(&enrollmentRow{}).
			Where("id = ? AND row_version = ? AND status = ?", row.ID, previous, string(domain.EnrollmentStatusApproved)).
			Updates(map[string]any{
				"status":      string(enrollment.Status),
				"endpoint_id": endpoint.ID.String(),
				"consumed_at": enrollment.ConsumedAt,
				"updated_at":  enrollment.UpdatedAt,
				"row_version": enrollment.RowVersion,
			})
		if result.Error != nil {
			return classifyPersistence(result.Error, "consume enrollment")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "enrollment changed concurrently", nil)
		}
		return nil
	})
}

func (store *Store) CreateEndpoint(ctx context.Context, endpoint domain.Endpoint) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if err := validatePersistedEndpoint(endpoint); err != nil {
		return err
	}
	row := endpointToRow(endpoint)
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create endpoint")
	}
	return nil
}

func (store *Store) CreateSession(ctx context.Context, session domain.Session) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	request := domain.SessionRequest{
		ABAEndpointID:         session.ABAEndpointID,
		HCEndpointID:          session.HCEndpointID,
		RuntimeProfileID:      session.RuntimeProfileID,
		WorkspaceID:           session.WorkspaceID,
		RequestedCapabilities: append([]string(nil), session.RequestedCapabilities...),
	}
	if session.ID.IsZero() || strings.TrimSpace(session.OwnerUserID) == "" {
		return domain.NewProblem(domain.CodeInvalidArgument, "session ID and owner are required", nil)
	}
	if err := request.Validate(); err != nil {
		return err
	}
	if session.Status != domain.SessionStatusCreating {
		return domain.NewProblem(domain.CodeInvalidState, "new session must be creating", nil)
	}
	session.RequestedCapabilities = request.RequestedCapabilities
	row, err := sessionToRow(session)
	if err != nil {
		return err
	}
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create session")
	}
	return nil
}

func (store *Store) GetSession(ctx context.Context, id domain.ID) (domain.Session, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Session{}, err
	}
	var row sessionRow
	if err := store.db.WithContext(ctx).First(&row, "id = ?", id.String()).Error; err != nil {
		return domain.Session{}, notFoundOr("read session", "session was not found", err)
	}
	return sessionFromRow(row)
}

func (store *Store) UpdateSession(
	ctx context.Context,
	id domain.ID,
	mutate func(*domain.Session) error,
) (domain.Session, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Session{}, err
	}
	if id.IsZero() || mutate == nil {
		return domain.Session{}, domain.NewProblem(domain.CodeInvalidArgument, "session update is invalid", nil)
	}
	var updated domain.Session
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row sessionRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id.String()).Error; err != nil {
			return notFoundOr("lock session", "session was not found", err)
		}
		session, err := sessionFromRow(row)
		if err != nil {
			return err
		}
		previous := session.RowVersion
		if err := mutate(&session); err != nil {
			return err
		}
		next, err := sessionToRow(session)
		if err != nil {
			return err
		}
		result := tx.Model(&sessionRow{}).
			Where("id = ? AND row_version = ?", row.ID, previous).
			Updates(map[string]any{
				"status":                 next.Status,
				"current_key_generation": next.CurrentKeyGeneration,
				"last_activity_at":       next.LastActivityAt,
				"closed_at":              next.ClosedAt,
				"updated_at":             next.UpdatedAt,
				"row_version":            next.RowVersion,
			})
		if result.Error != nil {
			return classifyPersistence(result.Error, "update session")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "session changed concurrently", nil)
		}
		updated = session
		return nil
	})
	return updated, err
}

func (store *Store) RevokeEndpoint(
	ctx context.Context,
	id domain.ID,
	owner string,
	now time.Time,
) (domain.Endpoint, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.Endpoint{}, err
	}
	var revoked domain.Endpoint
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row endpointRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&row, "id = ?", id.String()).Error; err != nil {
			return notFoundOr("lock endpoint", "endpoint was not found", err)
		}
		endpoint, err := endpointFromRow(row)
		if err != nil {
			return err
		}
		if endpoint.OwnerUserID != owner {
			return domain.NewProblem(domain.CodeNotFound, "endpoint was not found", nil)
		}
		previous := endpoint.RowVersion
		if err := endpoint.Revoke(now); err != nil {
			return err
		}
		result := tx.Model(&endpointRow{}).
			Where("id = ? AND row_version = ?", row.ID, previous).
			Updates(map[string]any{
				"status":      string(endpoint.Status),
				"revoked_at":  endpoint.RevokedAt,
				"updated_at":  endpoint.UpdatedAt,
				"row_version": endpoint.RowVersion,
			})
		if result.Error != nil {
			return classifyPersistence(result.Error, "revoke endpoint")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "endpoint changed concurrently", nil)
		}
		if err := tx.Model(&credentialRow{}).
			Where("endpoint_id = ? AND status = ?", row.ID, string(domain.CredentialStatusActive)).
			Updates(map[string]any{
				"status":     string(domain.CredentialStatusRevoked),
				"revoked_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("revoke credentials: %w", err)
		}
		if err := tx.Model(&refreshCredentialRow{}).
			Where("endpoint_id = ? AND status = ?", row.ID, string(domain.CredentialStatusActive)).
			Updates(map[string]any{
				"status":     string(domain.CredentialStatusRevoked),
				"revoked_at": now,
				"updated_at": now,
			}).Error; err != nil {
			return fmt.Errorf("revoke refresh credentials: %w", err)
		}
		if err := tx.Model(&ticketRow{}).
			Where("endpoint_id = ? AND status = ?", row.ID, string(domain.TicketStatusIssued)).
			Updates(map[string]any{
				"status":      string(domain.TicketStatusRevoked),
				"row_version": gorm.Expr("row_version + 1"),
			}).Error; err != nil {
			return fmt.Errorf("revoke tickets: %w", err)
		}
		sessionStatus := domain.SessionStatusRekeyRequired
		if endpoint.Type == domain.EndpointTypeABA {
			sessionStatus = domain.SessionStatusABARevoked
		}
		column := "hc_endpoint_id"
		if endpoint.Type == domain.EndpointTypeABA {
			column = "aba_endpoint_id"
		}
		updates := map[string]any{
			"status":      string(sessionStatus),
			"updated_at":  now,
			"row_version": gorm.Expr("row_version + 1"),
		}
		if endpoint.Type == domain.EndpointTypeABA {
			updates["closed_at"] = now
		}
		if err := tx.Model(&sessionRow{}).
			Where(column+" = ? AND status NOT IN ?", row.ID, []string{
				string(domain.SessionStatusClosed),
				string(domain.SessionStatusABARevoked),
			}).
			Updates(updates).Error; err != nil {
			return fmt.Errorf("update sessions after endpoint revocation: %w", err)
		}
		revoked = endpoint
		return nil
	})
	return revoked, err
}

func (store *Store) CreateTicket(ctx context.Context, ticket domain.WSTicket, maxTTL time.Duration) error {
	if err := requireStore(store, ctx); err != nil {
		return err
	}
	if ticket.ID.IsZero() || ticket.EndpointID.IsZero() || ticket.CredentialID.IsZero() ||
		zeroHash(ticket.TokenHash) || ticket.Status != domain.TicketStatusIssued ||
		strings.TrimSpace(ticket.Purpose) == "" || strings.TrimSpace(ticket.Origin) == "" ||
		strings.TrimSpace(ticket.Protocol) == "" || ticket.CreatedAt.IsZero() ||
		!ticket.ExpiresAt.After(ticket.CreatedAt) || maxTTL <= 0 ||
		ticket.ExpiresAt.Sub(ticket.CreatedAt) > maxTTL {
		return domain.NewProblem(domain.CodeInvalidArgument, "WebSocket ticket is invalid", nil)
	}
	row := ticketToRow(ticket)
	if err := store.db.WithContext(ctx).Create(&row).Error; err != nil {
		return classifyPersistence(err, "create WebSocket ticket")
	}
	return nil
}

func (store *Store) ConsumeTicket(
	ctx context.Context,
	tokenHash [32]byte,
	endpointID domain.ID,
	credentialID domain.ID,
	purpose string,
	origin string,
	protocol string,
	now time.Time,
) (domain.WSTicket, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.WSTicket{}, err
	}
	if zeroHash(tokenHash) || endpointID.IsZero() || credentialID.IsZero() {
		return domain.WSTicket{}, domain.NewProblem(domain.CodeInvalidArgument, "ticket token and binding are required", nil)
	}
	var consumed domain.WSTicket
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ticketRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&row, "token_hash = ?", hashString(tokenHash)).Error; err != nil {
			return notFoundOr("lock WebSocket ticket", "WebSocket ticket was not found", err)
		}
		ticket, err := ticketFromRow(row)
		if err != nil {
			return err
		}
		if ticket.EndpointID != endpointID || ticket.CredentialID != credentialID ||
			ticket.Purpose != purpose || ticket.Origin != origin || ticket.Protocol != protocol {
			return domain.NewProblem(domain.CodeSecurityViolation, "WebSocket ticket binding does not match", nil)
		}
		switch ticket.Status {
		case domain.TicketStatusConsumed:
			return domain.NewProblem(domain.CodeConflict, "WebSocket ticket was already consumed", nil)
		case domain.TicketStatusRevoked:
			return domain.NewProblem(domain.CodeRevoked, "WebSocket ticket is revoked", nil)
		case domain.TicketStatusExpired:
			return domain.NewProblem(domain.CodeExpired, "WebSocket ticket is expired", nil)
		case domain.TicketStatusIssued:
		default:
			return domain.NewProblem(domain.CodeInvalidState, "WebSocket ticket state is invalid", nil)
		}
		if !now.Before(ticket.ExpiresAt) {
			_ = tx.Model(&ticketRow{}).Where("id = ?", row.ID).Updates(map[string]any{
				"status":      string(domain.TicketStatusExpired),
				"row_version": row.RowVersion + 1,
			}).Error
			return domain.NewProblem(domain.CodeExpired, "WebSocket ticket is expired", nil)
		}
		result := tx.Model(&ticketRow{}).
			Where("id = ? AND row_version = ? AND status = ?", row.ID, row.RowVersion, string(domain.TicketStatusIssued)).
			Updates(map[string]any{
				"status":      string(domain.TicketStatusConsumed),
				"consumed_at": now,
				"row_version": row.RowVersion + 1,
			})
		if result.Error != nil {
			return classifyPersistence(result.Error, "consume WebSocket ticket")
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "WebSocket ticket was consumed concurrently", nil)
		}
		ticket.Status = domain.TicketStatusConsumed
		ticket.ConsumedAt = &now
		ticket.RowVersion++
		consumed = ticket
		return nil
	})
	return consumed, err
}

type PutFrameOutcome string

const (
	PutFrameStored    PutFrameOutcome = "STORED"
	PutFrameDuplicate PutFrameOutcome = "DUPLICATE"
)

func (store *Store) PutEndpointFrame(
	ctx context.Context,
	frame domain.EncryptedFrame,
) (bool, error) {
	outcome, err := store.PutFrame(ctx, frame, 1<<20)
	return outcome == PutFrameDuplicate, err
}

func (store *Store) PutFrame(
	ctx context.Context,
	frame domain.EncryptedFrame,
	maxCiphertextBytes int,
) (PutFrameOutcome, error) {
	if err := requireStore(store, ctx); err != nil {
		return "", err
	}
	if err := frame.Validate(maxCiphertextBytes); err != nil {
		return "", err
	}
	if frame.Status != domain.FrameStatusStored {
		return "", domain.NewProblem(domain.CodeInvalidState, "new frame must be stored", nil)
	}
	row := frameToRow(frame)
	var outcome PutFrameOutcome
	var conflict error
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
		if result.Error != nil {
			return classifyPersistence(result.Error, "store encrypted frame")
		}
		if result.RowsAffected == 1 {
			outcome = PutFrameStored
			return nil
		}
		var existing frameRow
		if err := tx.Where("message_id = ?", row.MessageID).Or(
			"session_id = ? AND key_generation = ? AND sender_endpoint_id = ? AND sequence = ?",
			row.SessionID,
			row.KeyGeneration,
			row.SenderEndpointID,
			row.Sequence,
		).First(&existing).Error; err != nil {
			return notFoundOr("read conflicting frame", "frame conflict could not be resolved", err)
		}
		if sameFrame(existing, row) {
			outcome = PutFrameDuplicate
			return nil
		}
		if err := tx.Model(&frameRow{}).
			Where("message_id = ?", existing.MessageID).
			Update("status", string(domain.FrameStatusConflict)).Error; err != nil {
			return fmt.Errorf("mark frame conflict: %w", err)
		}
		conflict = domain.NewProblem(domain.CodeConflict, "frame idempotency key was reused with different content", nil)
		return nil
	})
	if err != nil {
		return "", err
	}
	if conflict != nil {
		return "", conflict
	}
	return outcome, nil
}

func (store *Store) AdvanceAck(ctx context.Context, cursor domain.AckCursor) (domain.AckCursor, error) {
	if err := requireStore(store, ctx); err != nil {
		return domain.AckCursor{}, err
	}
	if cursor.SessionID.IsZero() || cursor.SenderEndpointID.IsZero() ||
		cursor.ReceiverEndpointID.IsZero() || cursor.KeyGeneration == 0 ||
		!cursor.Direction.Valid() || cursor.UpdatedAt.IsZero() {
		return domain.AckCursor{}, domain.NewProblem(domain.CodeInvalidArgument, "ACK cursor is invalid", nil)
	}
	var advanced domain.AckCursor
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row ackRow
		query := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where(
			"session_id = ? AND key_generation = ? AND direction = ? AND sender_endpoint_id = ? AND receiver_endpoint_id = ?",
			cursor.SessionID.String(),
			cursor.KeyGeneration,
			uint8(cursor.Direction),
			cursor.SenderEndpointID.String(),
			cursor.ReceiverEndpointID.String(),
		)
		err := query.First(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = ackToRow(cursor)
			row.RowVersion = 1
			result := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if result.Error != nil {
				return classifyPersistence(result.Error, "create ACK cursor")
			}
			if result.RowsAffected == 1 {
				cursor.RowVersion = 1
				advanced = cursor
				return nil
			}
			if err := query.First(&row).Error; err != nil {
				return fmt.Errorf("read concurrently created ACK cursor: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("lock ACK cursor: %w", err)
		}
		current, err := ackFromRow(row)
		if err != nil {
			return err
		}
		if cursor.HighestContiguousSequence <= current.HighestContiguousSequence {
			advanced = current
			return nil
		}
		result := tx.Model(&ackRow{}).
			Where(
				"session_id = ? AND key_generation = ? AND direction = ? AND sender_endpoint_id = ? AND receiver_endpoint_id = ? AND row_version = ?",
				row.SessionID,
				row.KeyGeneration,
				row.Direction,
				row.SenderEndpointID,
				row.ReceiverEndpointID,
				row.RowVersion,
			).
			Updates(map[string]any{
				"highest_contiguous_sequence": cursor.HighestContiguousSequence,
				"updated_at":                  cursor.UpdatedAt,
				"row_version":                 row.RowVersion + 1,
			})
		if result.Error != nil {
			return fmt.Errorf("advance ACK cursor: %w", result.Error)
		}
		if result.RowsAffected != 1 {
			return domain.NewProblem(domain.CodeConflict, "ACK cursor changed concurrently", nil)
		}
		cursor.RowVersion = row.RowVersion + 1
		advanced = cursor
		return nil
	})
	return advanced, err
}

func endpointToRow(value domain.Endpoint) endpointRow {
	return endpointRow{
		ID:                 value.ID.String(),
		OwnerUserID:        value.OwnerUserID,
		TenantID:           value.TenantID,
		Type:               string(value.Type),
		Name:               value.Name,
		SigningPublicJWK:   bytes.Clone(value.SigningPublicJWK),
		KEMPublicJWK:       bytes.Clone(value.KEMPublicJWK),
		SigningJKT:         value.SigningJKT,
		KEMJKT:             value.KEMJKT,
		Status:             string(value.Status),
		CredentialFamilyID: value.CredentialFamilyID.String(),
		SoftwareVersion:    value.SoftwareVersion,
		PlatformName:       value.PlatformName,
		LastSeenAt:         cloneTime(value.LastSeenAt),
		RevokedAt:          cloneTime(value.RevokedAt),
		RowVersion:         value.RowVersion,
		CreatedAt:          value.CreatedAt,
		UpdatedAt:          value.UpdatedAt,
	}
}

func endpointFromRow(row endpointRow) (domain.Endpoint, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.Endpoint{}, err
	}
	familyID, err := parseID(row.CredentialFamilyID)
	if err != nil {
		return domain.Endpoint{}, err
	}
	return domain.Endpoint{
		ID:                 id,
		OwnerUserID:        row.OwnerUserID,
		TenantID:           row.TenantID,
		Type:               domain.EndpointType(row.Type),
		Name:               row.Name,
		SigningPublicJWK:   bytes.Clone(row.SigningPublicJWK),
		KEMPublicJWK:       bytes.Clone(row.KEMPublicJWK),
		SigningJKT:         row.SigningJKT,
		KEMJKT:             row.KEMJKT,
		Status:             domain.EndpointStatus(row.Status),
		CredentialFamilyID: familyID,
		SoftwareVersion:    row.SoftwareVersion,
		PlatformName:       row.PlatformName,
		LastSeenAt:         cloneTime(row.LastSeenAt),
		RevokedAt:          cloneTime(row.RevokedAt),
		RowVersion:         row.RowVersion,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}, nil
}

func enrollmentToRow(value domain.Enrollment) enrollmentRow {
	endpointID := ""
	if !value.EndpointID.IsZero() {
		endpointID = value.EndpointID.String()
	}
	return enrollmentRow{
		ID:               value.ID.String(),
		OwnerUserID:      value.OwnerUserID,
		TenantID:         value.TenantID,
		EndpointType:     string(value.EndpointType),
		EndpointName:     value.EndpointName,
		DeviceCodeHash:   hashString(value.DeviceCodeHash),
		UserCodeHash:     hashString(value.UserCodeHash),
		SigningPublicJWK: bytes.Clone(value.SigningPublicJWK),
		KEMPublicJWK:     bytes.Clone(value.KEMPublicJWK),
		SigningJKT:       value.SigningJKT,
		KEMJKT:           value.KEMJKT,
		Status:           string(value.Status),
		ExpiresAt:        value.ExpiresAt,
		ApprovedBy:       value.ApprovedBy,
		ApprovedAt:       cloneTime(value.ApprovedAt),
		ConsumedAt:       cloneTime(value.ConsumedAt),
		EndpointID:       endpointID,
		RowVersion:       value.RowVersion,
		CreatedAt:        value.CreatedAt,
		UpdatedAt:        value.UpdatedAt,
	}
}

func enrollmentFromRow(row enrollmentRow) (domain.Enrollment, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.Enrollment{}, err
	}
	deviceHash, err := parseHash(row.DeviceCodeHash)
	if err != nil {
		return domain.Enrollment{}, err
	}
	userHash, err := parseHash(row.UserCodeHash)
	if err != nil {
		return domain.Enrollment{}, err
	}
	var endpointID domain.ID
	if row.EndpointID != "" {
		endpointID, err = parseID(row.EndpointID)
		if err != nil {
			return domain.Enrollment{}, err
		}
	}
	return domain.Enrollment{
		ID:               id,
		OwnerUserID:      row.OwnerUserID,
		TenantID:         row.TenantID,
		EndpointType:     domain.EndpointType(row.EndpointType),
		EndpointName:     row.EndpointName,
		DeviceCodeHash:   deviceHash,
		UserCodeHash:     userHash,
		SigningPublicJWK: bytes.Clone(row.SigningPublicJWK),
		KEMPublicJWK:     bytes.Clone(row.KEMPublicJWK),
		SigningJKT:       row.SigningJKT,
		KEMJKT:           row.KEMJKT,
		Status:           domain.EnrollmentStatus(row.Status),
		ExpiresAt:        row.ExpiresAt,
		ApprovedBy:       row.ApprovedBy,
		ApprovedAt:       cloneTime(row.ApprovedAt),
		ConsumedAt:       cloneTime(row.ConsumedAt),
		EndpointID:       endpointID,
		RowVersion:       row.RowVersion,
		CreatedAt:        row.CreatedAt,
		UpdatedAt:        row.UpdatedAt,
	}, nil
}

func credentialToRow(value domain.EndpointCredential) (credentialRow, error) {
	scopes, err := json.Marshal(value.Scopes)
	if err != nil {
		return credentialRow{}, fmt.Errorf("encode credential scopes: %w", err)
	}
	return credentialRow{
		ID:          value.ID.String(),
		EndpointID:  value.EndpointID.String(),
		FamilyID:    value.FamilyID.String(),
		TokenHash:   hashString(value.TokenHash),
		SigningJKT:  value.SigningJKT,
		ScopesJSON:  string(scopes),
		Status:      string(value.Status),
		ExpiresAt:   value.ExpiresAt,
		RotatedFrom: optionalID(value.RotatedFrom),
		CreatedAt:   value.CreatedAt,
		UpdatedAt:   value.UpdatedAt,
		RevokedAt:   cloneTime(value.RevokedAt),
	}, nil
}

func sessionToRow(value domain.Session) (sessionRow, error) {
	capabilities, err := json.Marshal(value.RequestedCapabilities)
	if err != nil {
		return sessionRow{}, fmt.Errorf("encode session capabilities: %w", err)
	}
	return sessionRow{
		ID:                   value.ID.String(),
		OwnerUserID:          value.OwnerUserID,
		TenantID:             value.TenantID,
		ABAEndpointID:        value.ABAEndpointID.String(),
		HCEndpointID:         value.HCEndpointID.String(),
		RuntimeProfileID:     value.RuntimeProfileID,
		WorkspaceID:          value.WorkspaceID,
		CapabilitiesJSON:     string(capabilities),
		Status:               string(value.Status),
		CurrentKeyGeneration: value.CurrentKeyGeneration,
		CreatedAt:            value.CreatedAt,
		UpdatedAt:            value.UpdatedAt,
		LastActivityAt:       cloneTime(value.LastActivityAt),
		ClosedAt:             cloneTime(value.ClosedAt),
		RowVersion:           value.RowVersion,
	}, nil
}

func sessionFromRow(row sessionRow) (domain.Session, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.Session{}, err
	}
	abaID, err := parseID(row.ABAEndpointID)
	if err != nil {
		return domain.Session{}, err
	}
	hcID, err := parseID(row.HCEndpointID)
	if err != nil {
		return domain.Session{}, err
	}
	var capabilities []string
	if err := json.Unmarshal([]byte(row.CapabilitiesJSON), &capabilities); err != nil {
		return domain.Session{}, fmt.Errorf("decode session capabilities: %w", err)
	}
	return domain.Session{
		ID:                    id,
		OwnerUserID:           row.OwnerUserID,
		TenantID:              row.TenantID,
		ABAEndpointID:         abaID,
		HCEndpointID:          hcID,
		RuntimeProfileID:      row.RuntimeProfileID,
		WorkspaceID:           row.WorkspaceID,
		RequestedCapabilities: capabilities,
		Status:                domain.SessionStatus(row.Status),
		CurrentKeyGeneration:  row.CurrentKeyGeneration,
		CreatedAt:             row.CreatedAt,
		UpdatedAt:             row.UpdatedAt,
		LastActivityAt:        cloneTime(row.LastActivityAt),
		ClosedAt:              cloneTime(row.ClosedAt),
		RowVersion:            row.RowVersion,
	}, nil
}

func ticketToRow(value domain.WSTicket) ticketRow {
	return ticketRow{
		ID:           value.ID.String(),
		TokenHash:    hashString(value.TokenHash),
		EndpointID:   value.EndpointID.String(),
		CredentialID: value.CredentialID.String(),
		Purpose:      value.Purpose,
		Origin:       value.Origin,
		Protocol:     value.Protocol,
		Status:       string(value.Status),
		ExpiresAt:    value.ExpiresAt,
		ConsumedAt:   cloneTime(value.ConsumedAt),
		CreatedAt:    value.CreatedAt,
		RowVersion:   value.RowVersion,
	}
}

func ticketFromRow(row ticketRow) (domain.WSTicket, error) {
	id, err := parseID(row.ID)
	if err != nil {
		return domain.WSTicket{}, err
	}
	endpointID, err := parseID(row.EndpointID)
	if err != nil {
		return domain.WSTicket{}, err
	}
	credentialID, err := parseID(row.CredentialID)
	if err != nil {
		return domain.WSTicket{}, err
	}
	tokenHash, err := parseHash(row.TokenHash)
	if err != nil {
		return domain.WSTicket{}, err
	}
	return domain.WSTicket{
		ID:           id,
		TokenHash:    tokenHash,
		EndpointID:   endpointID,
		CredentialID: credentialID,
		Purpose:      row.Purpose,
		Origin:       row.Origin,
		Protocol:     row.Protocol,
		Status:       domain.TicketStatus(row.Status),
		ExpiresAt:    row.ExpiresAt,
		ConsumedAt:   cloneTime(row.ConsumedAt),
		CreatedAt:    row.CreatedAt,
		RowVersion:   row.RowVersion,
	}, nil
}

func frameToRow(value domain.EncryptedFrame) frameRow {
	return frameRow{
		MessageID:          value.MessageID.String(),
		SessionID:          value.SessionID.String(),
		ChannelID:          value.ChannelID.String(),
		KeyGeneration:      value.KeyGeneration,
		SenderEndpointID:   value.SenderEndpointID.String(),
		ReceiverEndpointID: value.ReceiverEndpointID.String(),
		Direction:          uint8(value.Direction),
		Sequence:           value.Sequence,
		KeyID:              value.KeyID.String(),
		CreatedAtMS:        value.CreatedAtMS,
		AAD:                bytes.Clone(value.AAD),
		Ciphertext:         bytes.Clone(value.Ciphertext),
		Signature:          bytes.Clone(value.Signature),
		ContentHash:        hashString(value.ContentHash),
		Status:             string(value.Status),
		ReceivedAt:         value.ReceivedAt,
		RoutedAt:           cloneTime(value.RoutedAt),
		AcknowledgedAt:     cloneTime(value.AcknowledgedAt),
		ExpiresAt:          value.ExpiresAt,
	}
}

func sameFrame(left, right frameRow) bool {
	return left.MessageID == right.MessageID &&
		left.SessionID == right.SessionID &&
		left.ChannelID == right.ChannelID &&
		left.KeyGeneration == right.KeyGeneration &&
		left.SenderEndpointID == right.SenderEndpointID &&
		left.ReceiverEndpointID == right.ReceiverEndpointID &&
		left.Direction == right.Direction &&
		left.Sequence == right.Sequence &&
		left.KeyID == right.KeyID &&
		left.CreatedAtMS == right.CreatedAtMS &&
		left.ContentHash == right.ContentHash &&
		bytes.Equal(left.AAD, right.AAD) &&
		bytes.Equal(left.Ciphertext, right.Ciphertext) &&
		bytes.Equal(left.Signature, right.Signature)
}

func ackToRow(value domain.AckCursor) ackRow {
	return ackRow{
		SessionID:                 value.SessionID.String(),
		KeyGeneration:             value.KeyGeneration,
		Direction:                 uint8(value.Direction),
		SenderEndpointID:          value.SenderEndpointID.String(),
		ReceiverEndpointID:        value.ReceiverEndpointID.String(),
		HighestContiguousSequence: value.HighestContiguousSequence,
		UpdatedAt:                 value.UpdatedAt,
		RowVersion:                value.RowVersion,
	}
}

func ackFromRow(row ackRow) (domain.AckCursor, error) {
	sessionID, err := parseID(row.SessionID)
	if err != nil {
		return domain.AckCursor{}, err
	}
	senderID, err := parseID(row.SenderEndpointID)
	if err != nil {
		return domain.AckCursor{}, err
	}
	receiverID, err := parseID(row.ReceiverEndpointID)
	if err != nil {
		return domain.AckCursor{}, err
	}
	return domain.AckCursor{
		SessionID:                 sessionID,
		KeyGeneration:             row.KeyGeneration,
		Direction:                 domain.Direction(row.Direction),
		SenderEndpointID:          senderID,
		ReceiverEndpointID:        receiverID,
		HighestContiguousSequence: row.HighestContiguousSequence,
		UpdatedAt:                 row.UpdatedAt,
		RowVersion:                row.RowVersion,
	}, nil
}

func validatePersistedEndpoint(endpoint domain.Endpoint) error {
	if err := endpoint.Validate(); err != nil {
		return err
	}
	if endpoint.CredentialFamilyID.IsZero() ||
		endpoint.Status != domain.EndpointStatusActive ||
		endpoint.CreatedAt.IsZero() ||
		endpoint.UpdatedAt.IsZero() {
		return domain.NewProblem(domain.CodeInvalidArgument, "persisted endpoint is incomplete", nil)
	}
	return nil
}

func validateCredential(
	credential domain.EndpointCredential,
	endpoint domain.Endpoint,
	now time.Time,
) error {
	if credential.ID.IsZero() ||
		credential.EndpointID != endpoint.ID ||
		credential.FamilyID != endpoint.CredentialFamilyID ||
		credential.SigningJKT != endpoint.SigningJKT ||
		zeroHash(credential.TokenHash) ||
		credential.Status != domain.CredentialStatusActive ||
		!credential.ExpiresAt.After(now) ||
		credential.CreatedAt.IsZero() ||
		credential.UpdatedAt.IsZero() ||
		len(credential.Scopes) == 0 {
		return domain.NewProblem(domain.CodeSecurityViolation, "endpoint credential binding is invalid", nil)
	}
	return nil
}

func requireStore(store *Store, ctx context.Context) error {
	if store == nil || store.db == nil {
		return errors.New("harness store is unavailable")
	}
	if ctx == nil {
		return domain.NewProblem(domain.CodeInvalidArgument, "context is required", nil)
	}
	return nil
}

func notFoundOr(operation, safeMessage string, err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return domain.NewProblem(domain.CodeNotFound, safeMessage, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func classifyPersistence(err error, operation string) error {
	if err == nil {
		return nil
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "duplicated key") {
		return domain.NewProblem(domain.CodeConflict, operation+" conflicts with existing state", err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func parseID(value string) (domain.ID, error) {
	id, err := domain.ParseID(value)
	if err != nil {
		return domain.ID{}, fmt.Errorf("decode stored ID: %w", err)
	}
	return id, nil
}

func hashString(value [32]byte) string { return hex.EncodeToString(value[:]) }

func parseHash(value string) ([32]byte, error) {
	var output [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(output) {
		return output, errors.New("decode stored SHA-256 hash")
	}
	copy(output[:], decoded)
	return output, nil
}

func zeroHash(value [32]byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
