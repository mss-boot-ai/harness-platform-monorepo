package harness

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
)

const managementIdempotencyTTL = 24 * time.Hour

type managementMutationResult struct {
	HTTPStatus    int
	Body          gin.H
	AuditMetadata map[string]string
}

type managementMutationSpec struct {
	Operation         string
	ObjectType        string
	ObjectID          string
	RequestHash       [32]byte
	BaseAuditMetadata map[string]string
	Execute           func(*store.Store, time.Time) (managementMutationResult, error)
}

func executeManagementMutation(
	c *gin.Context,
	management managementContext,
	spec managementMutationSpec,
) {
	if spec.Execute == nil || spec.Operation == "" || spec.ObjectType == "" || spec.ObjectID == "" {
		writeManagementError(c, errors.New("invalid management mutation specification"))
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if strings.TrimSpace(idempotencyKey) != idempotencyKey {
		writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "Idempotency-Key is invalid", nil))
		return
	}
	if err := domain.ValidateIdempotencyKey(idempotencyKey); err != nil {
		writeManagementError(c, err)
		return
	}

	now := time.Now().UTC()
	recordID, err := domain.NewID(nil)
	if err != nil {
		writeManagementError(c, err)
		return
	}
	record := domain.IdempotencyRecord{
		ID:          recordID,
		OwnerUserID: management.owner,
		TenantID:    management.tenant,
		ActorID:     management.owner,
		Operation:   spec.Operation,
		Key:         idempotencyKey,
		RequestHash: spec.RequestHash,
		Status:      domain.IdempotencyStatusInProgress,
		CreatedAt:   now,
		UpdatedAt:   now,
		ExpiresAt:   now.Add(managementIdempotencyTTL),
	}
	reservation, err := management.store.ReserveIdempotency(c.Request.Context(), record)
	if err != nil {
		writeManagementError(c, err)
		return
	}
	switch reservation.Outcome {
	case store.IdempotencyReplay:
		c.Header("Idempotency-Replayed", "true")
		writeManagementBytes(c, reservation.Record.HTTPStatus, reservation.Record.ResponseJSON)
		return
	case store.IdempotencyInProgress:
		c.JSON(http.StatusConflict, gin.H{
			"code":    "HARNESS_IDEMPOTENCY_IN_PROGRESS",
			"message": "another request with this Idempotency-Key is still in progress",
		})
		return
	case store.IdempotencyAcquired:
	default:
		writeManagementError(c, errors.New("unsupported idempotency reservation outcome"))
		return
	}

	var mutationResult managementMutationResult
	var mutationErr error
	var responseJSON []byte
	transactionErr := management.store.WithTransaction(c.Request.Context(), func(transaction *store.Store) error {
		mutationResult, mutationErr = spec.Execute(transaction, now)
		if mutationErr != nil {
			return mutationErr
		}
		if mutationResult.HTTPStatus < 200 || mutationResult.HTTPStatus > 299 || mutationResult.Body == nil {
			return errors.New("management mutation returned an invalid success response")
		}
		responseJSON, err = json.Marshal(mutationResult.Body)
		if err != nil {
			return fmt.Errorf("encode management mutation response: %w", err)
		}
		auditEvent, auditErr := newManagementAudit(
			management, spec, "success", "", now,
			mergeAuditMetadata(spec.BaseAuditMetadata, mutationResult.AuditMetadata, map[string]string{
				"httpStatus":     fmt.Sprintf("%d", mutationResult.HTTPStatus),
				"requestOutcome": "completed",
			}),
		)
		if auditErr != nil {
			return auditErr
		}
		if auditErr := transaction.AppendAudit(c.Request.Context(), auditEvent); auditErr != nil {
			return auditErr
		}
		completed := reservation.Record
		completed.Status = domain.IdempotencyStatusCompleted
		completed.HTTPStatus = mutationResult.HTTPStatus
		completed.ResponseJSON = responseJSON
		completed.UpdatedAt = now
		if _, completionErr := transaction.CompleteIdempotency(c.Request.Context(), completed); completionErr != nil {
			return completionErr
		}
		return nil
	})
	if mutationErr != nil {
		if transactionErr != nil && !errors.Is(transactionErr, mutationErr) {
			writeManagementError(c, transactionErr)
			return
		}
		finalizeManagementMutationFailure(c, management, spec, reservation.Record, mutationErr, now)
		return
	}
	if transactionErr != nil {
		writeManagementError(c, transactionErr)
		return
	}
	writeManagementBytes(c, mutationResult.HTTPStatus, responseJSON)
}

