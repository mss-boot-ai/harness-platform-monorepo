package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

const (
	maxWirePacketBytes  = 1 << 20
	maxInflightFrames   = 128
	heartbeatIntervalMS = 30_000
	challengeTimeout    = 10 * time.Second
)

func (server *Server) websocket(writer http.ResponseWriter, request *http.Request) {
	ticketValue, ok := websocketTicketProtocol(request.Header.Values("Sec-WebSocket-Protocol"))
	if !ok {
		writeGatewayError(writer, http.StatusUnauthorized, "TICKET_INVALID", "WebSocket ticket is required")
		return
	}
	ticketRaw, err := base64.RawURLEncoding.Strict().DecodeString(ticketValue)
	if err != nil || len(ticketRaw) != 32 {
		writeGatewayError(writer, http.StatusUnauthorized, "TICKET_INVALID", "WebSocket ticket is invalid")
		return
	}
	ticketHash := sha256.Sum256(ticketRaw)
	now := server.now().UTC()
	ticket, err := server.persistence.InspectTicket(request.Context(), ticketHash, now)
	if err != nil || !server.validTicketRequest(ticket, request) || ticket.Protocol != protocolName || ticket.Purpose != ticketPurpose {
		writeGatewayError(writer, http.StatusUnauthorized, "TICKET_INVALID", "WebSocket ticket is invalid")
		return
	}
	endpoint, credential, err := server.persistence.GetEndpointCredential(request.Context(), ticket.EndpointID, ticket.CredentialID, now)
	if err != nil {
		writeDomainError(writer, err)
		return
	}
	if _, err := server.persistence.ConsumeTicket(
		request.Context(), ticketHash, endpoint.ID, credential.ID,
		ticketPurpose, ticket.Origin, protocolName, now,
	); err != nil {
		writeDomainError(writer, err)
		return
	}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: challengeTimeout,
		ReadBufferSize:   4096,
		WriteBufferSize:  4096,
		Subprotocols:     []string{protocolName},
		CheckOrigin: func(upgradeRequest *http.Request) bool {
			return server.validTicketRequest(ticket, upgradeRequest)
		},
	}
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	defer connection.Close()
	connection.SetReadLimit(maxWirePacketBytes)
	_ = connection.SetReadDeadline(time.Now().Add(challengeTimeout))
	if err := server.performChallenge(connection, endpoint, credential, now); err != nil {
		_ = connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "AWP challenge failed"),
			time.Now().Add(time.Second),
		)
		return
	}
	_ = connection.SetReadDeadline(time.Now().Add(2 * time.Duration(heartbeatIntervalMS) * time.Millisecond))
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(2 * time.Duration(heartbeatIntervalMS) * time.Millisecond))
	})
	for {
		messageType, message, err := connection.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.BinaryMessage || len(message) == 0 || len(message) > maxWirePacketBytes {
			_ = connection.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseUnsupportedData, "binary AWP packet required"),
				time.Now().Add(time.Second),
			)
			return
		}
		// Business packet routing is enabled by the relay checkpoint. Before
		// then, a READY connection remains authenticated but fails closed on
		// every unimplemented packet instead of silently acknowledging it.
		_ = connection.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "AWP relay not enabled"),
			time.Now().Add(time.Second),
		)
		return
	}
}

func (server *Server) validTicketRequest(ticket domain.WSTicket, request *http.Request) bool {
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	switch ticket.Origin {
	case nativeABAOrigin:
		return origin == ""
	case server.config.AllowedOrigin:
		return origin == server.config.AllowedOrigin
	default:
		return false
	}
}

