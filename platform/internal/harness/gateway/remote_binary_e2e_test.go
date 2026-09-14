package gateway

// Opt-in cross-language integration: actual Rust ABA, live Gateway, SQLite and
// a deterministic ACP subprocess. This is not real-model/browser acceptance.
import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/store"
	"google.golang.org/protobuf/proto"
)

type remoteLockedBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *remoteLockedBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(value)
}

func (b *remoteLockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

type remoteBinaryClient struct {
	t                                             *testing.T
	server                                        *httptest.Server
	store                                         *store.Store
	aba, hc                                       domain.Endpoint
	credential                                    domain.EndpointCredential
	signing, abaSigning                           *ecdsa.PrivateKey
	public                                        awpcrypto.P256PublicJWK
	token                                         string
	ws                                            *websocket.Conn
	session, channel, keyID                        domain.ID
	sendKey, receiveKey, sendPrefix, receivePrefix []byte
	sequence, received, control                    uint64
}

func TestRemoteActualABAGatewayDuplex(t *testing.T) {
	binaryPath := os.Getenv("HARNESS_TEST_ABA_BINARY")
	fixture := os.Getenv("HARNESS_TEST_ACP_FIXTURE")
	if binaryPath == "" || fixture == "" {
		t.Skip("opt-in: actual ABA binary and ACP fixture paths are required")
	}
	if !filepath.IsAbs(binaryPath) || !filepath.IsAbs(fixture) {
		t.Fatal("explicit absolute test executable paths required")
	}
	now := time.Now().UTC()
	persistence, aba, abaCredential, abaToken, abaRefresh, abaKey, _ := abaGatewayFixture(t, now)
	hc, hcCredential, err := persistence.GetEndpointCredential(t.Context(), gatewayID(1), gatewayID(3), now)
	if err != nil {
		t.Fatal(err)
	}
	hcPublic, hcKey := gatewaySigningKey(1)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + listener.Addr().String()
	trust, err := NewEphemeralTrust(rand.Reader, now)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(Config{AllowedOrigin: "http://127.0.0.1:8001", ExternalOrigin: base, NativeExternalOrigin: base, Trust: trust}, persistence, rand.Reader, time.Now)
	if err != nil {
		listener.Close()
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	_ = server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	client := &remoteBinaryClient{t: t, server: server, store: persistence, aba: aba, hc: hc, credential: hcCredential,
		public: hcPublic, signing: hcKey, abaSigning: abaKey, token: base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))}

	directory := t.TempDir()
	identityPath := filepath.Join(directory, "identity.json")
	scalar := func(value byte) string {
		raw := make([]byte, 32)
		raw[31] = value
		return base64.RawURLEncoding.EncodeToString(raw)
	}
	identity, _ := json.Marshal(map[string]any{"version": 1, "signing_d": scalar(7), "kem_d": scalar(8), "credentials": map[string]any{
		"endpoint_id": aba.ID.String(), "credential_id": abaCredential.ID.String(), "access_token": abaToken, "access_expires_at": now.Add(time.Hour).Format(time.RFC3339Nano),
		"refresh_token": abaRefresh, "refresh_expires_at": now.Add(time.Hour).Format(time.RFC3339Nano)}})
	if err := os.WriteFile(identityPath, identity, 0o600); err != nil {
		t.Fatal(err)
	}
	python, err := filepath.EvalSymlinks("/usr/bin/python3")
	if err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("schema_version = 1\n[platform]\nurl = %q\n[[runtime]]\nid = \"fixture\"\ndisplay_name = \"Deterministic ACP fixture\"\ncommand = %q\nargs = [%q]\nmax_sessions = 2\n[[workspace]]\nid = \"fixture\"\ndisplay_name = \"Ephemeral test directory\"\npath = %q\nallowed_runtimes = [\"fixture\"]\n", base, python, fixture, directory)
	configPath := filepath.Join(directory, "aba.toml")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	command := exec.CommandContext(ctx, binaryPath, "run", "--config", configPath, "--store", identityPath, "--insecure-dev-keystore")
	command.Stdout = io.Discard
	var stderr remoteLockedBuffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("ABA test process failed to stop")
		}
	})

	deadline := time.Now().Add(15 * time.Second)
	for {
		result := client.request("/gateway/v1/endpoints/abas", nil, "")
		if result.Code == http.StatusOK && bytes.Contains(result.Body.Bytes(), []byte(aba.ID.String())) {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("ABA exited during setup: %v (safe error: %s)", err, strings.TrimSpace(stderr.String()))
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("ABA did not connect before deadline")
		}
		time.Sleep(30 * time.Millisecond)
	}
	client.connect()
	t.Cleanup(func() {
		if client.ws != nil {
			_ = client.ws.Close()
		}
	})
	created := client.request("/gateway/v1/sessions", map[string]any{"abaEndpointId": aba.ID.String(), "runtimeProfileId": "fixture", "workspaceId": "fixture", "requestedCapabilities": []string{"prompt", "session", "permission", "cancel", "remote-session-v1"}}, "remote-create-fixture-0001")
	if created.Code != http.StatusCreated {
		t.Fatalf("create session status=%d body=%s", created.Code, created.Body.String())
	}
	var session endpointSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	client.session, err = domain.ParseID(session.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	client.channel, err = sessionChannelID(client.session, aba.ID, hc.ID)
	if err != nil {
		t.Fatal(err)
	}
	for {
		packet := client.packet()
		control := packet.GetControl()
		if control != nil && control.GetType() == awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE {
			client.keyPackage(control)
			break
		}
		if packet.GetError() != nil {
			t.Fatal("Gateway returned an error before key establishment")
		}
	}
	for attempt := 0; attempt < 100; attempt++ {
		current, err := persistence.GetSession(t.Context(), client.session)
		if err == nil && current.Status == domain.SessionStatusActive {
			break
		}
		if attempt == 99 {
			t.Fatal("key acknowledgment did not activate session")
		}
		time.Sleep(20 * time.Millisecond)
	}

	client.send(map[string]any{"id": "describe", "method": "_mss/session/describe", "params": map[string]any{"sessionId": client.session.String()}})
	described := client.untilID("describe")
	raw, _ := json.Marshal(described)
	if !bytes.Contains(raw, []byte("configOptions")) || !bytes.Contains(raw, []byte("turnCancellation")) {
		t.Fatal("actual runtime descriptor did not reach HC")
	}
	client.send(map[string]any{"id": "config", "method": "session/set_config_option", "params": map[string]any{"sessionId": client.session.String(), "configId": "model", "value": "large"}})
	configured := client.untilID("config")
	raw, _ = json.Marshal(configured)
	if !bytes.Contains(raw, []byte(`"currentValue":"large"`)) {
		t.Fatal("confirmed runtime config was not forwarded")
	}

	client.prompt("wait", "wait")
	first := client.message()
	if first["method"] != "session/update" {
		t.Fatal("first streaming update did not arrive before completion")
	}
	pong := false
	client.ws.SetPongHandler(func(value string) error {
		if value == "during-turn" {
			pong = true
		}
		return nil
	})
	if err := client.ws.WriteControl(websocket.PingMessage, []byte("during-turn"), time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	client.send(map[string]any{"method": "session/cancel", "params": map[string]any{"sessionId": client.session.String()}})
	cancelled := client.untilID("wait")
	if result, ok := cancelled["result"].(map[string]any); !ok || result["stopReason"] != "cancelled" {
		t.Fatal("turn cancel did not return the original terminal response")
	}
	if !pong || time.Since(started) > 5*time.Second {
		t.Fatal("Agent work blocked ping or cancel processing")
	}
	current, err := persistence.GetSession(t.Context(), client.session)
	if err != nil || current.Status != domain.SessionStatusActive {
		t.Fatal("turn cancellation closed the session")
	}

	client.prompt("permission", "permission")
	var permission map[string]any
	for {
		permission = client.message()
		if permission["method"] == "session/request_permission" {
			break
		}
		if permission["id"] == "permission" {
			t.Fatal("runtime completed before human permission")
		}
	}
	if permission["id"] == float64(77) {
		t.Fatal("runtime permission ID was not scoped by bridge")
	}
	client.send(map[string]any{"id": permission["id"], "result": map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": "deny"}}})
	rejected := client.untilID("permission")
	if result, ok := rejected["result"].(map[string]any); !ok || result["stopReason"] != "cancelled" {
		t.Fatal("original permission denial was not honored")
	}

	// Drop only the HC transport while the runtime is waiting; reconnect with a fresh ticket.
	client.prompt("reconnect-turn", "wait")
	_ = client.message()
	_ = client.ws.Close()
	client.connect()
	client.controlMessage(awpv1.ControlType_CONTROL_TYPE_RESUME_STATE, &awpv1.ResumeState{Cursors: []*awpv1.ChannelCursor{{ChannelId: client.channel[:], Direction: awpv1.Direction_DIRECTION_ABA_TO_HC, HighestContiguousSequence: client.received, KeyGeneration: 1}}}, make([]byte, 16))
	client.send(map[string]any{"method": "session/cancel", "params": map[string]any{"sessionId": client.session.String()}})
	resumed := client.untilID("reconnect-turn")
	if resumed["result"] == nil {
		t.Fatal("same runtime did not survive HC disconnect")
	}
	const canary = "private-remote-prompt-canary-17e98a"
	packet := client.prompt("last", canary)
	_ = client.untilID("last")
	// Replay unchanged bytes, then verify the session remains usable.
	if err := client.ws.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		t.Fatal(err)
	}
	client.send(map[string]any{"id": "after-replay", "method": "_mss/session/describe", "params": map[string]any{"sessionId": client.session.String()}})
	_ = client.untilID("after-replay")
	sent := new(awpv1.WirePacket)
	if err := proto.Unmarshal(packet, sent); err != nil {
		t.Fatal(err)
	}
	var messageID domain.ID
	copy(messageID[:], sent.GetEncrypted().GetMessageId())
	storedFrame, err := persistence.GetFrame(t.Context(), messageID)
	if err != nil {
		t.Fatal(err)
	}
	storedJSON, err := json.Marshal(storedFrame)
	if err != nil || bytes.Contains(storedJSON, []byte(canary)) || bytes.Contains(packet, []byte(canary)) {
		t.Fatal("private prompt leaked into the persisted transport frame")
	}
	snapshot, err := persistence.Delivery(t.Context(), client.session, "owner", "tenant", 200)
	if err != nil {
		t.Fatal(err)
	}
	saved, _ := json.Marshal(snapshot)
	if bytes.Contains(saved, []byte(canary)) || strings.Contains(stderr.String(), canary) {
		t.Fatal("private prompt leaked into server metadata or ABA diagnostics")
	}
	closed := client.request("/gateway/v1/sessions/"+client.session.String()+"/close", map[string]any{}, "remote-close-fixture-0001")
	if closed.Code != http.StatusOK && closed.Code != http.StatusAccepted {
		t.Fatalf("close status=%d body=%s", closed.Code, closed.Body.String())
	}
	for attempt := 0; attempt < 100; attempt++ {
		current, err := persistence.GetSession(t.Context(), client.session)
		if err == nil && current.Status == domain.SessionStatusClosed {
			t.Log("actual ABA + Gateway + HPKE + config + streaming + reverse permission + ping + cancel/continue + reconnect + replay passed (deterministic ACP)")
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("close was not confirmed")
}

func (c *remoteBinaryClient) request(path string, body any, idempotency string) *httptest.ResponseRecorder {
	c.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			c.t.Fatal(err)
		}
	}
	call := func(proof string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(encoded))
		request.Header.Set("Origin", "http://127.0.0.1:8001")
		request.Header.Set("Authorization", "DPoP "+c.token)
		request.Header.Set("Content-Type", "application/json")
		if idempotency != "" {
			request.Header.Set("Idempotency-Key", idempotency)
		}
		if proof != "" {
			request.Header.Set("DPoP", proof)
		}
		response := httptest.NewRecorder()
		c.server.Config.Handler.ServeHTTP(response, request)
		return response
	}
	challenge := call("")
	nonce := challenge.Header().Get("DPoP-Nonce")
	if challenge.Code != http.StatusUnauthorized || nonce == "" {
		c.t.Fatalf("DPoP challenge status=%d", challenge.Code)
	}
	header, _ := json.Marshal(map[string]any{"alg": "ES256", "typ": "dpop+jwt", "jwk": c.public})
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		c.t.Fatal(err)
	}
	claims, _ := json.Marshal(map[string]any{"ath": awpcrypto.AccessTokenHash(c.token), "htm": "POST", "htu": c.server.URL + path, "iat": time.Now().Unix(), "jti": fmt.Sprintf("%x", id), "nonce": nonce})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	signature, err := awpcrypto.SignP1363LowS(c.signing, []byte(input))
	if err != nil {
		c.t.Fatal(err)
	}
	return call(input + "." + base64.RawURLEncoding.EncodeToString(signature))
}
func (c *remoteBinaryClient) connect() {
	c.t.Helper()
	ticket := c.request("/gateway/v1/ws/tickets", nil, "")
	if ticket.Code != http.StatusCreated {
		c.t.Fatalf("ticket status=%d", ticket.Code)
	}
	var value struct {
		Ticket string `json:"ticket"`
	}
	if err := json.Unmarshal(ticket.Body.Bytes(), &value); err != nil {
		c.t.Fatal(err)
	}
	dialer := websocket.Dialer{Subprotocols: []string{protocolName, "mss.ticket." + value.Ticket}, HandshakeTimeout: 5 * time.Second}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(c.server.URL, "http")+"/gateway/v1/ws", http.Header{"Origin": []string{"http://127.0.0.1:8001"}})
	if err != nil {
		if response != nil {
			_ = response.Body.Close()
		}
		c.t.Fatal(err)
	}
	c.ws = connection
	c.control = 0
	_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
	completeClientChallenge(c.t, connection, c.hc, c.credential, c.signing)
}
func (c *remoteBinaryClient) packet() *awpv1.WirePacket {
	c.t.Helper()
	_ = c.ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	kind, raw, err := c.ws.ReadMessage()
	if err != nil || kind != websocket.BinaryMessage {
		c.t.Fatalf("read actual remote packet: %v type=%d", err, kind)
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(raw, packet); err != nil {
		c.t.Fatal(err)
	}
	return packet
}
func (c *remoteBinaryClient) write(packet *awpv1.WirePacket) {
	c.t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		c.t.Fatal(err)
	}
	if err := c.ws.WriteMessage(websocket.BinaryMessage, raw); err != nil {
		c.t.Fatal(err)
	}
}
func (c *remoteBinaryClient) controlMessage(kind awpv1.ControlType, payload proto.Message, receiver []byte) {
	c.t.Helper()
	raw, err := proto.MarshalOptions{Deterministic: true}.Marshal(payload)
	if err != nil {
		c.t.Fatal(err)
	}
	c.control++
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		c.t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	transcript, err := controlTranscript(id, c.hc.ID[:], receiver, c.control, now, uint32(kind), raw)
	if err != nil {
		c.t.Fatal(err)
	}
	signature, err := awpcrypto.SignP1363LowS(c.signing, transcript)
	if err != nil {
		c.t.Fatal(err)
	}
	c.write(&awpv1.WirePacket{WireMajor: 1, PacketId: id, Body: &awpv1.WirePacket_Control{Control: &awpv1.ControlFrame{MessageId: id, SenderEndpointId: c.hc.ID[:], ReceiverEndpointId: receiver, ControlSequence: c.control, CreatedAtMs: now, Type: kind, Payload: raw, Signature: signature}}})
}
func (c *remoteBinaryClient) keyPackage(control *awpv1.ControlFrame) {
	c.t.Helper()
	transcript, err := controlTranscript(control.MessageId, control.SenderEndpointId, control.ReceiverEndpointId, control.ControlSequence, control.CreatedAtMs, uint32(control.Type), control.Payload)
	if err != nil || !awpcrypto.VerifyP1363LowS(&c.abaSigning.PublicKey, transcript, control.Signature) {
		c.t.Fatal("actual key package outer signature invalid")
	}
	packageValue := new(awpv1.SessionKeyPackage)
	if err := proto.Unmarshal(control.Payload, packageValue); err != nil {
		c.t.Fatal(err)
	}
	if !bytes.Equal(packageValue.SessionId, c.session[:]) || !bytes.Equal(packageValue.RecipientHcEndpointId, c.hc.ID[:]) || !bytes.Equal(packageValue.IssuerAbaEndpointId, c.aba.ID[:]) {
		c.t.Fatal("key package route mismatch")
	}
	envelope, err := keyPackageEnvelopeTranscript(packageValue.KeyPackageId, packageValue.SessionId, packageValue.KeyGeneration, packageValue.IssuerAbaEndpointId, packageValue.RecipientHcEndpointId, packageValue.IssuerCredentialId, packageValue.PolicyRevision, packageValue.NotBeforeMs, packageValue.ExpiresAtMs, packageValue.HpkeEnc, packageValue.HpkeCiphertext)
	if err != nil || !awpcrypto.VerifyP1363LowS(&c.abaSigning.PublicKey, envelope, packageValue.IssuerSignature) {
		c.t.Fatal("key package issuer signature invalid")
	}
	info, err := keyPackageInfo(c.session[:], 1, c.aba.ID[:], c.hc.ID[:], 1)
	if err != nil {
		c.t.Fatal(err)
	}
	scalar := make([]byte, 32)
	scalar[31] = 2
	private, err := ecdh.P256().NewPrivateKey(scalar)
	if err != nil {
		c.t.Fatal(err)
	}
	secret, err := hpke.NewDHKEMPrivateKey(private)
	if err != nil {
		c.t.Fatal(err)
	}
	recipient, err := hpke.NewRecipient(packageValue.HpkeEnc, secret, hpke.HKDFSHA256(), hpke.AES256GCM(), info)
	if err != nil {
		c.t.Fatal(err)
	}
	plaintext, err := recipient.Open(info, packageValue.HpkeCiphertext)
	if err != nil {
		c.t.Fatal(err)
	}
	const prefix = "mss-key-package-plaintext-v1"
	if len(plaintext) != 157 || string(plaintext[:len(prefix)]) != prefix {
		c.t.Fatal("key plaintext shape mismatch")
	}
	offset := len(prefix)
	if !bytes.Equal(plaintext[offset:offset+16], c.session[:]) || binary.BigEndian.Uint64(plaintext[offset+16:offset+24]) != 1 {
		c.t.Fatal("key plaintext session mismatch")
	}
	offset += 24
	copy(c.keyID[:], plaintext[offset:offset+16])
	offset += 16
	srk := plaintext[offset : offset+32]
	offset += 32
	nonce := plaintext[offset : offset+32]
	offset += 32
	c.sendPrefix = append([]byte(nil), plaintext[offset:offset+4]...)
	offset += 4
	c.receivePrefix = append([]byte(nil), plaintext[offset:offset+4]...)
	prk, err := hkdf.Extract(sha256.New, srk, nonce)
	if err != nil {
		c.t.Fatal(err)
	}
	context := fmt.Sprintf("mss-awp/v1/session/%s/generation/1/endpoint/%s", c.session.String(), c.hc.ID.String())
	c.sendKey, err = hkdf.Expand(sha256.New, prk, context+"/hc-to-aba", 32)
	if err != nil {
		c.t.Fatal(err)
	}
	c.receiveKey, err = hkdf.Expand(sha256.New, prk, context+"/aba-to-hc", 32)
	if err != nil {
		c.t.Fatal(err)
	}
	c.controlMessage(awpv1.ControlType_CONTROL_TYPE_SESSION_KEY_PACKAGE_ACK, &awpv1.SessionKeyPackageAck{SessionId: c.session[:], KeyGeneration: 1, KeyPackageId: packageValue.KeyPackageId, RecipientHcEndpointId: c.hc.ID[:], AcknowledgedAtMs: time.Now().UnixMilli()}, c.aba.ID[:])
}
func (c *remoteBinaryClient) send(message map[string]any) []byte {
	c.t.Helper()
	message["jsonrpc"] = "2.0"
	raw, err := json.Marshal(message)
	if err != nil {
		c.t.Fatal(err)
	}
	c.sequence++
	packet := testEncryptedFramePacket(c.t, c.session, c.channel, c.hc.ID, c.aba.ID, c.keyID, awpv1.Direction_DIRECTION_HC_TO_ABA, c.sendKey, c.sendPrefix, c.signing, c.sequence, raw, time.Now())
	if err := c.ws.WriteMessage(websocket.BinaryMessage, packet); err != nil {
		c.t.Fatal(err)
	}
	return packet
}
func (c *remoteBinaryClient) prompt(id, text string) []byte {
	return c.send(map[string]any{"id": id, "method": "session/prompt", "params": map[string]any{"sessionId": c.session.String(), "prompt": []map[string]any{{"type": "text", "text": text}}}})
}
func (c *remoteBinaryClient) untilID(id string) map[string]any {
	c.t.Helper()
	for count := 0; count < 100; count++ {
		message := c.message()
		if message["id"] == id {
			if message["error"] != nil {
				c.t.Fatalf("runtime returned an error for test operation %s", id)
			}
			return message
		}
	}
	c.t.Fatal("bounded response search exceeded")
	return nil
}
func (c *remoteBinaryClient) message() map[string]any {
	c.t.Helper()
	for count := 0; count < 200; count++ {
		packet := c.packet()
		if packet.GetError() != nil {
			c.t.Fatalf("remote error code %s", packet.GetError().GetCode())
		}
		frame := packet.GetEncrypted()
		if frame == nil {
			continue
		}
		if !bytes.Equal(frame.SessionId, c.session[:]) || !bytes.Equal(frame.ChannelId, c.channel[:]) || frame.Direction != awpv1.Direction_DIRECTION_ABA_TO_HC || !bytes.Equal(frame.KeyId, c.keyID[:]) {
			c.t.Fatal("encrypted result route mismatch")
		}
		aad, err := frameAAD(frame)
		if err != nil || !awpcrypto.VerifyP1363LowS(&c.abaSigning.PublicKey, frameSignatureInput(aad, frame.Ciphertext), frame.Signature) {
			c.t.Fatal("result signature mismatch")
		}
		block, err := aes.NewCipher(c.receiveKey)
		if err != nil {
			c.t.Fatal(err)
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			c.t.Fatal(err)
		}
		nonce := make([]byte, 12)
		copy(nonce, c.receivePrefix)
		binary.BigEndian.PutUint64(nonce[4:], frame.Sequence)
		raw, err := gcm.Open(nil, nonce, frame.Ciphertext, aad)
		if err != nil {
			c.t.Fatal(err)
		}
		if frame.Sequence > c.received+1 {
			c.t.Fatal("result stream has an unexplained gap")
		}
		duplicate := frame.Sequence <= c.received
		if frame.Sequence > c.received {
			c.received = frame.Sequence
		}
		ack := testAckFramePacket(c.t, c.session, c.channel, c.hc.ID, awpv1.Direction_DIRECTION_ABA_TO_HC, c.received, c.signing, time.Now())
		if err := c.ws.WriteMessage(websocket.BinaryMessage, ack); err != nil {
			c.t.Fatal(err)
		}
		if duplicate {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			c.t.Fatal(err)
		}
		return value
	}
	c.t.Fatal("bounded packet search exceeded")
	return nil
}
