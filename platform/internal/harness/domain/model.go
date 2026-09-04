package domain

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type EndpointType string

const (
	EndpointTypeABA         EndpointType = "ABA"
	EndpointTypeHCWeb       EndpointType = "HC_WEB"
	EndpointTypeHCReference EndpointType = "HC_REFERENCE"
)

func (value EndpointType) Valid() bool {
	switch value {
	case EndpointTypeABA, EndpointTypeHCWeb, EndpointTypeHCReference:
		return true
	default:
		return false
	}
}

type EndpointStatus string

const (
	EndpointStatusPending   EndpointStatus = "PENDING"
	EndpointStatusActive    EndpointStatus = "ACTIVE"
	EndpointStatusSuspended EndpointStatus = "SUSPENDED"
	EndpointStatusRevoked   EndpointStatus = "REVOKED"
)

type EnrollmentStatus string

const (
	EnrollmentStatusPending  EnrollmentStatus = "PENDING"
	EnrollmentStatusApproved EnrollmentStatus = "APPROVED"
	EnrollmentStatusDenied   EnrollmentStatus = "DENIED"
	EnrollmentStatusExpired  EnrollmentStatus = "EXPIRED"
	EnrollmentStatusConsumed EnrollmentStatus = "CONSUMED"
)

type CredentialStatus string

const (
	CredentialStatusActive  CredentialStatus = "ACTIVE"
	CredentialStatusRotated CredentialStatus = "ROTATED"
	CredentialStatusRevoked CredentialStatus = "REVOKED"
	CredentialStatusExpired CredentialStatus = "EXPIRED"
)

type SessionStatus string

const (
	SessionStatusCreating      SessionStatus = "CREATING"
	SessionStatusWaitingKey    SessionStatus = "WAITING_KEY"
	SessionStatusActive        SessionStatus = "ACTIVE"
	SessionStatusRekeyRequired SessionStatus = "REKEY_REQUIRED"
	SessionStatusDraining      SessionStatus = "DRAINING"
	SessionStatusUncertain     SessionStatus = "UNCERTAIN"
	SessionStatusClosed        SessionStatus = "CLOSED"
	SessionStatusFailed        SessionStatus = "FAILED"
	SessionStatusABARevoked    SessionStatus = "ABA_REVOKED"
)

type KeyPackageStatus string

const (
	KeyPackageStatusPending      KeyPackageStatus = "PENDING"
	KeyPackageStatusDelivered    KeyPackageStatus = "DELIVERED"
	KeyPackageStatusAcknowledged KeyPackageStatus = "ACKNOWLEDGED"
	KeyPackageStatusExpired      KeyPackageStatus = "EXPIRED"
	KeyPackageStatusRevoked      KeyPackageStatus = "REVOKED"
)

type TicketStatus string

const (
	TicketStatusIssued   TicketStatus = "ISSUED"
	TicketStatusConsumed TicketStatus = "CONSUMED"
	TicketStatusExpired  TicketStatus = "EXPIRED"
	TicketStatusRevoked  TicketStatus = "REVOKED"
)

type FrameStatus string

const (
	FrameStatusStored               FrameStatus = "STORED"
	FrameStatusRouted               FrameStatus = "ROUTED"
	FrameStatusReceiverAcknowledged FrameStatus = "RECEIVER_ACKED"
	FrameStatusExpired              FrameStatus = "EXPIRED"
	FrameStatusConflict             FrameStatus = "CONFLICT"
)

type Direction uint8

const (
	DirectionHCToABA Direction = 1
	DirectionABAToHC Direction = 2
)

func (value Direction) Valid() bool {
	return value == DirectionHCToABA || value == DirectionABAToHC
}