func (server *Server) performChallenge(
	connection *websocket.Conn,
	endpoint domain.Endpoint,
	credential domain.EndpointCredential,
	now time.Time,
) error {
	connectionID, err := server.randomBytes(16)
	if err != nil {
		return err
	}
	packetID, err := server.randomBytes(16)
	if err != nil {
		return err
	}
	serverNonce, err := server.randomBytes(32)
	if err != nil {
		return err
	}
	generation, err := server.persistence.NextConnectionGeneration(context.Background(), endpoint.ID, now)
	if err != nil {
		return err
	}
	serverTimeMS := now.UnixMilli()
	credentialRevision := uint64(1)
	transcript, err := serverChallengeTranscript(
		connectionID, generation, serverNonce, serverTimeMS,
		server.trust.Revision, credentialRevision, endpoint.ID[:],
	)
	if err != nil {
		return err
	}
	signature, err := awpcrypto.SignP1363LowS(server.trust.Online, transcript)
	if err != nil {
		return err
	}
	challengePacket := &awpv1.WirePacket{
		WireMajor: 1, WireMinor: 0, PacketId: packetID,
		Body: &awpv1.WirePacket_ServerChallenge{ServerChallenge: &awpv1.ServerChallenge{
			ConnectionId: connectionID, ConnectionGeneration: generation, ServerNonce: serverNonce,
			ServerTimeMs: serverTimeMS, TrustManifestRevision: server.trust.Revision,
			CredentialStatusRevision: credentialRevision, ServerSignature: signature,
		}},
	}
	if err := writeWirePacket(connection, challengePacket); err != nil {
		return err
	}
	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		return err
	}
	if messageType != websocket.BinaryMessage || len(encoded) == 0 || len(encoded) > maxWirePacketBytes {
		return errors.New("challenge response must be one bounded binary packet")
	}
	responsePacket := new(awpv1.WirePacket)
	if err := proto.Unmarshal(encoded, responsePacket); err != nil {
		return errors.New("challenge response is not valid AWP protobuf")
	}
	response := responsePacket.GetChallengeResponse()
	if responsePacket.GetWireMajor() != 1 || len(responsePacket.GetPacketId()) != 16 || response == nil ||
		!equalBytes(response.GetConnectionId(), connectionID) || response.GetConnectionGeneration() != generation ||
		!equalBytes(response.GetEndpointId(), endpoint.ID[:]) || !equalBytes(response.GetCredentialSerial(), credential.ID[:]) ||
		len(response.GetClientNonce()) != 32 || response.GetLastManifestRevision() != server.trust.Revision ||
		response.GetLastCredentialStatusRevision() != credentialRevision || len(response.GetEndpointSignature()) != 64 {
		return errors.New("challenge response binding is invalid")
	}
	clientTranscript, err := clientChallengeTranscript(
		connectionID, generation, serverNonce, response.GetClientNonce(), endpoint.ID[:], credential.ID[:],
		connection.Subprotocol(), response.GetLastManifestRevision(), response.GetLastCredentialStatusRevision(),
	)
	if err != nil {
		return err
	}
	signingJWK, err := awpcrypto.ParseP256PublicJWK(endpoint.SigningPublicJWK)
	if err != nil {
		return err
	}
	publicKey, err := signingJWK.PublicKey()
	if err != nil || !awpcrypto.VerifyP1363LowS(publicKey, clientTranscript, response.GetEndpointSignature()) {
		return errors.New("endpoint challenge signature is invalid")
	}

	fencingToken, err := server.randomBytes(32)
	if err != nil {
		return err
	}
	readyPacketID, err := server.randomBytes(16)
	if err != nil {
		return err
	}
	readyAtMS := server.now().UTC().UnixMilli()
	readyTranscript, err := connectionReadyTranscript(
		connectionID, generation, fencingToken, readyAtMS,
		maxWirePacketBytes, maxInflightFrames, heartbeatIntervalMS, endpoint.ID[:],
	)
	if err != nil {
		return err
	}
	readySignature, err := awpcrypto.SignP1363LowS(server.trust.Online, readyTranscript)
	if err != nil {
		return err
	}
	return writeWirePacket(connection, &awpv1.WirePacket{
		WireMajor: 1, WireMinor: 0, PacketId: readyPacketID,
		Body: &awpv1.WirePacket_ConnectionReady{ConnectionReady: &awpv1.ConnectionReady{
			ConnectionId: connectionID, ConnectionGeneration: generation, FencingToken: fencingToken,
			ReadyAtMs: readyAtMS, MaxPacketBytes: maxWirePacketBytes, MaxInflightFrames: maxInflightFrames,
			HeartbeatIntervalMs: heartbeatIntervalMS, ServerSignature: readySignature,
		}},
	})
}

func (server *Server) randomBytes(length int) ([]byte, error) {
	value := make([]byte, length)
	if _, err := io.ReadFull(server.random, value); err != nil {
		return nil, err
	}
	return value, nil
}

func writeWirePacket(connection *websocket.Conn, packet *awpv1.WirePacket) error {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		return err
	}
	_ = connection.SetWriteDeadline(time.Now().Add(challengeTimeout))
	return connection.WriteMessage(websocket.BinaryMessage, encoded)
}

func websocketTicketProtocol(headers []string) (string, bool) {
	var foundProtocol bool
	var ticket string
	var count int
	for _, header := range headers {
		for _, candidate := range strings.Split(header, ",") {
			candidate = strings.TrimSpace(candidate)
			if candidate == "" {
				continue
			}
			count++
			switch {
			case candidate == protocolName && !foundProtocol:
				foundProtocol = true
			case strings.HasPrefix(candidate, "mss.ticket.") && ticket == "":
				ticket = strings.TrimPrefix(candidate, "mss.ticket.")
			default:
				return "", false
			}
		}
	}
	return ticket, foundProtocol && ticket != "" && count == 2
}

func equalBytes(left, right []byte) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare(left, right) == 1
}
