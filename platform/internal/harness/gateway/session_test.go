package gateway

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

func TestHCSessionCreateIsIdempotentAndDeliversSignedOpenTunnel(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, abaEndpoint, abaCredential, _, _, abaSigningKey, _ := abaGatewayFixture(t, now)
	hcEndpointID := gatewayID(1)
	hcAccessToken := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	hcPublicJWK, hcSigningKey := gatewaySigningKey(1)
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082", Trust: trust,
	}, persistence, deterministicGatewayBytes(8192), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ticketRaw := bytes.Repeat([]byte{83}, 32)
	ticketValue := base64.RawURLEncoding.EncodeToString(ticketRaw)
	ticket := domain.WSTicket{
		ID: gatewayID(83), TokenHash: sha256.Sum256(ticketRaw), EndpointID: abaEndpoint.ID, CredentialID: abaCredential.ID,
		Purpose: ticketPurpose, Origin: nativeABAOrigin, Protocol: protocolName,
		Status: domain.TicketStatusIssued, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now,
	}
	if err := persistence.CreateTicket(context.Background(), ticket, 30*time.Second); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/gateway/v1/ws"
	dialer := websocket.Dialer{Subprotocols: []string{protocolName, "mss.ticket." + ticketValue}}
	abaConnection, response, err := dialer.Dial(websocketURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial ABA WebSocket: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial ABA WebSocket: %v", err)
	}
	defer abaConnection.Close()
	completeClientChallenge(t, abaConnection, abaEndpoint, abaCredential, abaSigningKey)
	hcEndpoint, hcCredential, err := persistence.GetEndpointCredential(
		t.Context(), hcEndpointID, gatewayID(3), now,
	)
	if err != nil {
		t.Fatalf("GetEndpointCredential HC: %v", err)
	}
	hcTicketRaw := bytes.Repeat([]byte{91}, 32)
	hcTicketValue := base64.RawURLEncoding.EncodeToString(hcTicketRaw)
	if err := persistence.CreateTicket(t.Context(), domain.WSTicket{
		ID: gatewayID(91), TokenHash: sha256.Sum256(hcTicketRaw), EndpointID: hcEndpoint.ID, CredentialID: hcCredential.ID,
		Purpose: ticketPurpose, Origin: "http://127.0.0.1:8001", Protocol: protocolName,
		Status: domain.TicketStatusIssued, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now,
	}, 30*time.Second); err != nil {
		t.Fatalf("CreateTicket HC: %v", err)
	}
	hcDialer := websocket.Dialer{Subprotocols: []string{protocolName, "mss.ticket." + hcTicketValue}}
	hcConnection, response, err := hcDialer.Dial(
		websocketURL, http.Header{"Origin": []string{"http://127.0.0.1:8001"}},
	)
	if err != nil {
		if response != nil {
			t.Fatalf("dial HC WebSocket: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial HC WebSocket: %v", err)
	}
	defer hcConnection.Close()
	completeClientChallenge(t, hcConnection, hcEndpoint, hcCredential, hcSigningKey)
	time.Sleep(10 * time.Millisecond)

	listChallenge := httptest.NewRecorder()
	handler.ServeHTTP(listChallenge, gatewayABAListRequest(hcAccessToken, ""))
	listNonce := listChallenge.Header().Get("DPoP-Nonce")
	if listChallenge.Code != http.StatusUnauthorized || listNonce == "" {
		t.Fatalf("ABA list nonce status=%d headers=%v body=%s", listChallenge.Code, listChallenge.Header(), listChallenge.Body.String())
	}
	listProof := signDPoPForMethodPath(
		t, hcSigningKey, hcPublicJWK, hcAccessToken, listNonce, now,
		"00000000-0000-4000-8000-000000000400", "POST", "/gateway/v1/endpoints/abas",
	)
	listed := httptest.NewRecorder()
	handler.ServeHTTP(listed, gatewayABAListRequest(hcAccessToken, listProof))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), abaEndpoint.ID.String()) ||
		strings.Contains(listed.Body.String(), hcEndpointID.String()) {
		t.Fatalf("online ABA list status=%d body=%s", listed.Code, listed.Body.String())
	}

	requestBody, _ := json.Marshal(map[string]any{
		"abaEndpointId": abaEndpoint.ID.String(), "runtimeProfileId": "test-agent",
		"workspaceId": "fixture", "requestedCapabilities": []string{"session", "prompt"},
	})
	perform := func(jti string) *httptest.ResponseRecorder {
		t.Helper()
		challengeRequest := gatewaySessionRequest(hcAccessToken, "session-create-key-0001", requestBody, "")
		challengeResponse := httptest.NewRecorder()
		handler.ServeHTTP(challengeResponse, challengeRequest)
		nonce := challengeResponse.Header().Get("DPoP-Nonce")
		if challengeResponse.Code != http.StatusUnauthorized || nonce == "" {
			t.Fatalf("session nonce status=%d headers=%v body=%s", challengeResponse.Code, challengeResponse.Header(), challengeResponse.Body.String())
		}
		proof := signDPoPForPath(
			t, hcSigningKey, hcPublicJWK, hcAccessToken, nonce, now, jti, "/gateway/v1/sessions",
		)
		result := httptest.NewRecorder()
		handler.ServeHTTP(result, gatewaySessionRequest(hcAccessToken, "session-create-key-0001", requestBody, proof))
		return result
	}

	created := perform("00000000-0000-4000-8000-000000000401")
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", created.Code, created.Body.String())
	}
	var createdBody endpointSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &createdBody); err != nil {
		t.Fatalf("decode session response: %v", err)
	}
	if createdBody.ABAEndpointID != abaEndpoint.ID.String() || createdBody.HCEndpointID != hcEndpointID.String() ||
		createdBody.Status != domain.SessionStatusCreating {
		t.Fatalf("created session=%#v", createdBody)
	}

	messageType, encoded, err := abaConnection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage {
		t.Fatalf("read OpenTunnel type=%d error=%v", messageType, err)
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, packet); err != nil {
		t.Fatalf("decode OpenTunnel packet: %v", err)
	}
	control := packet.GetControl()
	if packet.GetWireMajor() != 1 || len(packet.GetPacketId()) != 16 || control == nil ||
		control.GetType() != awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_REQUEST ||
		len(control.GetMessageId()) != 16 || len(control.GetSenderEndpointId()) != 16 ||
		!bytes.Equal(control.GetSenderEndpointId(), make([]byte, 16)) ||
		!bytes.Equal(control.GetReceiverEndpointId(), abaEndpoint.ID[:]) || control.GetControlSequence() != 1 {
		t.Fatalf("invalid OpenTunnel control=%#v", control)
	}
	transcript, err := controlTranscript(
		control.GetMessageId(), control.GetSenderEndpointId(), control.GetReceiverEndpointId(),
		control.GetControlSequence(), control.GetCreatedAtMs(), uint32(control.GetType()), control.GetPayload(),
	)
	if err != nil || !awpcrypto.VerifyP1363LowS(&trust.Online.PublicKey, transcript, control.GetSignature()) {
		t.Fatalf("OpenTunnel signature error=%v", err)
	}
	open := new(awpv1.OpenTunnelRequest)
	if err := proto.Unmarshal(control.GetPayload(), open); err != nil {
		t.Fatalf("decode OpenTunnel payload: %v", err)
	}
	createdSessionID, err := domain.ParseID(createdBody.SessionID)
	if err != nil || !bytes.Equal(open.GetSessionId(), createdSessionID[:]) ||
		!bytes.Equal(open.GetHcEndpointId(), hcEndpointID[:]) || open.GetRuntimeProfileId() != "test-agent" ||
		open.GetWorkspaceId() != "fixture" || open.GetAuthorizationRevision() != 1 || open.GetRequestedKeyGeneration() != 1 ||
		len(open.GetHcKemPublicKey()) != 65 || open.GetHcKemJkt() == "" ||
		len(open.GetHcSigningPublicKey()) != 65 || open.GetHcSigningJkt() == "" {
		t.Fatalf("OpenTunnel payload=%#v error=%v", open, err)
	}

	resultPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.OpenTunnelResult{
		SessionId: createdSessionID[:], Status: awpv1.OpenTunnelStatus_OPEN_TUNNEL_STATUS_ACCEPTED,
		AcceptedAuthorizationRevision: 1, NegotiatedCapabilityHints: []string{"prompt", "session"},
	})
	if err != nil {
		t.Fatalf("encode OpenTunnelResult: %v", err)
	}
	resultMessageID := bytes.Repeat([]byte{84}, 16)
	resultTranscript, err := controlTranscript(
		resultMessageID, abaEndpoint.ID[:], hcEndpointID[:], 1, now.UnixMilli(),
		uint32(awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_RESULT), resultPayload,
	)
	if err != nil {
		t.Fatalf("OpenTunnelResult transcript: %v", err)
	}
	resultSignature, err := awpcrypto.SignP1363LowS(abaSigningKey, resultTranscript)
	if err != nil {
		t.Fatalf("sign OpenTunnelResult: %v", err)
	}
	resultPacket := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{85}, 16),
		Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
			MessageId: resultMessageID, SenderEndpointId: abaEndpoint.ID[:], ReceiverEndpointId: hcEndpointID[:],
			ControlSequence: 1, CreatedAtMs: now.UnixMilli(), Type: awpv1.ControlType_CONTROL_TYPE_OPEN_TUNNEL_RESULT,
			Payload: resultPayload, Signature: resultSignature,
		}},
	}
	encodedResult, err := proto.MarshalOptions{Deterministic: true}.Marshal(resultPacket)
	if err != nil {
		t.Fatalf("encode OpenTunnelResult packet: %v", err)
	}
	if err := abaConnection.WriteMessage(websocket.BinaryMessage, encodedResult); err != nil {
		t.Fatalf("write OpenTunnelResult: %v", err)
	}
	var stored domain.Session
	for attempt := 0; attempt < 20; attempt++ {
		stored, err = persistence.GetSession(t.Context(), createdSessionID)
		if err == nil && stored.Status == domain.SessionStatusWaitingKey {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || stored.Status != domain.SessionStatusWaitingKey {
		t.Fatalf("session after accepted OpenTunnel=%#v error=%v", stored, err)
	}
	if forwardedType, forwarded, err := hcConnection.ReadMessage(); err != nil || forwardedType != websocket.BinaryMessage {
		t.Fatalf("read forwarded OpenTunnelResult type=%d error=%v bytes=%d", forwardedType, err, len(forwarded))
	}
	keyPackageID := gatewayID(86)
	packageInfo, err := keyPackageInfo(
		createdSessionID[:], 1, abaEndpoint.ID[:], hcEndpointID[:], 1,
	)
	if err != nil {
		t.Fatalf("keyPackageInfo: %v", err)
	}
	hcKEMPublic, err := ecdh.P256().NewPublicKey(open.GetHcKemPublicKey())
	if err != nil {
		t.Fatalf("parse HC KEM public key: %v", err)
	}
	hpkePublic, err := hpke.NewDHKEMPublicKey(hcKEMPublic)
	if err != nil {
		t.Fatalf("wrap HC KEM public key: %v", err)
	}
	enc, sender, err := hpke.NewSender(hpkePublic, hpke.HKDFSHA256(), hpke.AES256GCM(), packageInfo)
	if err != nil {
		t.Fatalf("create HPKE sender: %v", err)
	}
	plaintext := testKeyPackagePlaintext(createdSessionID, now)
	ciphertext, err := sender.Seal(packageInfo, plaintext)
	if err != nil {
		t.Fatalf("seal key package: %v", err)
	}
	envelope, err := keyPackageEnvelopeTranscript(
		keyPackageID[:], createdSessionID[:], 1, abaEndpoint.ID[:], hcEndpointID[:], abaCredential.ID[:],
		1, now.UnixMilli(), now.Add(time.Hour).UnixMilli(), enc, ciphertext,
	)
	if err != nil {
		t.Fatalf("keyPackageEnvelopeTranscript: %v", err)
	}
	issuerSignature, err := awpcrypto.SignP1363LowS(abaSigningKey, envelope)
	if err != nil {
		t.Fatalf("sign key package: %v", err)
	}
	packagePayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.SessionKeyPackage{
		SessionId: createdSessionID[:], KeyGeneration: 1, IssuerAbaEndpointId: abaEndpoint.ID[:],
		RecipientHcEndpointId: hcEndpointID[:], CryptoSuite: keyPackageSuiteName,
		HpkeEnc: enc, HpkeCiphertext: ciphertext, NotBeforeMs: now.UnixMilli(),
		ExpiresAtMs: now.Add(time.Hour).UnixMilli(), IssuerSignature: issuerSignature,
		KeyPackageId: keyPackageID[:], IssuerCredentialId: abaCredential.ID[:], PolicyRevision: 1,
	})
	if err != nil {
		t.Fatalf("encode SessionKeyPackage: %v", err)
	}
	packageMessageID := bytes.Repeat([]byte{87}, 16)
	packageControlTranscript, err := controlTranscript(
		packageMessageID, abaEndpoint.ID[:], hcEndpointID[:], 2, now.UnixMilli(),
		uint32(awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE), packagePayload,
	)
	if err != nil {
		t.Fatalf("SessionKeyPackage control transcript: %v", err)
	}
	packageControlSignature, err := awpcrypto.SignP1363LowS(abaSigningKey, packageControlTranscript)
	if err != nil {
		t.Fatalf("sign SessionKeyPackage control: %v", err)
	}
	packagePacket := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{88}, 16),
		Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
			MessageId: packageMessageID, SenderEndpointId: abaEndpoint.ID[:], ReceiverEndpointId: hcEndpointID[:],
			ControlSequence: 2, CreatedAtMs: now.UnixMilli(), Type: awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE,
			Payload: packagePayload, Signature: packageControlSignature,
		}},
	}
	encodedPackage, err := proto.MarshalOptions{Deterministic: true}.Marshal(packagePacket)
	if err != nil {
		t.Fatalf("encode SessionKeyPackage packet: %v", err)
	}
	if err := abaConnection.WriteMessage(websocket.BinaryMessage, encodedPackage); err != nil {
		t.Fatalf("write SessionKeyPackage: %v", err)
	}
	var packages []domain.SessionKeyPackage
	for attempt := 0; attempt < 20; attempt++ {
		packages, err = persistence.ListSessionKeyPackages(
			t.Context(), createdSessionID, abaEndpoint.OwnerUserID, abaEndpoint.TenantID, 10,
		)
		if err == nil && len(packages) == 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || len(packages) != 1 || packages[0].ID != keyPackageID ||
		!bytes.Equal(packages[0].Ciphertext, ciphertext) {
		t.Fatalf("stored key packages=%#v error=%v", packages, err)
	}
	forwardedType, forwardedPackage, err := hcConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedPackage, encodedPackage) {
		t.Fatalf("read forwarded SessionKeyPackage type=%d error=%v", forwardedType, err)
	}
	ackPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.SessionKeyPackageAck{
		SessionId: createdSessionID[:], KeyGeneration: 1, KeyPackageId: keyPackageID[:],
		RecipientHcEndpointId: hcEndpointID[:], AcknowledgedAtMs: now.UnixMilli(),
	})
	if err != nil {
		t.Fatalf("encode SessionKeyPackageAck: %v", err)
	}
	ackMessageID := bytes.Repeat([]byte{92}, 16)
	ackTranscript, err := controlTranscript(
		ackMessageID, hcEndpointID[:], abaEndpoint.ID[:], 1, now.UnixMilli(),
		uint32(awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE_ACK), ackPayload,
	)
	if err != nil {
		t.Fatalf("SessionKeyPackageAck transcript: %v", err)
	}
	ackSignature, err := awpcrypto.SignP1363LowS(hcSigningKey, ackTranscript)
	if err != nil {
		t.Fatalf("sign SessionKeyPackageAck: %v", err)
	}
	ackPacket := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{93}, 16),
		Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
			MessageId: ackMessageID, SenderEndpointId: hcEndpointID[:], ReceiverEndpointId: abaEndpoint.ID[:],
			ControlSequence: 1, CreatedAtMs: now.UnixMilli(), Type: awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE_ACK,
			Payload: ackPayload, Signature: ackSignature,
		}},
	}
	encodedACK, err := proto.MarshalOptions{Deterministic: true}.Marshal(ackPacket)
	if err != nil {
		t.Fatalf("encode SessionKeyPackageAck packet: %v", err)
	}
	if err := hcConnection.WriteMessage(websocket.BinaryMessage, encodedACK); err != nil {
		t.Fatalf("write SessionKeyPackageAck: %v", err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		stored, err = persistence.GetSession(t.Context(), createdSessionID)
		packages, _ = persistence.ListSessionKeyPackages(
			t.Context(), createdSessionID, abaEndpoint.OwnerUserID, abaEndpoint.TenantID, 10,
		)
		if err == nil && stored.Status == domain.SessionStatusActive && len(packages) == 1 &&
			packages[0].Status == domain.KeyPackageStatusAcknowledged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil || stored.Status != domain.SessionStatusActive || stored.CurrentKeyGeneration != 1 ||
		len(packages) != 1 || packages[0].Status != domain.KeyPackageStatusAcknowledged {
		t.Fatalf("activated session=%#v packages=%#v error=%v", stored, packages, err)
	}
	forwardedType, forwardedACK, err := abaConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedACK, encodedACK) {
		t.Fatalf("read forwarded SessionKeyPackageAck type=%d error=%v", forwardedType, err)
	}
	prk, err := hkdf.Extract(sha256.New, bytes.Repeat([]byte{4}, 32), bytes.Repeat([]byte{5}, 32))
	if err != nil {
		t.Fatalf("extract Session PRK: %v", err)
	}
	directionPrefix := "mss-awp/v1/session/" + createdSessionID.String() + "/generation/1/endpoint/" + hcEndpointID.String()
	hcToABAKey, err := hkdf.Expand(sha256.New, prk, directionPrefix+"/hc-to-aba", 32)
	if err != nil {
		t.Fatalf("derive HC to ABA key: %v", err)
	}
	abaToHCKey, err := hkdf.Expand(sha256.New, prk, directionPrefix+"/aba-to-hc", 32)
	if err != nil {
		t.Fatalf("derive ABA to HC key: %v", err)
	}
	channelID, err := sessionChannelID(createdSessionID, abaEndpoint.ID, hcEndpointID)
	if err != nil {
		t.Fatalf("sessionChannelID: %v", err)
	}
	hcFrame := testEncryptedFramePacket(
		t, createdSessionID, channelID, hcEndpointID, abaEndpoint.ID, gatewayID(8),
		awpv1.Direction_DIRECTION_HC_TO_ABA, hcToABAKey, []byte{6, 6, 6, 6}, hcSigningKey,
		1, []byte(`{"jsonrpc":"2.0","id":1,"method":"session/prompt","params":{"sessionId":"`+createdSessionID.String()+`","prompt":[{"type":"text","text":"canary"}]}}`), now,
	)
	if err := hcConnection.WriteMessage(websocket.BinaryMessage, hcFrame); err != nil {
		t.Fatalf("write HC encrypted frame: %v", err)
	}
	forwardedType, forwardedHCFrame, err := abaConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedHCFrame, hcFrame) {
		t.Fatalf("read forwarded HC frame type=%d error=%v", forwardedType, err)
	}
	abaACK := testAckFramePacket(
		t, createdSessionID, channelID, abaEndpoint.ID,
		awpv1.Direction_DIRECTION_HC_TO_ABA, 1, abaSigningKey, now,
	)
	if err := abaConnection.WriteMessage(websocket.BinaryMessage, abaACK); err != nil {
		t.Fatalf("write ABA ACK: %v", err)
	}
	forwardedType, forwardedABAACK, err := hcConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedABAACK, abaACK) {
		t.Fatalf("read forwarded ABA ACK type=%d error=%v", forwardedType, err)
	}
	abaFrame := testEncryptedFramePacket(
		t, createdSessionID, channelID, abaEndpoint.ID, hcEndpointID, gatewayID(8),
		awpv1.Direction_DIRECTION_ABA_TO_HC, abaToHCKey, []byte{7, 7, 7, 7}, abaSigningKey,
		1, []byte(`{"jsonrpc":"2.0","id":1,"result":{"stopReason":"end_turn"}}`), now,
	)
	if err := abaConnection.WriteMessage(websocket.BinaryMessage, abaFrame); err != nil {
		t.Fatalf("write ABA encrypted frame: %v", err)
	}
	forwardedType, forwardedABAFrame, err := hcConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedABAFrame, abaFrame) {
		t.Fatalf("read forwarded ABA frame type=%d error=%v", forwardedType, err)
	}
	resumePayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.ResumeState{
		Cursors: []*awpv1.ChannelCursor{{
			ChannelId: channelID[:], Direction: awpv1.Direction_DIRECTION_ABA_TO_HC,
			HighestContiguousSequence: 0, KeyGeneration: 1,
		}},
	})
	if err != nil {
		t.Fatalf("encode ResumeState: %v", err)
	}
	resumeMessageID := bytes.Repeat([]byte{97}, 16)
	platformReceiver := make([]byte, 16)
	resumeTranscript, err := controlTranscript(
		resumeMessageID, hcEndpointID[:], platformReceiver, 2, now.UnixMilli(),
		uint32(awpv1.ControlType_CONTROL_TYPE_RESUME_STATE), resumePayload,
	)
	if err != nil {
		t.Fatalf("ResumeState transcript: %v", err)
	}
	resumeSignature, err := awpcrypto.SignP1363LowS(hcSigningKey, resumeTranscript)
	if err != nil {
		t.Fatalf("sign ResumeState: %v", err)
	}
	resumePacket, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{98}, 16),
		Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
			MessageId: resumeMessageID, SenderEndpointId: hcEndpointID[:], ReceiverEndpointId: platformReceiver,
			ControlSequence: 2, CreatedAtMs: now.UnixMilli(), Type: awpv1.ControlType_CONTROL_TYPE_RESUME_STATE,
			Payload: resumePayload, Signature: resumeSignature,
		}},
	})
	if err != nil {
		t.Fatalf("encode ResumeState packet: %v", err)
	}
	if err := hcConnection.WriteMessage(websocket.BinaryMessage, resumePacket); err != nil {
		t.Fatalf("write ResumeState: %v", err)
	}
	replayType, replayedABAFrame, err := hcConnection.ReadMessage()
	if err != nil || replayType != websocket.BinaryMessage {
		t.Fatalf("read replayed ABA frame type=%d error=%v", replayType, err)
	}
	replayedPacket := new(awpv1.WirePacket)
	originalPacket := new(awpv1.WirePacket)
	if err := proto.Unmarshal(replayedABAFrame, replayedPacket); err != nil {
		t.Fatalf("decode replayed ABA frame: %v", err)
	}
	if err := proto.Unmarshal(abaFrame, originalPacket); err != nil {
		t.Fatalf("decode original ABA frame: %v", err)
	}
	if !proto.Equal(replayedPacket.GetEncrypted(), originalPacket.GetEncrypted()) {
		t.Fatalf("replayed encrypted frame changed: original=%#v replay=%#v", originalPacket.GetEncrypted(), replayedPacket.GetEncrypted())
	}
	hcACK := testAckFramePacket(
		t, createdSessionID, channelID, hcEndpointID,
		awpv1.Direction_DIRECTION_ABA_TO_HC, 1, hcSigningKey, now,
	)
	if err := hcConnection.WriteMessage(websocket.BinaryMessage, hcACK); err != nil {
		t.Fatalf("write HC ACK: %v", err)
	}
	forwardedType, forwardedHCACK, err := abaConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedHCACK, hcACK) {
		t.Fatalf("read forwarded HC ACK type=%d error=%v", forwardedType, err)
	}
	for attempt := 0; attempt < 20; attempt++ {
		delivery, err := persistence.Delivery(
			t.Context(), createdSessionID, abaEndpoint.OwnerUserID, abaEndpoint.TenantID, 10,
		)
		if err == nil && len(delivery.Frames) == 2 &&
			delivery.Frames[0].Status == domain.FrameStatusReceiverAcknowledged &&
			delivery.Frames[1].Status == domain.FrameStatusReceiverAcknowledged {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	delivery, err := persistence.Delivery(
		t.Context(), createdSessionID, abaEndpoint.OwnerUserID, abaEndpoint.TenantID, 10,
	)
	if err != nil || len(delivery.Frames) != 2 ||
		delivery.Frames[0].Status != domain.FrameStatusReceiverAcknowledged ||
		delivery.Frames[1].Status != domain.FrameStatusReceiverAcknowledged {
		t.Fatalf("encrypted delivery frames=%#v error=%v", delivery.Frames, err)
	}

	replayed := perform("00000000-0000-4000-8000-000000000402")
	if replayed.Code != http.StatusCreated || replayed.Header().Get("Idempotency-Replayed") != "true" ||
		!bytes.Equal(replayed.Body.Bytes(), created.Body.Bytes()) {
		t.Fatalf("replay status=%d headers=%v body=%s", replayed.Code, replayed.Header(), replayed.Body.String())
	}
	closePath := "/gateway/v1/sessions/" + createdSessionID.String() + "/close"
	closeChallenge := httptest.NewRecorder()
	handler.ServeHTTP(closeChallenge, gatewaySessionCloseRequest(hcAccessToken, "session-close-key-0001", closePath, ""))
	closeNonce := closeChallenge.Header().Get("DPoP-Nonce")
	if closeChallenge.Code != http.StatusUnauthorized || closeNonce == "" {
		t.Fatalf("session close nonce status=%d body=%s", closeChallenge.Code, closeChallenge.Body.String())
	}
	closeProof := signDPoPForMethodPath(
		t, hcSigningKey, hcPublicJWK, hcAccessToken, closeNonce, now,
		"00000000-0000-4000-8000-000000000403", http.MethodPost, closePath,
	)
	closedResponse := httptest.NewRecorder()
	handler.ServeHTTP(
		closedResponse,
		gatewaySessionCloseRequest(hcAccessToken, "session-close-key-0001", closePath, closeProof),
	)
	if closedResponse.Code != http.StatusOK || !strings.Contains(closedResponse.Body.String(), `"status":"CLOSED"`) {
		t.Fatalf("close session status=%d body=%s", closedResponse.Code, closedResponse.Body.String())
	}
	closeType, encodedClose, err := abaConnection.ReadMessage()
	if err != nil || closeType != websocket.BinaryMessage {
		t.Fatalf("read CloseTunnelRequest type=%d error=%v", closeType, err)
	}
	closePacket := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encodedClose, closePacket); err != nil {
		t.Fatalf("decode CloseTunnelRequest: %v", err)
	}
	closeControl := closePacket.GetControl()
	if closeControl == nil || closeControl.GetType() != awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_REQUEST ||
		closeControl.GetControlSequence() != 2 {
		t.Fatalf("CloseTunnelRequest control=%#v", closeControl)
	}
	closeRequest := new(awpv1.CloseTunnelRequest)
	if err := proto.Unmarshal(closeControl.GetPayload(), closeRequest); err != nil ||
		!bytes.Equal(closeRequest.GetSessionId(), createdSessionID[:]) || closeRequest.GetStableReasonCode() != "HC_REQUESTED" {
		t.Fatalf("CloseTunnelRequest payload=%#v error=%v", closeRequest, err)
	}
	closeResultPayload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.CloseTunnelResult{
		SessionId: createdSessionID[:], Status: awpv1.CloseTunnelStatus_CLOSE_TUNNEL_STATUS_ACCEPTED,
	})
	if err != nil {
		t.Fatalf("encode CloseTunnelResult: %v", err)
	}
	closeResultID := bytes.Repeat([]byte{95}, 16)
	closeResultTranscript, err := controlTranscript(
		closeResultID, abaEndpoint.ID[:], hcEndpointID[:], 3, now.UnixMilli(),
		uint32(awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_RESULT), closeResultPayload,
	)
	if err != nil {
		t.Fatalf("CloseTunnelResult transcript: %v", err)
	}
	closeResultSignature, err := awpcrypto.SignP1363LowS(abaSigningKey, closeResultTranscript)
	if err != nil {
		t.Fatalf("sign CloseTunnelResult: %v", err)
	}
	closeResultPacket := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{96}, 16),
		Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{
			MessageId: closeResultID, SenderEndpointId: abaEndpoint.ID[:], ReceiverEndpointId: hcEndpointID[:],
			ControlSequence: 3, CreatedAtMs: now.UnixMilli(), Type: awpv1.ControlType_CONTROL_TYPE_CLOSE_TUNNEL_RESULT,
			Payload: closeResultPayload, Signature: closeResultSignature,
		}},
	}
	encodedCloseResult, err := proto.MarshalOptions{Deterministic: true}.Marshal(closeResultPacket)
	if err != nil {
		t.Fatalf("encode CloseTunnelResult packet: %v", err)
	}
	if err := abaConnection.WriteMessage(websocket.BinaryMessage, encodedCloseResult); err != nil {
		t.Fatalf("write CloseTunnelResult: %v", err)
	}
	forwardedType, forwardedCloseResult, err := hcConnection.ReadMessage()
	if err != nil || forwardedType != websocket.BinaryMessage || !bytes.Equal(forwardedCloseResult, encodedCloseResult) {
		t.Fatalf("read forwarded CloseTunnelResult type=%d error=%v", forwardedType, err)
	}
	replayCloseChallenge := httptest.NewRecorder()
	handler.ServeHTTP(
		replayCloseChallenge,
		gatewaySessionCloseRequest(hcAccessToken, "session-close-key-0001", closePath, ""),
	)
	replayCloseNonce := replayCloseChallenge.Header().Get("DPoP-Nonce")
	if replayCloseChallenge.Code != http.StatusUnauthorized || replayCloseNonce == "" {
		t.Fatalf("replay close nonce status=%d body=%s", replayCloseChallenge.Code, replayCloseChallenge.Body.String())
	}
	replayCloseProof := signDPoPForMethodPath(
		t, hcSigningKey, hcPublicJWK, hcAccessToken, replayCloseNonce, now,
		"00000000-0000-4000-8000-000000000404", http.MethodPost, closePath,
	)
	replayedClose := httptest.NewRecorder()
	handler.ServeHTTP(
		replayedClose,
		gatewaySessionCloseRequest(hcAccessToken, "session-close-key-0001", closePath, replayCloseProof),
	)
	if replayedClose.Code != http.StatusOK || replayedClose.Header().Get("Idempotency-Replayed") != "true" ||
		!bytes.Equal(replayedClose.Body.Bytes(), closedResponse.Body.Bytes()) {
		t.Fatalf("replayed close status=%d headers=%v body=%s", replayedClose.Code, replayedClose.Header(), replayedClose.Body.String())
	}
	if _, err := persistence.RevokeEndpointForOwner(
		t.Context(), hcEndpoint.ID, hcEndpoint.OwnerUserID, hcEndpoint.TenantID, now.Add(time.Second),
	); err != nil {
		t.Fatalf("revoke connected HC endpoint: %v", err)
	}
	if err := hcConnection.WriteMessage(websocket.BinaryMessage, []byte{1}); err != nil {
		t.Fatalf("write post-revocation probe: %v", err)
	}
	_ = hcConnection.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := hcConnection.ReadMessage(); err == nil {
		t.Fatal("revoked HC WebSocket remained usable")
	}
	_ = abaConnection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := abaConnection.ReadMessage(); err == nil {
		t.Fatal("idempotent session replay delivered a duplicate OpenTunnel control")
	}
}

