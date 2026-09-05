package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"regexp"
	"slices"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

var stableControlErrorCode = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)

func (server *Server) handleReadyPacket(
	ctx context.Context,
	connection *activeConnection,
	endpoint domain.Endpoint,
	credential domain.EndpointCredential,
	encoded []byte,
	now time.Time,
) error {
	if connection == nil || len(encoded) == 0 || len(encoded) > maxWirePacketBytes {
		return errors.New("ready packet input is invalid")
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, packet); err != nil {
		return errors.New("ready packet is not valid AWP protobuf")
	}
	if packet.GetEncrypted() != nil {
		return server.processEncryptedFrame(ctx, endpoint, credential, packet, encoded, now)
	}
	if packet.GetAck() != nil {
		receiverID, err := server.processAckFrame(ctx, endpoint, packet, now)
		if err != nil {
			return err
		}
		if err := server.connections.send(receiverID, encoded); err != nil &&
			!errors.Is(err, errConnectionOffline) && !errors.Is(err, errConnectionBackpressure) {
			return err
		}
		return nil
	}
	if packet.GetError() != nil {
		receiverID, err := server.processEndpointError(ctx, endpoint, packet, now)
		if err != nil {
			return err
		}
		if err := server.connections.send(receiverID, encoded); err != nil &&
			!errors.Is(err, errConnectionOffline) && !errors.Is(err, errConnectionBackpressure) {
			return err
		}
		return nil
	}
	control := packet.GetControl()
	if packet.GetWireMajor() != 1 || packet.GetWireMinor() != 0 || len(packet.GetPacketId()) != 16 ||
		control == nil || !readyControlAllowed(endpoint.Type, control.GetType()) {
		return errors.New("ready packet type is not allowed")
	}
	if len(control.GetMessageId()) != 16 || !bytes.Equal(control.GetSenderEndpointId(), endpoint.ID[:]) ||
		len(control.GetReceiverEndpointId()) != 16 || control.GetControlSequence() == 0 ||
		len(control.GetPayload()) == 0 || len(control.GetSignature()) != 64 ||
		!connection.acceptInboundControlSequence(control.GetControlSequence()) {
		return errors.New("control frame binding is invalid")
	}
	createdAt := time.UnixMilli(control.GetCreatedAtMs()).UTC()
	if createdAt.Before(now.Add(-time.Minute)) || createdAt.After(now.Add(time.Minute)) {
		return errors.New("control frame time is invalid")
	}
	transcript, err := controlTranscript(
		control.GetMessageId(), control.GetSenderEndpointId(), control.GetReceiverEndpointId(),
		control.GetControlSequence(), control.GetCreatedAtMs(), uint32(control.GetType()), control.GetPayload(),
	)
	if err != nil {
		return err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, transcript, control.GetSignature()) {
		return errors.New("control frame signature is invalid")
	}
	if control.GetType() == awpv1.ControlType_CONTROL_TYPE_RESUME_STATE {
		return server.processResumeState(ctx, endpoint, control)
	}
	var receiverID domain.ID
	switch control.GetType() {
	case awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_RESULT:
		receiverID, err = server.processOpenTunnelResult(ctx, endpoint, control, now)
	case awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE:
		receiverID, err = server.processSessionKeyPackage(ctx, endpoint, credential, control, now)
	case awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE_ACK:
		receiverID, err = server.processSessionKeyPackageACK(ctx, endpoint, control, now)
	case awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_RESULT:
		receiverID, err = server.processCloseTunnelResult(ctx, endpoint, control)
	default:
		return errors.New("ready control type is unsupported")
	}
	if err != nil {
		return err
	}
	if err := server.connections.send(receiverID, encoded); err != nil &&
		!errors.Is(err, errConnectionOffline) && !errors.Is(err, errConnectionBackpressure) {
		return err
	}
	return nil
}

func readyControlAllowed(endpointType domain.EndpointType, controlType awpv1.ControlType) bool {
	switch endpointType {
	case domain.EndpointTypeABA:
		return controlType == awpv1.ControlType_CONTROL_TYPE_RESUME_STATE ||
			controlType == awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_RESULT ||
			controlType == awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE ||
			controlType == awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_RESULT
	case domain.EndpointTypeHCWeb, domain.EndpointTypeHCReference:
		return controlType == awpv1.ControlType_CONTROL_TYPE_RESUME_STATE ||
			controlType == awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE_ACK
	default:
		return false
	}
}

