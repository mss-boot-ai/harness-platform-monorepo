package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestPostgresSchemaMigrationContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HARNESS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("HARNESS_TEST_POSTGRES_DSN is not configured")
	}
	admin, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL integration database: %v", err)
	}
	closeGORMDatabase(t, admin)
	if err := admin.Exec("CREATE EXTENSION IF NOT EXISTS timescaledb").Error; err != nil {
		t.Fatalf("create TimescaleDB extension: %v", err)
	}
	var extensionVersion string
	if err := admin.Raw("SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'").Scan(&extensionVersion).Error; err != nil || extensionVersion == "" {
		t.Fatalf("verify TimescaleDB extension: version=%q err=%v", extensionVersion, err)
	}

	randomSuffix := make([]byte, 8)
	if _, err := rand.Read(randomSuffix); err != nil {
		t.Fatalf("generate test schema suffix: %v", err)
	}
	schema := "harness_test_" + hex.EncodeToString(randomSuffix)
	quotedSchema := `"` + schema + `"`
	if err := admin.Exec("CREATE SCHEMA " + quotedSchema).Error; err != nil {
		t.Fatalf("create PostgreSQL test schema: %v", err)
	}
	t.Cleanup(func() {
		if err := admin.Exec("DROP SCHEMA " + quotedSchema + " CASCADE").Error; err != nil {
			t.Errorf("drop PostgreSQL test schema: %v", err)
		}
	})

	scopedDSN, err := postgresDSNWithSearchPath(dsn, schema)
	if err != nil {
		t.Fatalf("scope PostgreSQL DSN: %v", err)
	}
	db, err := gorm.Open(postgres.Open(scopedDSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open scoped PostgreSQL integration database: %v", err)
	}
	closeGORMDatabase(t, db)
	var currentSchema string
	if err := db.Raw("SELECT CURRENT_SCHEMA()").Scan(&currentSchema).Error; err != nil || currentSchema != schema {
		t.Fatalf("PostgreSQL integration search_path schema=%q, want %q, err=%v", currentSchema, schema, err)
	}
	if err := CreateAllSchema(db); err != nil {
		t.Fatalf("CreateAllSchema on TimescaleDB: %v", err)
	}
	if err := VerifyAllSchema(db); err != nil {
		t.Fatalf("VerifyAllSchema on TimescaleDB: %v", err)
	}
	exercisePostgresEnrollmentRoundTrip(t, db)
	if err := db.Exec(`DROP INDEX "ux_harness_endpoint_owner_sign"`).Error; err != nil {
		t.Fatalf("drop PostgreSQL security index for partial-index negative test: %v", err)
	}
	if err := db.Exec(`CREATE UNIQUE INDEX "ux_harness_endpoint_owner_sign" ON "harness_endpoints"("owner_user_id", "signing_jkt") WHERE "owner_user_id" <> ''`).Error; err != nil {
		t.Fatalf("create partial PostgreSQL security index: %v", err)
	}
	if err := VerifyAllSchema(db); err == nil {
		t.Fatal("VerifyAllSchema accepted a partial PostgreSQL security index")
	}
	if err := CreateAllSchema(db); err != nil {
		t.Fatalf("repair partial PostgreSQL security index: %v", err)
	}
	if err := VerifyAllSchema(db); err != nil {
		t.Fatalf("VerifyAllSchema after partial-index repair: %v", err)
	}
	if err := db.Exec(`DROP INDEX "ux_harness_endpoint_owner_sign"`).Error; err != nil {
		t.Fatalf("drop PostgreSQL security index for deferrable negative test: %v", err)
	}
	if err := db.Exec(`ALTER TABLE "harness_endpoints" ADD CONSTRAINT "ux_harness_endpoint_owner_sign" UNIQUE ("owner_user_id", "signing_jkt") DEFERRABLE INITIALLY IMMEDIATE`).Error; err != nil {
		t.Fatalf("create deferrable PostgreSQL unique constraint: %v", err)
	}
	if err := VerifyAllSchema(db); err == nil {
		t.Fatal("VerifyAllSchema accepted a deferrable PostgreSQL unique constraint")
	}
	if err := db.Exec(`ALTER TABLE "harness_endpoints" DROP CONSTRAINT "ux_harness_endpoint_owner_sign"`).Error; err != nil {
		t.Fatalf("drop deferrable PostgreSQL unique constraint: %v", err)
	}
	if err := CreateAllSchema(db); err != nil {
		t.Fatalf("restore PostgreSQL security index after deferrable negative test: %v", err)
	}
	exercisePostgresAuthorizedFrames(t, db)
	exercisePostgresRefreshRevocationLocks(t, db)
}

func TestPostgresEnrollmentRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("HARNESS_TEST_POSTGRES_DSN"))
	if dsn == "" {
		t.Skip("HARNESS_TEST_POSTGRES_DSN is not configured")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open PostgreSQL enrollment database: %v", err)
	}
	closeGORMDatabase(t, db)
	exercisePostgresEnrollmentRoundTrip(t, db)
}

func exercisePostgresEnrollmentRoundTrip(t *testing.T, db *gorm.DB) {
	t.Helper()
	persistence, err := New(db)
	if err != nil {
		t.Fatalf("New PostgreSQL Store for enrollment: %v", err)
	}
	now := time.Unix(1_800_200_000, 123_456_000).UTC()
	endpoint := endpoint(0x31, 0x32, 0x33, domain.EndpointTypeABA, "", now)
	enrollmentID, err := domain.NewID(rand.Reader)
	if err != nil {
		t.Fatalf("generate PostgreSQL enrollment ID: %v", err)
	}
	deviceRaw := make([]byte, 32)
	userRaw := make([]byte, 32)
	if _, err := rand.Read(deviceRaw); err != nil {
		t.Fatalf("generate PostgreSQL enrollment device code: %v", err)
	}
	if _, err := rand.Read(userRaw); err != nil {
		t.Fatalf("generate PostgreSQL enrollment user code: %v", err)
	}
	deviceHash := sha256.Sum256(deviceRaw)
	userHash := sha256.Sum256(userRaw)
	enrollment := domain.Enrollment{
		ID: enrollmentID, EndpointType: domain.EndpointTypeABA, EndpointName: "PostgreSQL ABA",
		DeviceCodeHash: deviceHash, UserCodeHash: userHash,
		SigningPublicJWK: endpoint.SigningPublicJWK, KEMPublicJWK: endpoint.KEMPublicJWK,
		SigningJKT: endpoint.SigningJKT, KEMJKT: endpoint.KEMJKT,
		Status: domain.EnrollmentStatusPending, ExpiresAt: now.Add(10 * time.Minute),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := persistence.CreateEnrollment(t.Context(), enrollment); err != nil {
		t.Fatalf("CreateEnrollment on PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Exec("DELETE FROM harness_enrollments WHERE id = ?", enrollment.ID.String()).Error; err != nil {
			t.Errorf("delete PostgreSQL enrollment fixture: %v", err)
		}
	})
	stored, err := persistence.GetEnrollmentByDeviceCode(t.Context(), enrollment.ID, deviceHash, now.Add(time.Second))
	if err != nil {
		t.Fatalf("GetEnrollmentByDeviceCode on PostgreSQL: %v", err)
	}
	if stored.ID != enrollment.ID || stored.Status != domain.EnrollmentStatusPending || !stored.EndpointID.IsZero() ||
		!stored.ExpiresAt.Equal(enrollment.ExpiresAt) {
		t.Fatalf("PostgreSQL enrollment round trip = %#v", stored)
	}
}