type Endpoint struct {
	ID                 ID
	OwnerUserID        string
	TenantID           string
	Type               EndpointType
	Name               string
	SigningPublicJWK   json.RawMessage
	KEMPublicJWK       json.RawMessage
	SigningJKT         string
	KEMJKT             string
	Status             EndpointStatus
	CredentialFamilyID ID
	SoftwareVersion    string
	PlatformName       string
	LastSeenAt         *time.Time
	RevokedAt          *time.Time
	RowVersion         uint64
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

func (endpoint Endpoint) Validate() error {
	if endpoint.ID.IsZero() {
		return NewProblem(CodeInvalidArgument, "endpoint ID is required", nil)
	}
	if strings.TrimSpace(endpoint.OwnerUserID) == "" {
		return NewProblem(CodeInvalidArgument, "endpoint owner is required", nil)
	}
	if !endpoint.Type.Valid() {
		return NewProblem(CodeInvalidArgument, "endpoint type is unsupported", nil)
	}
	if name := strings.TrimSpace(endpoint.Name); name == "" || len(name) > 120 {
		return NewProblem(CodeInvalidArgument, "endpoint name is invalid", nil)
	}
	if err := validatePublicJWK(endpoint.SigningPublicJWK); err != nil {
		return NewProblem(CodeInvalidArgument, "endpoint signing JWK is invalid", err)
	}
	if err := validatePublicJWK(endpoint.KEMPublicJWK); err != nil {
		return NewProblem(CodeInvalidArgument, "endpoint KEM JWK is invalid", err)
	}
	if err := validateJKT(endpoint.SigningJKT); err != nil {
		return NewProblem(CodeInvalidArgument, "endpoint signing JKT is invalid", err)
	}
	if err := validateJKT(endpoint.KEMJKT); err != nil {
		return NewProblem(CodeInvalidArgument, "endpoint KEM JKT is invalid", err)
	}
	if endpoint.SigningJKT == endpoint.KEMJKT {
		return NewProblem(CodeSecurityViolation, "signing and KEM keys must be distinct", nil)
	}
	if endpoint.Status == EndpointStatusRevoked && endpoint.RevokedAt == nil {
		return NewProblem(CodeInvalidState, "revoked endpoint requires revoked_at", nil)
	}
	return nil
}

type Enrollment struct {
	ID               ID
	OwnerUserID      string
	TenantID         string
	EndpointType     EndpointType
	EndpointName     string
	DeviceCodeHash   [32]byte
	UserCodeHash     [32]byte
	SigningPublicJWK json.RawMessage
	KEMPublicJWK     json.RawMessage
	SigningJKT       string
	KEMJKT           string
	Status           EnrollmentStatus
	ExpiresAt        time.Time
	ApprovedBy       string
	ApprovedAt       *time.Time
	ConsumedAt       *time.Time
	EndpointID       ID
	RowVersion       uint64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (enrollment Enrollment) Validate() error {
	if enrollment.ID.IsZero() {
		return NewProblem(CodeInvalidArgument, "enrollment ID is required", nil)
	}
	if !enrollment.EndpointType.Valid() {
		return NewProblem(CodeInvalidArgument, "enrollment endpoint type is unsupported", nil)
	}
	if strings.TrimSpace(enrollment.EndpointName) == "" {
		return NewProblem(CodeInvalidArgument, "enrollment endpoint name is required", nil)
	}
	if allZero(enrollment.DeviceCodeHash[:]) || allZero(enrollment.UserCodeHash[:]) {
		return NewProblem(CodeSecurityViolation, "enrollment code hashes must be populated", nil)
	}
	if enrollment.ExpiresAt.IsZero() {
		return NewProblem(CodeInvalidArgument, "enrollment expiry is required", nil)
	}
	if err := validatePublicJWK(enrollment.SigningPublicJWK); err != nil {
		return NewProblem(CodeInvalidArgument, "enrollment signing JWK is invalid", err)
	}
	if err := validatePublicJWK(enrollment.KEMPublicJWK); err != nil {
		return NewProblem(CodeInvalidArgument, "enrollment KEM JWK is invalid", err)
	}
	if err := validateJKT(enrollment.SigningJKT); err != nil {
		return NewProblem(CodeInvalidArgument, "enrollment signing JKT is invalid", err)
	}
	if err := validateJKT(enrollment.KEMJKT); err != nil {
		return NewProblem(CodeInvalidArgument, "enrollment KEM JKT is invalid", err)
	}
	if enrollment.SigningJKT == enrollment.KEMJKT {
		return NewProblem(CodeSecurityViolation, "signing and KEM keys must be distinct", nil)
	}
	return nil
}

type EndpointCredential struct {
	ID          ID
	EndpointID  ID
	FamilyID    ID
	TokenHash   [32]byte
	SigningJKT  string
	Scopes      []string
	Status      CredentialStatus
	ExpiresAt   time.Time
	RotatedFrom ID
	CreatedAt   time.Time
	UpdatedAt   time.Time
	RevokedAt   *time.Time
}

type Session struct {
	ID                    ID
	OwnerUserID           string
	TenantID              string
	ABAEndpointID         ID
	HCEndpointID          ID
	RuntimeProfileID      string
	WorkspaceID           string
	RequestedCapabilities []string
	Status                SessionStatus
	CurrentKeyGeneration  uint64
	CreatedAt             time.Time
	UpdatedAt             time.Time
	LastActivityAt        *time.Time
	ClosedAt              *time.Time
	RowVersion            uint64
}

type SessionRequest struct {
	ABAEndpointID         ID       `json:"abaEndpointId"`
	HCEndpointID          ID       `json:"hcEndpointId"`
	RuntimeProfileID      string   `json:"runtimeProfileId"`
	WorkspaceID           string   `json:"workspaceId"`
	RequestedCapabilities []string `json:"requestedCapabilities"`
}

type SessionKeyPackage struct {
	ID                    ID
	SessionID             ID
	Generation            uint64
	IssuerABAEndpointID   ID
	RecipientHCEndpointID ID
	CryptoSuite           uint16
	EncapsulatedKey       []byte
	Ciphertext            []byte
	ContextHash           [32]byte
	IssuerSignature       []byte
	IssuerCredentialID    ID
	Status                KeyPackageStatus
	ExpiresAt             time.Time
	AcknowledgedAt        *time.Time
	CreatedAt             time.Time
}

type WSTicket struct {
	ID           ID
	TokenHash    [32]byte
	EndpointID   ID
	CredentialID ID
	Purpose      string
	Origin       string
	Protocol     string
	Status       TicketStatus
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	CreatedAt    time.Time
	RowVersion   uint64
}

type EncryptedFrame struct {
	MessageID          ID
	ChannelID          ID
	SessionID          ID
	SenderEndpointID   ID
	ReceiverEndpointID ID
	Direction          Direction
	Sequence           uint64
	KeyGeneration      uint64
	KeyID              ID
	CreatedAtMS        int64
	AAD                []byte
	Ciphertext         []byte
	Signature          []byte
	ContentHash        [32]byte
	Status             FrameStatus
	ReceivedAt         time.Time
	RoutedAt           *time.Time
	AcknowledgedAt     *time.Time
	ExpiresAt          time.Time
}

func (frame EncryptedFrame) Validate(maxCiphertextBytes int) error {
	if frame.MessageID.IsZero() || frame.ChannelID.IsZero() || frame.SessionID.IsZero() || frame.SenderEndpointID.IsZero() || frame.ReceiverEndpointID.IsZero() || frame.KeyID.IsZero() {
		return NewProblem(CodeInvalidArgument, "frame identifiers must be non-zero", nil)
	}
	if !frame.Direction.Valid() {
		return NewProblem(CodeInvalidArgument, "frame direction is invalid", nil)
	}
	if frame.Sequence == 0 || frame.KeyGeneration == 0 {
		return NewProblem(CodeInvalidArgument, "frame sequence and generation must be non-zero", nil)
	}
	if len(frame.AAD) != 148 {
		return NewProblem(CodeInvalidArgument, "frame AAD must be exactly 148 bytes", nil)
	}
	if len(frame.Ciphertext) == 0 || len(frame.Ciphertext) > maxCiphertextBytes {
		return NewProblem(CodeResourceLimit, "frame ciphertext length is outside the allowed range", nil)
	}
	if len(frame.Signature) != 64 {
		return NewProblem(CodeInvalidArgument, "frame signature must be 64-byte P1363", nil)
	}
	if allZero(frame.ContentHash[:]) {
		return NewProblem(CodeInvalidArgument, "frame content hash is required", nil)
	}
	return nil
}

type AckCursor struct {
	SessionID                 ID
	KeyGeneration             uint64
	Direction                 Direction
	SenderEndpointID          ID
	ReceiverEndpointID        ID
	HighestContiguousSequence uint64
	ReceivedRanges            []SequenceRange
	UpdatedAt                 time.Time
	RowVersion                uint64
}

type SequenceRange struct {
	Start uint64
	End   uint64
}

type AuditEvent struct {
	ID              ID
	OwnerUserID     string
	TenantID        string
	ActorEndpointID ID
	Action          string
	ObjectType      string
	ObjectID        string
	Result          string
	ErrorCode       string
	Metadata        map[string]string
	CreatedAt       time.Time
}

func validateJKT(value string) error {
	value = strings.TrimSpace(value)
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return fmt.Errorf("expected a base64url SHA-256 thumbprint")
	}
	return nil
}

func validatePublicJWK(value json.RawMessage) error {
	if len(value) == 0 || len(value) > 8192 {
		return fmt.Errorf("JWK length is invalid")
	}
	var key map[string]any
	if err := json.Unmarshal(value, &key); err != nil {
		return err
	}
	if key["kty"] != "EC" || key["crv"] != "P-256" {
		return fmt.Errorf("only EC P-256 public keys are allowed")
	}
	if _, private := key["d"]; private {
		return fmt.Errorf("private JWK material is forbidden")
	}
	for _, field := range []string{"x", "y"} {
		if strings.TrimSpace(fmt.Sprint(key[field])) == "" || fmt.Sprint(key[field]) == "<nil>" {
			return fmt.Errorf("JWK field %s is required", field)
		}
	}
	return nil
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}