func testEncryptedFramePacket(
	t *testing.T,
	sessionID, channelID, senderID, receiverID, keyID domain.ID,
	direction awpv1.Direction,
	key []byte,
	noncePrefix []byte,
	signingKey *ecdsa.PrivateKey,
	sequence uint64,
	plaintext []byte,
	now time.Time,
) []byte {
	t.Helper()
	idOffset := byte(100)
	if direction == awpv1.Direction_DIRECTION_ABA_TO_HC {
		idOffset = 120
	}
	messageID := gatewayID(idOffset + byte(sequence))
	packetID := gatewayID(idOffset + 10 + byte(sequence))
	frame := &awpv1.EncryptedFrame{
		CryptoSuiteId: 1, FrameType: awpv1.FrameType_FRAME_TYPE_ACP_TRANSPORT_FRAME,
		MessageId: messageID[:], ChannelId: channelID[:], SessionId: sessionID[:],
		SenderEndpointId: senderID[:], ReceiverEndpointId: receiverID[:], Direction: direction,
		Sequence: sequence, KeyGeneration: 1, KeyId: keyID[:], CreatedAtMs: now.UnixMilli(),
		Ciphertext: make([]byte, len(plaintext)+16),
	}
	aad, err := frameAAD(frame)
	if err != nil {
		t.Fatalf("frameAAD: %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("AES key: %v", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("AES-GCM: %v", err)
	}
	nonce := make([]byte, 12)
	copy(nonce, noncePrefix)
	binary.BigEndian.PutUint64(nonce[4:], sequence)
	frame.Ciphertext = gcm.Seal(nil, nonce, plaintext, aad)
	frame.Signature, err = awpcrypto.SignP1363LowS(signingKey, frameSignatureInput(aad, frame.Ciphertext))
	if err != nil {
		t.Fatalf("sign encrypted frame: %v", err)
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.WirePacket{
		WireMajor: 1, PacketId: packetID[:],
		Body: &awpv1.WirePacket_Encrypted{Encrypted: frame},
	})
	if err != nil {
		t.Fatalf("encode encrypted frame: %v", err)
	}
	return encoded
}

func testAckFramePacket(
	t *testing.T,
	sessionID, channelID, endpointID domain.ID,
	direction awpv1.Direction,
	highest uint64,
	signingKey *ecdsa.PrivateKey,
	now time.Time,
) []byte {
	t.Helper()
	offset := byte(140)
	if direction == awpv1.Direction_DIRECTION_ABA_TO_HC {
		offset = 160
	}
	ackID := gatewayID(offset)
	packetID := gatewayID(offset + 1)
	ack := &awpv1.AckFrame{
		AckId: ackID[:], ChannelId: channelID[:], SessionId: sessionID[:], EndpointId: endpointID[:],
		AcknowledgedDirection: direction, HighestContiguousSequence: highest,
		KeyGeneration: 1, CreatedAtMs: now.UnixMilli(),
	}
	transcript, err := ackTranscript(ack)
	if err != nil {
		t.Fatalf("ACK transcript: %v", err)
	}
	ack.Signature, err = awpcrypto.SignP1363LowS(signingKey, transcript)
	if err != nil {
		t.Fatalf("sign ACK: %v", err)
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(&awpv1.WirePacket{
		WireMajor: 1, PacketId: packetID[:],
		Body: &awpv1.WirePacket_Ack{Ack: ack},
	})
	if err != nil {
		t.Fatalf("encode ACK: %v", err)
	}
	return encoded
}

func testKeyPackagePlaintext(sessionID domain.ID, now time.Time) []byte {
	var output bytes.Buffer
	output.WriteString("mss-key-package-plaintext-v1")
	output.Write(sessionID[:])
	_ = binary.Write(&output, binary.BigEndian, uint64(1))
	output.Write(bytes.Repeat([]byte{8}, 16))
	output.Write(bytes.Repeat([]byte{4}, 32))
	output.Write(bytes.Repeat([]byte{5}, 32))
	output.Write(bytes.Repeat([]byte{6}, 4))
	output.Write(bytes.Repeat([]byte{7}, 4))
	_ = binary.Write(&output, binary.BigEndian, now.UnixMilli())
	_ = binary.Write(&output, binary.BigEndian, now.Add(time.Hour).UnixMilli())
	output.WriteByte(1)
	return output.Bytes()
}

func gatewaySessionRequest(accessToken, idempotencyKey string, body []byte, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/sessions", bytes.NewReader(body))
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.Header.Set("Authorization", "DPoP "+accessToken)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Content-Type", "application/json")
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}

func gatewaySessionCloseRequest(accessToken, idempotencyKey, path, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.Header.Set("Authorization", "DPoP "+accessToken)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	request.Header.Set("Content-Type", "application/json")
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}

func gatewayABAListRequest(accessToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/gateway/v1/endpoints/abas", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.Header.Set("Authorization", "DPoP "+accessToken)
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}
