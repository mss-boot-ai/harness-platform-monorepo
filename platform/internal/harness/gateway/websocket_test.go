package gateway

import (
	"bytes"
	"context"
	"crypto/ecdsa"
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
	if err == nil || secondResponse == nil || secondResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ticket replay error=%v response=%v", err, secondResponse)
	}
}

func TestWebSocketAcceptsNativeABATicketOnlyWithoutBrowserOrigin(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, credential, _, _, _, _ := abaGatewayFixture(t, now)
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	ticketRaw := bytes.Repeat([]byte{79}, 32)
	ticketValue := base64.RawURLEncoding.EncodeToString(ticketRaw)
	ticket := domain.WSTicket{
		ID: gatewayID(71), TokenHash: sha256.Sum256(ticketRaw), EndpointID: endpoint.ID, CredentialID: credential.ID,
		Purpose: ticketPurpose, Origin: nativeABAOrigin, Protocol: protocolName,
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

	invalid, invalidResponse, err := dialer.Dial(websocketURL, http.Header{"Origin": []string{"http://127.0.0.1:8001"}})
	if invalid != nil {
		_ = invalid.Close()
	}
	if err == nil || invalidResponse == nil || invalidResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("native ticket with browser origin error=%v response=%v", err, invalidResponse)
	}

	connection, response, err := dialer.Dial(websocketURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("dial native WebSocket: %v status=%d", err, response.StatusCode)
		}
		t.Fatalf("dial native WebSocket: %v", err)
	}
	defer connection.Close()
	if connection.Subprotocol() != protocolName {
		t.Fatalf("selected native subprotocol=%q", connection.Subprotocol())
	}
	messageType, encoded, err := connection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage {
		t.Fatalf("read native challenge type=%d error=%v", messageType, err)
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, packet); err != nil || packet.GetServerChallenge() == nil {
		t.Fatalf("decode native challenge packet=%#v error=%v", packet, err)
	}
}

func TestNewReadyGenerationFencesPreviousConnectionAndMarksPresence(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	persistence, endpoint, credential, _, _, signingKey, _ := gatewayFixture(t, now)
	trust, err := NewEphemeralTrust(deterministicGatewayBytes(512), now)
	if err != nil {
		t.Fatalf("NewEphemeralTrust: %v", err)
	}
	handler, err := NewHandler(Config{
		AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: "http://127.0.0.1:8082", Trust: trust,
	}, persistence, deterministicGatewayBytes(4096), func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewHandler: %v", err)
	}
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	websocketURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/gateway/v1/ws"
	origin := http.Header{"Origin": []string{"http://127.0.0.1:8001"}}

	open := func(ticketByte, idByte byte) (*websocket.Conn, uint64) {
		t.Helper()
		ticketRaw := bytes.Repeat([]byte{ticketByte}, 32)
		ticketValue := base64.RawURLEncoding.EncodeToString(ticketRaw)
		ticket := domain.WSTicket{
			ID: gatewayID(idByte), TokenHash: sha256.Sum256(ticketRaw), EndpointID: endpoint.ID, CredentialID: credential.ID,
			Purpose: ticketPurpose, Origin: "http://127.0.0.1:8001", Protocol: protocolName,
			Status: domain.TicketStatusIssued, ExpiresAt: now.Add(30 * time.Second), CreatedAt: now,
		}
		if err := persistence.CreateTicket(t.Context(), ticket, 30*time.Second); err != nil {
			t.Fatalf("CreateTicket: %v", err)
		}
		dialer := websocket.Dialer{Subprotocols: []string{protocolName, "mss.ticket." + ticketValue}}
		connection, response, err := dialer.Dial(websocketURL, origin)
		if err != nil {
			if response != nil {
				t.Fatalf("dial WebSocket: %v status=%d", err, response.StatusCode)
			}
			t.Fatalf("dial WebSocket: %v", err)
		}
		generation := completeClientChallenge(t, connection, endpoint, credential, signingKey)
		return connection, generation
	}

	first, firstGeneration := open(81, 81)
	defer first.Close()
	second, secondGeneration := open(82, 82)
	defer second.Close()
	if firstGeneration != 1 || secondGeneration != 2 {
		t.Fatalf("connection generations=%d,%d", firstGeneration, secondGeneration)
	}
	_ = first.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := first.ReadMessage(); err == nil {
		t.Fatal("previous connection remained readable after newer READY")
	}
	endpoints, err := persistence.ListEndpoints(t.Context(), endpoint.OwnerUserID, endpoint.TenantID, 10)
	if err != nil || len(endpoints) != 1 || endpoints[0].LastSeenAt == nil || !endpoints[0].LastSeenAt.Equal(now) {
		t.Fatalf("endpoint presence=%#v error=%v", endpoints, err)
	}
}

func completeClientChallenge(
	t *testing.T,
	connection *websocket.Conn,
	endpoint domain.Endpoint,
	credential domain.EndpointCredential,
	signingKey *ecdsa.PrivateKey,
) uint64 {
	t.Helper()
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
	clientNonce := bytes.Repeat([]byte{89}, 32)
	transcript, err := clientChallengeTranscript(
		challenge.GetConnectionId(), challenge.GetConnectionGeneration(), challenge.GetServerNonce(), clientNonce,
		endpoint.ID[:], credential.ID[:], protocolName,
		challenge.GetTrustManifestRevision(), challenge.GetCredentialStatusRevision(),
	)
	if err != nil {
		t.Fatalf("clientChallengeTranscript: %v", err)
	}
	signature, err := awpcrypto.SignP1363LowS(signingKey, transcript)
	if err != nil {
		t.Fatalf("sign client challenge: %v", err)
	}
	response := &awpv1.WirePacket{
		WireMajor: 1, PacketId: bytes.Repeat([]byte{90}, 16),
		Body: &awpv1.WirePacket_ChallengeResponse{ChallengeResponse: &awpv1.ChallengeResponse{
			ConnectionId: challenge.GetConnectionId(), ConnectionGeneration: challenge.GetConnectionGeneration(),
			EndpointId: endpoint.ID[:], CredentialSerial: credential.ID[:], ClientNonce: clientNonce,
			LastManifestRevision:         challenge.GetTrustManifestRevision(),
			LastCredentialStatusRevision: challenge.GetCredentialStatusRevision(), EndpointSignature: signature,
		}},
	}
	encoded, err = proto.MarshalOptions{Deterministic: true}.Marshal(response)
	if err != nil {
		t.Fatalf("encode challenge response: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		t.Fatalf("write challenge response: %v", err)
	}
	messageType, encoded, err = connection.ReadMessage()
	if err != nil || messageType != websocket.BinaryMessage {
		t.Fatalf("read ready type=%d error=%v", messageType, err)
	}
	ready := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, ready); err != nil || ready.GetConnectionReady() == nil {
		t.Fatalf("decode ConnectionReady packet=%#v error=%v", ready, err)
	}
	return ready.GetConnectionReady().GetConnectionGeneration()
}