func (server *Server) processOpenTunnelResult(
	ctx context.Context,
	endpoint domain.Endpoint,
	control *awpv1.ControlFrame,
	now time.Time,
) (domain.ID, error) {
	result := new(awpv1.OpenTunnelResult)
	if err := proto.Unmarshal(control.GetPayload(), result); err != nil {
		return domain.ID{}, errors.New("OpenTunnelResult payload is invalid")
	}
	sessionID, err := idFromWire(result.GetSessionId())
	if err != nil {
		return domain.ID{}, err
	}
	receiverID, err := idFromWire(control.GetReceiverEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return domain.ID{}, err
	}
	if session.ABAEndpointID != endpoint.ID || session.HCEndpointID != receiverID ||
		session.Status != domain.SessionStatusCreating || result.GetActiveKeyGeneration() != 0 ||
		!capabilitySubset(result.GetNegotiatedCapabilityHints(), session.RequestedCapabilities) {
		return domain.ID{}, errors.New("OpenTunnelResult session binding is invalid")
	}
	switch result.GetStatus() {
	case awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_ACCEPTED, awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_ALREADY_OPEN:
		if result.GetStableErrorCode() != "" || result.GetAcceptedAuthorizationRevision() != 1 {
			return domain.ID{}, errors.New("accepted OpenTunnelResult is invalid")
		}
		_, err = server.persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
			return value.WaitForKey(now)
		})
	case awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_REJECTED, awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_RESOURCE_BUSY:
		if !stableControlErrorCode.MatchString(result.GetStableErrorCode()) || result.GetAcceptedAuthorizationRevision() > 1 {
			return domain.ID{}, errors.New("rejected OpenTunnelResult is invalid")
		}
		_, err = server.persistence.UpdateSession(ctx, session.ID, func(value *domain.Session) error {
			return value.Fail(now)
		})
	default:
		return domain.ID{}, errors.New("OpenTunnelResult status is invalid")
	}
	if err != nil {
		return domain.ID{}, err
	}
	return receiverID, nil
}

