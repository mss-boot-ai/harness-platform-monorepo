package harness

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"github.com/mss-boot-io/mss-boot-admin/admin/business"
)

func enrollmentDecision(runtime business.Runtime, approve bool) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		var request enrollmentDecisionRequest
		if err := decodeManagementJSON(c, &request); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "valid userCode is required", err))
			return
		}
		normalizedCode := normalizeUserCode(request.UserCode)
		if normalizedCode == "" {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "valid userCode is required", nil))
			return
		}
		decision := "deny"
		operation := "enrollment.deny"
		if approve {
			decision = "approve"
			operation = "enrollment.approve"
		}
		requestHash, err := managementMutationRequestHash(operation, id.String(), struct {
			UserCode string `json:"userCode"`
		}{UserCode: normalizedCode})
		if err != nil {
			writeManagementError(c, err)
			return
		}
		executeManagementMutation(c, management, managementMutationSpec{
			Operation:   operation,
			ObjectType:  "enrollment",
			ObjectID:    id.String(),
			RequestHash: requestHash,
			BaseAuditMetadata: map[string]string{
				"decision": decision,
			},
			Execute: func(transaction *store.Store, now time.Time) (managementMutationResult, error) {
				var value domain.Enrollment
				var mutationErr error
				if approve {
					value, mutationErr = transaction.ApproveEnrollmentWithCode(
						c.Request.Context(), id, hashUserCode(normalizedCode),
						management.owner, management.tenant, now,
					)
				} else {
					value, mutationErr = transaction.DenyEnrollmentWithCode(
						c.Request.Context(), id, hashUserCode(normalizedCode),
						management.owner, management.tenant, now,
					)
				}
				if mutationErr != nil {
					return managementMutationResult{}, mutationErr
				}
				return managementMutationResult{
					HTTPStatus: http.StatusOK,
					Body:       projectEnrollment(value),
					AuditMetadata: map[string]string{
						"previousStatus":  string(domain.EnrollmentStatusPending),
						"currentStatus":   string(value.Status),
						"resourceVersion": domain.FormatAuditUint(value.RowVersion),
					},
				}, nil
			},
		})
	})
}

func endpointAction(
	runtime business.Runtime,
	operation string,
	mutate func(*domain.Endpoint, time.Time) error,
) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		if err := decodeOptionalEmptyManagementJSON(c); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "request body must be empty or an empty object", err))
			return
		}
		requestHash, err := managementMutationRequestHash(operation, id.String(), struct{}{})
		if err != nil {
			writeManagementError(c, err)
			return
		}
		executeManagementMutation(c, management, managementMutationSpec{
			Operation:   operation,
			ObjectType:  "endpoint",
			ObjectID:    id.String(),
			RequestHash: requestHash,
			Execute: func(transaction *store.Store, now time.Time) (managementMutationResult, error) {
				value, mutationErr := transaction.UpdateEndpointForOwner(
					c.Request.Context(), id, management.owner, management.tenant,
					func(endpoint *domain.Endpoint) error {
						return mutate(endpoint, now)
					},
				)
				if mutationErr != nil {
					return managementMutationResult{}, mutationErr
				}
				return managementMutationResult{
					HTTPStatus: http.StatusOK,
					Body:       projectEndpoint(value),
					AuditMetadata: map[string]string{
						"currentStatus":   string(value.Status),
						"endpointType":    string(value.Type),
						"resourceVersion": domain.FormatAuditUint(value.RowVersion),
					},
				}, nil
			},
		})
	})
}

func revokeEndpoint(runtime business.Runtime) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		if err := decodeOptionalEmptyManagementJSON(c); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "request body must be empty or an empty object", err))
			return
		}
		const operation = "endpoint.revoke"
		requestHash, err := managementMutationRequestHash(operation, id.String(), struct{}{})
		if err != nil {
			writeManagementError(c, err)
			return
		}
		executeManagementMutation(c, management, managementMutationSpec{
			Operation:   operation,
			ObjectType:  "endpoint",
			ObjectID:    id.String(),
			RequestHash: requestHash,
			Execute: func(transaction *store.Store, now time.Time) (managementMutationResult, error) {
				value, mutationErr := transaction.RevokeEndpointForOwner(
					c.Request.Context(), id, management.owner, management.tenant, now,
				)
				if mutationErr != nil {
					return managementMutationResult{}, mutationErr
				}
				return managementMutationResult{
					HTTPStatus: http.StatusOK,
					Body:       projectEndpoint(value),
					AuditMetadata: map[string]string{
						"currentStatus":   string(value.Status),
						"endpointType":    string(value.Type),
						"resourceVersion": domain.FormatAuditUint(value.RowVersion),
					},
				}, nil
			},
		})
	})
}

func closeSession(runtime business.Runtime) gin.HandlerFunc {
	return withManagementResource(runtime, func(c *gin.Context, management managementContext, id domain.ID) {
		if err := decodeOptionalEmptyManagementJSON(c); err != nil {
			writeManagementError(c, domain.NewProblem(domain.CodeInvalidArgument, "request body must be empty or an empty object", err))
			return
		}
		const operation = "session.close"
		requestHash, err := managementMutationRequestHash(operation, id.String(), struct{}{})
		if err != nil {
			writeManagementError(c, err)
			return
		}
		executeManagementMutation(c, management, managementMutationSpec{
			Operation:   operation,
			ObjectType:  "session",
			ObjectID:    id.String(),
			RequestHash: requestHash,
			Execute: func(transaction *store.Store, now time.Time) (managementMutationResult, error) {
				value, mutationErr := transaction.CloseSessionForOwner(
					c.Request.Context(), id, management.owner, management.tenant, now,
				)
				if mutationErr != nil {
					return managementMutationResult{}, mutationErr
				}
				return managementMutationResult{
					HTTPStatus: http.StatusOK,
					Body:       projectSession(value),
					AuditMetadata: map[string]string{
						"sessionStatus":   string(value.Status),
						"keyGeneration":   domain.FormatAuditUint(value.CurrentKeyGeneration),
						"resourceVersion": domain.FormatAuditUint(value.RowVersion),
					},
				}, nil
			},
		})
	})
}