func finalizeManagementMutationFailure(
	c *gin.Context,
	management managementContext,
	spec managementMutationSpec,
	record domain.IdempotencyRecord,
	mutationErr error,
	now time.Time,
) {
	status, payload, errorCode := managementErrorPayload(mutationErr)
	responseJSON, err := json.Marshal(payload)
	if err != nil {
		writeManagementError(c, err)
		return
	}
	auditEvent, err := newManagementAudit(
		management, spec, "failure", errorCode, now,
		mergeAuditMetadata(spec.BaseAuditMetadata, map[string]string{
			"httpStatus":     fmt.Sprintf("%d", status),
			"requestOutcome": "failed",
		}),
	)
	if err != nil {
		writeManagementError(c, err)
		return
	}
	err = management.store.WithTransaction(c.Request.Context(), func(transaction *store.Store) error {
		if appendErr := transaction.AppendAudit(c.Request.Context(), auditEvent); appendErr != nil {
			return appendErr
		}
		completed := record
		completed.Status = domain.IdempotencyStatusCompleted
		completed.HTTPStatus = status
		completed.ResponseJSON = responseJSON
		completed.ErrorCode = errorCode
		completed.UpdatedAt = now
		_, completionErr := transaction.CompleteIdempotency(c.Request.Context(), completed)
		return completionErr
	})
	if err != nil {
		writeManagementError(c, err)
		return
	}
	writeManagementBytes(c, status, responseJSON)
}

func managementMutationRequestHash(operation, objectID string, body any) ([32]byte, error) {
	canonical, err := json.Marshal(struct {
		Operation string `json:"operation"`
		ObjectID  string `json:"objectId"`
		Body      any    `json:"body"`
	}{
		Operation: operation,
		ObjectID:  objectID,
		Body:      body,
	})
	if err != nil {
		return [32]byte{}, domain.NewProblem(domain.CodeInvalidArgument, "management request cannot be canonicalized", err)
	}
	return sha256.Sum256(canonical), nil
}

func newManagementAudit(
	management managementContext,
	spec managementMutationSpec,
	result string,
	errorCode string,
	now time.Time,
	metadata map[string]string,
) (domain.SecurityAuditEvent, error) {
	id, err := domain.NewID(nil)
	if err != nil {
		return domain.SecurityAuditEvent{}, err
	}
	event := domain.SecurityAuditEvent{
		ID:          id,
		OwnerUserID: management.owner,
		TenantID:    management.tenant,
		ActorType:   domain.AuditActorHuman,
		ActorID:     management.owner,
		Action:      spec.Operation,
		ObjectType:  spec.ObjectType,
		ObjectID:    spec.ObjectID,
		Result:      result,
		ErrorCode:   errorCode,
		Metadata:    metadata,
		CreatedAt:   now,
	}
	if err := event.Validate(); err != nil {
		return domain.SecurityAuditEvent{}, err
	}
	return event, nil
}

func mergeAuditMetadata(values ...map[string]string) map[string]string {
	merged := make(map[string]string)
	for _, value := range values {
		for key, item := range value {
			merged[key] = item
		}
	}
	return merged
}

func writeManagementBytes(c *gin.Context, status int, body []byte) {
	c.Data(status, "application/json; charset=utf-8", body)
}