func exercisePostgresAuthorizedFrames(t *testing.T, db *gorm.DB) {
	t.Helper()
	persistence, err := New(db)
	if err != nil {
		t.Fatalf("New PostgreSQL Store: %v", err)
	}
	now := time.Unix(1_800_000_000, 0).UTC()
	aba := endpoint(0xa0, 0xa1, 0x10, domain.EndpointTypeABA, "postgres-owner", now)
	hc := endpoint(0xb0, 0xb1, 0x20, domain.EndpointTypeHCWeb, "postgres-owner", now)
	aba.TenantID = "postgres-tenant"
	hc.TenantID = "postgres-tenant"
	for _, endpoint := range []domain.Endpoint{aba, hc} {
		if err := persistence.CreateEndpoint(t.Context(), endpoint); err != nil {
			t.Fatalf("CreateEndpoint on PostgreSQL: %v", err)
		}
	}
	abaCredential := credential(0xc0, aba, now)
	hcCredential := credential(0xc1, hc, now)
	for _, value := range []domain.EndpointCredential{abaCredential, hcCredential} {
		row, err := credentialToRow(value)
		if err != nil {
			t.Fatalf("credentialToRow on PostgreSQL: %v", err)
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatalf("create credential on PostgreSQL: %v", err)
		}
	}

	makeSession := func(seed byte) domain.Session {
		session := domain.Session{
			ID: tid(seed), OwnerUserID: "postgres-owner", TenantID: "postgres-tenant",
			ABAEndpointID: aba.ID, HCEndpointID: hc.ID,
			RuntimeProfileID: "test-agent", WorkspaceID: "fixture", RequestedCapabilities: []string{"prompt"},
			Status: domain.SessionStatusCreating, CreatedAt: now, UpdatedAt: now,
		}
		if err := persistence.CreateSession(t.Context(), session); err != nil {
			t.Fatalf("CreateSession on PostgreSQL: %v", err)
		}
		session, err = persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
			return value.WaitForKey(now.Add(time.Millisecond))
		})
		if err != nil {
			t.Fatalf("WaitForKey on PostgreSQL: %v", err)
		}
		session, err = persistence.UpdateSession(t.Context(), session.ID, func(value *domain.Session) error {
			return value.Activate(1, now.Add(2*time.Millisecond))
		})
		if err != nil {
			t.Fatalf("Activate on PostgreSQL: %v", err)
		}
		return session
	}
	makeFrame := func(session domain.Session, seed byte, direction domain.Direction, ciphertext []byte) (domain.EncryptedFrame, domain.ID) {
		channelID, err := authorizedFrameChannelID(session.ID, aba.ID, hc.ID)
		if err != nil {
			t.Fatalf("authorizedFrameChannelID on PostgreSQL: %v", err)
		}
		frame := testFrame(now.Add(3 * time.Millisecond))
		frame.MessageID = tid(seed)
		frame.SessionID = session.ID
		frame.ChannelID = channelID
		frame.KeyID = tid(seed + 1)
		frame.Ciphertext = bytes.Clone(ciphertext)
		frame.ContentHash = sha256.Sum256(ciphertext)
		frame.Direction = direction
		credentialID := hcCredential.ID
		frame.SenderEndpointID = hc.ID
		frame.ReceiverEndpointID = aba.ID
		if direction == domain.DirectionABAToHC {
			credentialID = abaCredential.ID
			frame.SenderEndpointID = aba.ID
			frame.ReceiverEndpointID = hc.ID
		}
		return frame, credentialID
	}

	largeSession := makeSession(0x30)
	largeCiphertext := bytes.Repeat([]byte{0x5a}, 1<<20)
	largeFrame, largeCredentialID := makeFrame(largeSession, 0x40, domain.DirectionHCToABA, largeCiphertext)
	if duplicate, err := persistence.PutAuthorizedEndpointFrame(
		t.Context(), "postgres-owner", "postgres-tenant", largeCredentialID, largeFrame, largeFrame.ReceivedAt,
	); err != nil || duplicate {
		t.Fatalf("store 1 MiB PostgreSQL frame duplicate=%v err=%v", duplicate, err)
	}
	stored, err := persistence.GetFrame(t.Context(), largeFrame.MessageID)
	if err != nil || !bytes.Equal(stored.Ciphertext, largeCiphertext) {
		t.Fatalf("round-trip 1 MiB PostgreSQL frame length=%d err=%v", len(stored.Ciphertext), err)
	}

	type frameOperation struct {
		credentialID domain.ID
		frame        domain.EncryptedFrame
	}
	operations := make([]frameOperation, 0, 8)
	for index := 0; index < 8; index++ {
		direction := domain.DirectionHCToABA
		if index%2 == 1 {
			direction = domain.DirectionABAToHC
		}
		session := makeSession(byte(0x50 + index))
		frame, credentialID := makeFrame(session, byte(0x70+index*2), direction, []byte{byte(index + 1)})
		operations = append(operations, frameOperation{credentialID: credentialID, frame: frame})
	}
	start := make(chan struct{})
	errorsChannel := make(chan error, len(operations))
	var group sync.WaitGroup
	for _, operation := range operations {
		operation := operation
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_, err := persistence.PutAuthorizedEndpointFrame(
				context.Background(), "postgres-owner", "postgres-tenant",
				operation.credentialID, operation.frame, operation.frame.ReceivedAt,
			)
			errorsChannel <- err
		}()
	}
	close(start)
	group.Wait()
	close(errorsChannel)
	for err := range errorsChannel {
		if err != nil {
			t.Fatalf("opposite-direction PostgreSQL frame concurrency: %v", err)
		}
	}
}

