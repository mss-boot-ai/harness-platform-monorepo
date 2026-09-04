package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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
	time.Sleep(10 * time.Millisecond)

	listChallenge := httptest.NewRecorder()
	handler.ServeHTTP(listChallenge, gatewayABAListRequest(hcAccessToken, ""))
	listNonce := listChallenge.Header().Get("DPoP-Nonce")
	if listChallenge.Code != http.StatusUnauthorized || listNonce == "" {
		t.Fatalf("ABA list nonce status=%d headers=%v body=%s", listChallenge.Code, listChallenge.Header(), listChallenge.Body.String())
	}
	listProof := signDPoPForMethodPath(
		t, hcSigningKey, hcPublicJWK, hcAccessToken, listNonce, now,
		"00000000-0000-4000-8000-000000000400", "GET", "/gateway/v1/endpoints/abas",
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
		open.GetWorkspaceId() != "fixture" || open.GetAuthorizationRevision() != 1 || open.GetRequestedKeyGeneration() != 1 {
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

	replayed := perform("00000000-0000-4000-8000-000000000402")
	if replayed.Code != http.StatusCreated || replayed.Header().Get("Idempotency-Replayed") != "true" ||
		!bytes.Equal(replayed.Body.Bytes(), created.Body.Bytes()) {
		t.Fatalf("replay status=%d headers=%v body=%s", replayed.Code, replayed.Header(), replayed.Body.String())
	}
	_ = abaConnection.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := abaConnection.ReadMessage(); err == nil {
		t.Fatal("idempotent session replay delivered a duplicate OpenTunnel control")
	}
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

func gatewayABAListRequest(accessToken, proof string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/gateway/v1/endpoints/abas", nil)
	request.Header.Set("Origin", "http://127.0.0.1:8001")
	request.Header.Set("Authorization", "DPoP "+accessToken)
	if proof != "" {
		request.Header.Set("DPoP", proof)
	}
	return request
}
