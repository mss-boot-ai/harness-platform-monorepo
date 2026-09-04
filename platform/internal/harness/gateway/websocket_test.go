package gateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
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

func TestWebSocketConsumesTicketAndCompletesSignedChallenge(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, credential, _, _, signingKey, _ := gatewayFixture(t, now)
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	ticketRaw := bytes.Repeat([]byte{77}, 32)
	ticketValue := base64.RawURLEncoding.EncodeToString(ticketRaw)
	ticket := domain.WSTicket{
		ID: gatewayID(70), TokenHash: sha256.Sum256(ticketRaw), EndpointID: endpoint.ID, CredentialID: credential.ID,
		Purpose: ticketPurpose, Origin: "http://127.0.0.1:8001", Protocol: protocolName,
		Status: domain.TicketStatusIssued, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now,
	}
	if err := persistence.CreateTicket(context.Background(), ticket, 30*time.Second); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082", Trust: trust,
	}, persistence, deterministicGatewayBytes(1024), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/gateway/v1/ws"
	dialer := websocket.Dialer{Subprotocols: []string{protocolName, "mss.ticket." + ticketValue}}
	headers := http.Header{"Origin": []string{"http://127.0.0.1:8001"}}
	connection, response, err := dialer.Dial(websocketURL, headers)
	if err != nil {
		if response != nil {
			t.Fatalf("dial WebSocket: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial WebSocket: %v", err)
	}
	defer connection.Close()
	if connection.Subprotocol() != protocolName {
		t.Fatalf("selected subprotocol=%q", connection.Subprotocol())
	}
	messageType, encoded, err := connection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage {
		t.Fatalf("read challenge type=%d error=%v", messageType, err)
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, packet); err != nil {
		t.Fatalf("decode challenge: %v", err)
	}
	challenge := packet.GetServerChallenge()
	if challenge == nil {
		t.Fatal("server did not send challenge")
	}
	serverTranscript, err := serverChallengeTranscript(
		challenge.GetConnectionId(), challenge.GetConnectionGeneration(), challenge.GetServerNonce(),
		challenge.GetServerTimeMs(), challenge.GetTrustManifestRevision(), challenge.GetCredentialStatusRevision(), endpoint.ID[:],
	)
	if err != nil {
		t.Fatalf("serverChallengeTranscript: %v", err)
	}
	if !awpcrypto.VerifyP1363LowS(&trust.Online.PublicKey, serverTranscript, challenge.GetServerSignature()) {
		t.Fatal("server challenge signature did not verify")
	}
	clientNonce := bytes.Repeat([]byte{88}, 32)
	clientTranscript, err := clientChallengeTranscript(
		challenge.GetConnectionId(), challenge.GetConnectionGeneration(), challenge.GetServerNonce(), clientNonce,
		endpoint.ID[:], credential.ID[:], protocolName,
		challenge.GetTrustManifestRevision(), challenge.GetCredentialStatusRevision(),
	)
	if err != nil {
		t.Fatalf("clientChallengeTranscript: %v", err)
	}
	clientSignature, err := awpcrypto.SignP1363LowS(signingKey, clientTranscript)
	if err != nil {
		t.Fatalf("sign client challenge: %v", err)
	}
	responsePacket := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{99}, 16),
		Body: &awpv1.WirePacket_ChallengeResponse{ChallengeResponse: &awpv1.ChallengeResponse{
			ConnectionId: challenge.GetConnectionId(), ConnectionGeneration: challenge.GetConnectionGeneration(),
			EndpointId: endpoint.ID[:], CredentialSerial: credential.ID[:], ClientNonce: clientNonce,
			LastManifestRevision:         challenge.GetTrustManifestRevision(),
			LastCredentialStatusRevision: challenge.GetCredentialStatusRevision(), EndpointSignature: clientSignature,
		}},
	}
	responseBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(responsePacket)
	if err != nil {
		t.Fatalf("encode challenge response: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, responseBytes); err != nil {
		t.Fatalf("write challenge response: %v", err)
	}
	messageType, encoded, err = connection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage {
		t.Fatalf("read ready type=%d error=%v", messageType, err)
	}
	readyPacket := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, readyPacket); err != nil {
		t.Fatalf("decode ready: %v", err)
	}
	ready := readyPacket.GetConnectionReady()
	if ready == nil || ready.GetConnectionGeneration() != challenge.GetConnectionGeneration() ||
		!equalBytes(ready.GetConnectionId(), challenge.GetConnectionId()) {
		t.Fatalf("invalid ConnectionReady: %#v", ready)
	}
	readyTranscript, err := connectionReadyTranscript(
		ready.GetConnectionId(), ready.GetConnectionGeneration(), ready.GetFencingToken(), ready.GetReadyAtMs(),
		ready.GetMaxPacketBytes(), ready.GetMaxInflightFrames(), ready.GetHeartbeatIntervalMs(), endpoint.ID[:],
	)
	if err != nil {
		t.Fatalf("connectionReadyTranscript: %v", err)
	}
	if !awpcrypto.VerifyP1363LowS(&trust.Online.PublicKey, readyTranscript, ready.GetServerSignature()) {
		t.Fatal("ConnectionReady signature did not verify")
	}

	second, secondResponse, err := dialer.Dial(websocketURL, headers)
	if second != nil {
		_ = second.Close()
	}
	if err == nil || secondResponse == nil || secondResponse.StatusCode != http.StatusConflict {
		t.Fatalf("ticket replay error=%v response=%v", err, secondResponse)
	}
}