func exercisePostgresRefreshRevocationLocks(t *testing.T, db *gorm.DB) {
	t.Helper()
	persistence, err := New(db)
	if err != nil {
		t.Fatalf("New PostgreSQL Store: %v", err)
	}
	now := time.Unix(1_800_100_000, 0).UTC()
	for index := 0; index < 8; index++ {
		endpoint := endpoint(
			byte(0xd0+index), byte(0xe0+index), byte(0x30+index),
			domain.EndpointTypeHCWeb, "refresh-owner", now,
		)
		endpoint.TenantID = "refresh-tenant"
		if err := persistence.CreateEndpoint(t.Context(), endpoint); err != nil {
			t.Fatalf("CreateEndpoint for PostgreSQL refresh race: %v", err)
		}
		access := credential(byte(0x90+index), endpoint, now)
		accessRow, err := credentialToRow(access)
		if err != nil {
			t.Fatalf("credentialToRow for PostgreSQL refresh race: %v", err)
		}
		refresh := testRefreshCredential(byte(0xa8+index), endpoint, now)
		refreshRow := refreshCredentialToRow(refresh)
		if err := db.Create(&accessRow).Error; err != nil {
			t.Fatalf("create PostgreSQL access credential: %v", err)
		}
		if err := db.Create(&refreshRow).Error; err != nil {
			t.Fatalf("create PostgreSQL refresh credential: %v", err)
		}

		rotationTime := now.Add(time.Duration(index+1) * time.Minute)
		nextAccess := credential(byte(0x80+index), endpoint, rotationTime)
		nextAccess.TokenHash = sha256.Sum256([]byte(fmt.Sprintf("postgres-next-access-%d", index)))
		nextRefresh := testRefreshCredential(byte(0xb8+index), endpoint, rotationTime)
		nextRefresh.TokenHash = sha256.Sum256([]byte(fmt.Sprintf("postgres-next-refresh-%d", index)))
		nextRefresh.RotatedFrom = refresh.ID
		audit := domain.SecurityAuditEvent{
			ID: tid(byte(0x60 + index)), OwnerUserID: "refresh-owner", TenantID: "refresh-tenant",
			ActorType: domain.AuditActorEndpoint, ActorID: endpoint.ID.String(),
			Action: "endpoint.token.refresh", ObjectType: "endpoint", ObjectID: endpoint.ID.String(),
			Result: "success", Metadata: map[string]string{}, CreatedAt: rotationTime,
		}

		start := make(chan struct{})
		refreshResult := make(chan error, 1)
		revokeResult := make(chan error, 1)
		go func() {
			<-start
			refreshResult <- persistence.RotateRefreshCredential(
				context.Background(), refresh.TokenHash, endpoint.SigningJKT,
				nextAccess, nextRefresh, audit, rotationTime,
			)
		}()
		go func() {
			<-start
			_, err := persistence.RevokeEndpointForOwner(
				context.Background(), endpoint.ID, "refresh-owner", "refresh-tenant", rotationTime.Add(time.Second),
			)
			revokeResult <- err
		}()
		close(start)
		refreshErr := <-refreshResult
		revokeErr := <-revokeResult
		if revokeErr != nil {
			t.Fatalf("PostgreSQL revocation lost refresh race: %v", revokeErr)
		}
		if refreshErr != nil && !domain.HasCode(refreshErr, domain.CodeRevoked) {
			t.Fatalf("PostgreSQL refresh race returned unstable error: %v", refreshErr)
		}
	}
}

func postgresDSNWithSearchPath(dsn, schema string) (string, error) {
	if !safeSQLIdentifier(schema) {
		return "", fmt.Errorf("invalid PostgreSQL schema %q", schema)
	}
	parsed, err := url.Parse(dsn)
	if err == nil && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		query := parsed.Query()
		query.Set("search_path", schema)
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}
	if strings.ContainsAny(dsn, "\r\n") {
		return "", fmt.Errorf("PostgreSQL DSN contains a line break")
	}
	return strings.TrimSpace(dsn) + " search_path=" + schema, nil
}

func closeGORMDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("access SQL database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close SQL database: %v", err)
		}
	})
}