func (server *Server) processSessionKeyPackage(
	ctx context.Context,
	endpoint domain.Endpoint,
	credential domain.EndpointCredential,
	control *awpv1.ControlFrame,
	now time.Time,
) (domain.ID, error) {
	message := new(awpv1.SessionKeyPackage)
	if err := proto.Unmarshal(control.GetPayload(), message); err != nil {
		return domain.ID{}, errors.New("SessionKeyPackage payload is invalid")
	}
	packageID, err := idFromWire(message.GetKeyPackageId())
	if err != nil {
		return domain.ID{}, err
	}
	sessionID, err := idFromWire(message.GetSessionId())
	if err != nil {
		return domain.ID{}, err
	}
	issuerID, err := idFromWire(message.GetIssuerAbaEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	receiverID, err := idFromWire(message.GetRecipientHcEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	credentialID, err := idFromWire(message.GetIssuerCredentialId())
	if err != nil {
		return domain.ID{}, err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return domain.ID{}, err
	}
	if issuerID != endpoint.ID || credentialID != credential.ID || credential.EndpointID != endpoint.ID ||
		receiverID != session.HCEndpointID || !bytes.Equal(control.GetReceiverEndpointId(), receiverID[:]) ||
		session.ABAEndpointID != endpoint.ID || session.Status != domain.SessionStatusWaitingKey ||
		message.GetKeyGeneration() != 1 || message.GetCryptoSuite() != keyPackageSuiteName ||
		message.GetPolicyRevision() != 1 || len(message.GetHpkeEnc()) != 65 ||
		len(message.GetHpkeCiphertext()) != 173 || len(message.GetIssuerSignature()) != 64 {
		return domain.ID{}, errors.New("SessionKeyPackage binding is invalid")
	}
	notBefore := time.UnixMilli(message.GetNotBeforeMs()).UTC()
	expiresAt := time.UnixMilli(message.GetExpiresAtMs()).UTC()
	if notBefore.Before(now.Add(-time.Minute)) || notBefore.After(now.Add(time.Minute)) ||
		!expiresAt.After(notBefore) || expiresAt.After(notBefore.Add(24*time.Hour)) {
		return domain.ID{}, errors.New("SessionKeyPackage time is invalid")
	}
	envelope, err := keyPackageEnvelopeTranscript(
		packageID[:], sessionID[:], message.GetKeyGeneration(), issuerID[:], receiverID[:], credentialID[:],
		message.GetPolicyRevision(), message.GetNotBeforeMs(), message.GetExpiresAtMs(),
		message.GetHpkeEnc(), message.GetHpkeCiphertext(),
	)
	if err != nil {
		return domain.ID{}, err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return domain.ID{}, err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, envelope, message.GetIssuerSignature()) {
		return domain.ID{}, errors.New("SessionKeyPackage issuer signature is invalid")
	}
	info, err := keyPackageInfo(
		sessionID[:], message.GetKeyGeneration(), issuerID[:], receiverID[:], message.GetPolicyRevision(),
	)
	if err != nil {
		return domain.ID{}, err
	}
	value := domain.SessionKeyPackage{
		ID: packageID, SessionID: sessionID, Generation: message.GetKeyGeneration(),
		IssuerABAEndpointID: issuerID, RecipientHCEndpointID: receiverID,
		CryptoSuite: keyPackageSuiteID, EncapsulatedKey: bytes.Clone(message.GetHpkeEnc()),
		Ciphertext: bytes.Clone(message.GetHpkeCiphertext()), ContextHash: sha256.Sum256(info),
		IssuerSignature: bytes.Clone(message.GetIssuerSignature()), IssuerCredentialID: credentialID,
		Status: domain.KeyPackageStatusPending, ExpiresAt: expiresAt, CreatedAt: notBefore,
	}
	if _, _, err := server.persistence.PutEndpointSessionKeyPackage(
		ctx, session.OwnerUserID, session.TenantID, value,
	); err != nil {
		return domain.ID{}, err
	}
	return receiverID, nil
}

func (server *Server) processSessionKeyPackageACK(
	ctx context.Context,
	endpoint domain.Endpoint,
	control *awpv1.ControlFrame,
	now time.Time,
) (domain.ID, error) {
	message := new(awpv1.SessionKeyPackageAck)
	if err := proto.Unmarshal(control.GetPayload(), message); err != nil {
		return domain.ID{}, errors.New("SessionKeyPackageAck payload is invalid")
	}
	sessionID, err := idFromWire(message.GetSessionId())
	if err != nil {
		return domain.ID{}, err
	}
	packageID, err := idFromWire(message.GetKeyPackageId())
	if err != nil {
		return domain.ID{}, err
	}
	recipientID, err := idFromWire(message.GetRecipientHcEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	abaID, err := idFromWire(control.GetReceiverEndpointId())
	if err != nil {
		return domain.ID{}, err
	}
	session, err := server.persistence.GetSession(ctx, sessionID)
	if err != nil {
		return domain.ID{}, err
	}
	if recipientID != endpoint.ID || session.HCEndpointID != endpoint.ID || session.ABAEndpointID != abaID ||
		message.GetKeyGeneration() != 1 || message.GetAcknowledgedAtMs() != control.GetCreatedAtMs() {
		return domain.ID{}, errors.New("SessionKeyPackageAck binding is invalid")
	}
	if _, err := server.persistence.AcknowledgeAndActivateSessionKeyPackage(
		ctx, packageID, endpoint.ID, session.OwnerUserID, session.TenantID, now,
	); err != nil {
		return domain.ID{}, err
	}
	return abaID, nil
}

func idFromWire(value []byte) (domain.ID, error) {
	if len(value) != 16 {
		return domain.ID{}, errors.New("wire identifier length is invalid")
	}
	var id domain.ID
	copy(id[:], value)
	if id.IsZero() {
		return domain.ID{}, errors.New("wire identifier is zero")
	}
	return id, nil
}

func capabilitySubset(values, allowed []string) bool {
	if len(values) > len(allowed) {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
