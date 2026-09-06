package domain

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var safeLocalID = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,62}[A-Za-z0-9])?$`)

var allowedCapabilities = map[string]struct{}{
	"prompt":     {},
	"permission": {},
	"cancel":     {},
	"session":    {},
}

func (request *SessionRequest) Validate() error {
	if request == nil {
		return NewProblem(CodeInvalidArgument, "session request is required", nil)
	}
	if request.ABAEndpointID.IsZero() || request.HCEndpointID.IsZero() {
		return NewProblem(CodeInvalidArgument, "ABA and HC endpoint IDs are required", nil)
	}
	if request.ABAEndpointID == request.HCEndpointID {
		return NewProblem(CodeSecurityViolation, "ABA and HC endpoints must be distinct", nil)
	}
	for label, value := range map[string]string{
		"runtimeProfileId": request.RuntimeProfileID,
		"workspaceId":      request.WorkspaceID,
	} {
		if !safeLocalID.MatchString(value) {
			return NewProblem(CodeInvalidArgument, label+" is invalid", nil)
		}
	}
	if len(request.RequestedCapabilities) == 0 || len(request.RequestedCapabilities) > 8 {
		return NewProblem(CodeInvalidArgument, "requested capabilities are invalid", nil)
	}
	seen := make(map[string]struct{}, len(request.RequestedCapabilities))
	for _, capability := range request.RequestedCapabilities {
		capability = strings.TrimSpace(capability)
		if _, allowed := allowedCapabilities[capability]; !allowed {
			return NewProblem(CodeInvalidArgument, "unsupported capability: "+capability, nil)
		}
		if _, duplicate := seen[capability]; duplicate {
			return NewProblem(CodeInvalidArgument, "duplicate capability: "+capability, nil)
		}
		seen[capability] = struct{}{}
	}
	sort.Strings(request.RequestedCapabilities)
	return nil
}

func (endpoint *Endpoint) Activate(now time.Time) error {
	if endpoint == nil {
		return NewProblem(CodeInvalidArgument, "endpoint is required", nil)
	}
	if endpoint.Status != EndpointStatusPending {
		return invalidTransition("endpoint", string(endpoint.Status), string(EndpointStatusActive))
	}
	endpoint.Status = EndpointStatusActive
	endpoint.UpdatedAt = now
	endpoint.RowVersion++
	return nil
}

func (endpoint *Endpoint) Suspend(now time.Time) error {
	if endpoint == nil {
		return NewProblem(CodeInvalidArgument, "endpoint is required", nil)
	}
	if endpoint.Status == EndpointStatusRevoked {
		return NewProblem(CodeRevoked, "endpoint is revoked", nil)
	}
	if endpoint.Status != EndpointStatusActive {
		return invalidTransition("endpoint", string(endpoint.Status), string(EndpointStatusSuspended))
	}
	endpoint.Status = EndpointStatusSuspended
	endpoint.UpdatedAt = now
	endpoint.RowVersion++
	return nil
}

func (endpoint *Endpoint) Resume(now time.Time) error {
	if endpoint == nil {
		return NewProblem(CodeInvalidArgument, "endpoint is required", nil)
	}
	if endpoint.Status == EndpointStatusRevoked {
		return NewProblem(CodeRevoked, "endpoint is revoked", nil)
	}
	if endpoint.Status != EndpointStatusSuspended {
		return invalidTransition("endpoint", string(endpoint.Status), string(EndpointStatusActive))
	}
	endpoint.Status = EndpointStatusActive
	endpoint.UpdatedAt = now
	endpoint.RowVersion++
	return nil
}

func (endpoint *Endpoint) Revoke(now time.Time) error {
	if endpoint == nil {
		return NewProblem(CodeInvalidArgument, "endpoint is required", nil)
	}
	if endpoint.Status == EndpointStatusRevoked {
		return nil
	}
	if endpoint.Status != EndpointStatusPending && endpoint.Status != EndpointStatusActive && endpoint.Status != EndpointStatusSuspended {
		return invalidTransition("endpoint", string(endpoint.Status), string(EndpointStatusRevoked))
	}
	endpoint.Status = EndpointStatusRevoked
	endpoint.RevokedAt = &now
	endpoint.UpdatedAt = now
	endpoint.RowVersion++
	return nil
}

func (enrollment *Enrollment) Approve(actor string, now time.Time) error {
	if enrollment == nil {
		return NewProblem(CodeInvalidArgument, "enrollment is required", nil)
	}
	if enrollment.Status != EnrollmentStatusPending {
		return invalidTransition("enrollment", string(enrollment.Status), string(EnrollmentStatusApproved))
	}
	if !now.Before(enrollment.ExpiresAt) {
		enrollment.Status = EnrollmentStatusExpired
		enrollment.UpdatedAt = now
		enrollment.RowVersion++
		return NewProblem(CodeExpired, "enrollment expired before approval", nil)
	}
	if strings.TrimSpace(actor) == "" {
		return NewProblem(CodeInvalidArgument, "approval actor is required", nil)
	}
	enrollment.Status = EnrollmentStatusApproved
	enrollment.ApprovedBy = actor
	enrollment.ApprovedAt = &now
	enrollment.UpdatedAt = now
	enrollment.RowVersion++
	return nil
}

func (enrollment *Enrollment) Deny(actor string, now time.Time) error {
	if enrollment == nil {
		return NewProblem(CodeInvalidArgument, "enrollment is required", nil)
	}
	if enrollment.Status != EnrollmentStatusPending {
		return invalidTransition("enrollment", string(enrollment.Status), string(EnrollmentStatusDenied))
	}
	if strings.TrimSpace(actor) == "" {
		return NewProblem(CodeInvalidArgument, "denial actor is required", nil)
	}
	enrollment.Status = EnrollmentStatusDenied
	enrollment.ApprovedBy = actor
	enrollment.ApprovedAt = &now
	enrollment.UpdatedAt = now
	enrollment.RowVersion++
	return nil
}

func (enrollment *Enrollment) Expire(now time.Time) error {
	if enrollment == nil {
		return NewProblem(CodeInvalidArgument, "enrollment is required", nil)
	}
	if enrollment.Status == EnrollmentStatusExpired {
		return nil
	}
	if enrollment.Status != EnrollmentStatusPending && enrollment.Status != EnrollmentStatusApproved {
		return invalidTransition("enrollment", string(enrollment.Status), string(EnrollmentStatusExpired))
	}
	if now.Before(enrollment.ExpiresAt) {
		return NewProblem(CodeInvalidState, "enrollment has not reached its expiry", nil)
	}
	enrollment.Status = EnrollmentStatusExpired
	enrollment.UpdatedAt = now
	enrollment.RowVersion++
	return nil
}

func (enrollment *Enrollment) Consume(endpointID ID, now time.Time) error {
	if enrollment == nil {
		return NewProblem(CodeInvalidArgument, "enrollment is required", nil)
	}
	if enrollment.Status == EnrollmentStatusConsumed {
		if enrollment.EndpointID == endpointID {
			return nil
		}
		return NewProblem(CodeConflict, "enrollment was consumed for a different endpoint", nil)
	}
	if enrollment.Status != EnrollmentStatusApproved {
		return invalidTransition("enrollment", string(enrollment.Status), string(EnrollmentStatusConsumed))
	}
	if !now.Before(enrollment.ExpiresAt) {
		enrollment.Status = EnrollmentStatusExpired
		enrollment.UpdatedAt = now
		enrollment.RowVersion++
		return NewProblem(CodeExpired, "enrollment expired before consumption", nil)
	}
	if endpointID.IsZero() {
		return NewProblem(CodeInvalidArgument, "consumed endpoint ID is required", nil)
	}
	enrollment.Status = EnrollmentStatusConsumed
	enrollment.EndpointID = endpointID
	enrollment.ConsumedAt = &now
	enrollment.UpdatedAt = now
	enrollment.RowVersion++
	return nil
}

func (session *Session) WaitForKey(now time.Time) error {
	return session.move(SessionStatusCreating, SessionStatusWaitingKey, now)
}

func (session *Session) Activate(generation uint64, now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status != SessionStatusWaitingKey && session.Status != SessionStatusRekeyRequired {
		return invalidTransition("session", string(session.Status), string(SessionStatusActive))
	}
	if generation == 0 || generation <= session.CurrentKeyGeneration {
		return NewProblem(CodeInvalidArgument, "session key generation must increase", nil)
	}
	session.CurrentKeyGeneration = generation
	session.Status = SessionStatusActive
	session.UpdatedAt = now
	session.LastActivityAt = &now
	session.RowVersion++
	return nil
}

func (session *Session) RequireRekey(now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status == SessionStatusRekeyRequired {
		return nil
	}
	if session.Status != SessionStatusActive {
		return invalidTransition("session", string(session.Status), string(SessionStatusRekeyRequired))
	}
	session.Status = SessionStatusRekeyRequired
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (session *Session) MarkUncertain(now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status != SessionStatusActive && session.Status != SessionStatusDraining {
		return invalidTransition("session", string(session.Status), string(SessionStatusUncertain))
	}
	session.Status = SessionStatusUncertain
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (session *Session) Fail(now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status == SessionStatusFailed {
		return nil
	}
	switch session.Status {
	case SessionStatusCreating, SessionStatusWaitingKey, SessionStatusRekeyRequired:
	default:
		return invalidTransition("session", string(session.Status), string(SessionStatusFailed))
	}
	session.Status = SessionStatusFailed
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (session *Session) StartDraining(now time.Time) error {
	return session.move(SessionStatusActive, SessionStatusDraining, now)
}

func (session *Session) Close(now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status == SessionStatusClosed {
		return nil
	}
	switch session.Status {
	case SessionStatusCreating, SessionStatusWaitingKey, SessionStatusActive, SessionStatusRekeyRequired, SessionStatusDraining, SessionStatusUncertain, SessionStatusFailed:
	default:
		return invalidTransition("session", string(session.Status), string(SessionStatusClosed))
	}
	session.Status = SessionStatusClosed
	session.ClosedAt = &now
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (session *Session) MarkABARevoked(now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status == SessionStatusABARevoked {
		return nil
	}
	if session.Status == SessionStatusClosed {
		return invalidTransition("session", string(session.Status), string(SessionStatusABARevoked))
	}
	session.Status = SessionStatusABARevoked
	session.ClosedAt = &now
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (session *Session) move(from, to SessionStatus, now time.Time) error {
	if session == nil {
		return NewProblem(CodeInvalidArgument, "session is required", nil)
	}
	if session.Status != from {
		return invalidTransition("session", string(session.Status), string(to))
	}
	session.Status = to
	session.UpdatedAt = now
	session.RowVersion++
	return nil
}

func (frame *EncryptedFrame) MarkRouted(now time.Time) error {
	if frame == nil {
		return NewProblem(CodeInvalidArgument, "frame is required", nil)
	}
	if frame.Status == FrameStatusRouted {
		return nil
	}
	if frame.Status != FrameStatusStored {
		return invalidTransition("frame", string(frame.Status), string(FrameStatusRouted))
	}
	frame.Status = FrameStatusRouted
	frame.RoutedAt = &now
	return nil
}

func (frame *EncryptedFrame) Acknowledge(now time.Time) error {
	if frame == nil {
		return NewProblem(CodeInvalidArgument, "frame is required", nil)
	}
	if frame.Status == FrameStatusReceiverAcknowledged {
		return nil
	}
	if frame.Status != FrameStatusStored && frame.Status != FrameStatusRouted {
		return invalidTransition("frame", string(frame.Status), string(FrameStatusReceiverAcknowledged))
	}
	frame.Status = FrameStatusReceiverAcknowledged
	frame.AcknowledgedAt = &now
	return nil
}

func (frame *EncryptedFrame) Conflict() error {
	if frame == nil {
		return NewProblem(CodeInvalidArgument, "frame is required", nil)
	}
	if frame.Status == FrameStatusReceiverAcknowledged || frame.Status == FrameStatusExpired {
		return invalidTransition("frame", string(frame.Status), string(FrameStatusConflict))
	}
	frame.Status = FrameStatusConflict
	return nil
}

func invalidTransition(object, from, to string) error {
	return NewProblem(CodeInvalidState, fmt.Sprintf("%s cannot transition from %s to %s", object, from, to), nil)
}
