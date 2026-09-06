package harness

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestManagementWriteRequiresIdempotencyKey(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	now := time.Now().UTC()
	endpoint := managementEndpoint(31, 32, 40, domain.EndpointTypeHCWeb, now)
	if err := persistence.CreateEndpoint(context.Background(), endpoint); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	response := requestJSONWithKey(
		router, http.MethodPost,
		"/api/harness/v1/endpoints/"+endpoint.ID.String()+"/suspend",
		"", "",
	)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "Idempotency-Key") {
		t.Fatalf("missing key status = %d body=%s", response.Code, response.Body.String())
	}
	values, err := persistence.ListEndpoints(context.Background(), "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].Status != domain.EndpointStatusActive {
		t.Fatalf("missing key mutated endpoint: %#v err=%v", values, err)
	}
	audit, err := persistence.ListAudit(context.Background(), "owner", "tenant", 10)
	if err != nil || len(audit) != 0 {
		t.Fatalf("missing key audit = %#v err=%v", audit, err)
	}
}

func TestManagementIdempotencyReplaysExactSuccessAndAuditsOnce(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	now := time.Now().UTC()
	endpoint := managementEndpoint(41, 42, 50, domain.EndpointTypeHCWeb, now)
	if err := persistence.CreateEndpoint(context.Background(), endpoint); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	const key = "endpoint-suspend-idempotency-0001"
	path := "/api/harness/v1/endpoints/" + endpoint.ID.String() + "/suspend"
	first := requestJSONWithKey(router, http.MethodPost, path, "", key)
	second := requestJSONWithKey(router, http.MethodPost, path, "", key)
	if first.Code != http.StatusOK || second.Code != http.StatusOK {
		t.Fatalf("first=%d %s second=%d %s", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("replayed body differs:\nfirst=%s\nsecond=%s", first.Body.String(), second.Body.String())
	}
	if second.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay header = %q", second.Header().Get("Idempotency-Replayed"))
	}
	values, err := persistence.ListEndpoints(context.Background(), "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].Status != domain.EndpointStatusSuspended || values[0].RowVersion != 1 {
		t.Fatalf("endpoint after replay = %#v err=%v", values, err)
	}
	audit, err := persistence.ListAudit(context.Background(), "owner", "tenant", 10)
	if err != nil || len(audit) != 1 {
		t.Fatalf("audit after replay = %#v err=%v", audit, err)
	}
	if audit[0].Action != "endpoint.suspend" || audit[0].Result != "success" || audit[0].Metadata["currentStatus"] != string(domain.EndpointStatusSuspended) {
		t.Fatalf("audit = %#v", audit[0])
	}
}

func TestManagementIdempotencyRejectsDifferentRequestAndReplaysFailure(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	now := time.Now().UTC()
	enrollment := domain.Enrollment{
		ID:               managementID(51),
		EndpointType:     domain.EndpointTypeABA,
		EndpointName:     "ABA",
		DeviceCodeHash:   sha256.Sum256([]byte("device")),
		UserCodeHash:     hashUserCode("RIGHT-CODE"),
		SigningPublicJWK: managementJWK(60),
		KEMPublicJWK:     managementJWK(62),
		SigningJKT:       managementJKT(60),
		KEMJKT:           managementJKT(61),
		Status:           domain.EnrollmentStatusPending,
		ExpiresAt:        now.Add(time.Minute),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := persistence.CreateEnrollment(context.Background(), enrollment); err != nil {
		t.Fatalf("CreateEnrollment: %v", err)
	}
	path := "/api/harness/v1/enrollments/" + enrollment.ID.String() + "/approve"
	const failureKey = "enrollment-failure-idempotency-0001"
	firstFailure := requestJSONWithKey(router, http.MethodPost, path, `{"userCode":"WRONG"}`, failureKey)
	secondFailure := requestJSONWithKey(router, http.MethodPost, path, `{"userCode":"WRONG"}`, failureKey)
	if firstFailure.Code != http.StatusNotFound || secondFailure.Code != http.StatusNotFound {
		t.Fatalf("failure first=%d %s second=%d %s", firstFailure.Code, firstFailure.Body.String(), secondFailure.Code, secondFailure.Body.String())
	}
	if firstFailure.Body.String() != secondFailure.Body.String() || secondFailure.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("failure replay body/header first=%s second=%s header=%q", firstFailure.Body.String(), secondFailure.Body.String(), secondFailure.Header().Get("Idempotency-Replayed"))
	}
	audit, err := persistence.ListAudit(context.Background(), "owner", "tenant", 10)
	if err != nil || len(audit) != 1 || audit[0].Result != "failure" || audit[0].ErrorCode != string(domain.CodeNotFound) {
		t.Fatalf("failure audit = %#v err=%v", audit, err)
	}

	const conflictKey = "enrollment-conflict-idempotency-0001"
	approved := requestJSONWithKey(router, http.MethodPost, path, `{"userCode":"RIGHT CODE"}`, conflictKey)
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", approved.Code, approved.Body.String())
	}
	conflict := requestJSONWithKey(router, http.MethodPost, path, `{"userCode":"DIFFERENT"}`, conflictKey)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), string(domain.CodeConflict)) {
		t.Fatalf("conflict status = %d body=%s", conflict.Code, conflict.Body.String())
	}
	audit, err = persistence.ListAudit(context.Background(), "owner", "tenant", 10)
	if err != nil || len(audit) != 2 {
		t.Fatalf("audit after request conflict = %#v err=%v", audit, err)
	}
}

func TestConcurrentManagementIdempotencyProducesOneSideEffect(t *testing.T) {
	router, persistence := managementRouter(t, testPrincipal{})
	now := time.Now().UTC()
	endpoint := managementEndpoint(71, 72, 80, domain.EndpointTypeHCWeb, now)
	if err := persistence.CreateEndpoint(context.Background(), endpoint); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	const key = "endpoint-concurrent-idempotency-0001"
	path := "/api/harness/v1/endpoints/" + endpoint.ID.String() + "/suspend"
	start := make(chan struct{})
	responses := make(chan *httptest.ResponseRecorder, 2)
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			responses <- requestJSONWithKey(router, http.MethodPost, path, "", key)
		}()
	}
	close(start)
	group.Wait()
	close(responses)
	successes := 0
	for response := range responses {
		switch response.Code {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			if !strings.Contains(response.Body.String(), "HARNESS_IDEMPOTENCY_IN_PROGRESS") {
				t.Fatalf("unexpected conflict body: %s", response.Body.String())
			}
		default:
			t.Fatalf("unexpected concurrent status = %d body=%s", response.Code, response.Body.String())
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent request completed successfully")
	}
	replay := requestJSONWithKey(router, http.MethodPost, path, "", key)
	if replay.Code != http.StatusOK || replay.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("post-concurrency replay status=%d header=%q body=%s", replay.Code, replay.Header().Get("Idempotency-Replayed"), replay.Body.String())
	}
	values, err := persistence.ListEndpoints(context.Background(), "owner", "tenant", 10)
	if err != nil || len(values) != 1 || values[0].Status != domain.EndpointStatusSuspended || values[0].RowVersion != 1 {
		t.Fatalf("concurrent endpoint = %#v err=%v", values, err)
	}
	audit, err := persistence.ListAudit(context.Background(), "owner", "tenant", 10)
	if err != nil || len(audit) != 1 {
		t.Fatalf("concurrent audit = %#v err=%v", audit, err)
	}
}
