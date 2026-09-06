package protocol_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"os"
	"testing"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
	"google.golang.org/protobuf/proto"
)

type challengeVector struct {
	FixtureUse               string `json:"fixtureUse"`
	WirePacketBase64         string `json:"wirePacketBase64"`
	WireMajor                uint32 `json:"wireMajor"`
	WireMinor                uint32 `json:"wireMinor"`
	PacketIDByte             byte   `json:"packetIdByte"`
	ConnectionIDByte         byte   `json:"connectionIdByte"`
	ConnectionGeneration     uint64 `json:"connectionGeneration"`
	ServerNonceByte          byte   `json:"serverNonceByte"`
	ServerTimeMS             int64  `json:"serverTimeMs"`
	TrustManifestRevision    uint64 `json:"trustManifestRevision"`
	CredentialStatusRevision uint64 `json:"credentialStatusRevision"`
	ServerSignatureByte      byte   `json:"serverSignatureByte"`
}

func TestGeneratedGoBindingMatchesServerChallengeVector(t *testing.T) {
	contents, err := os.ReadFile("../../../../protocol/testdata/v1/wire-server-challenge.json")
	if err != nil {
		t.Fatalf("read challenge vector: %v", err)
	}
	var vector challengeVector
	if err := json.Unmarshal(contents, &vector); err != nil {
		t.Fatalf("decode challenge vector: %v", err)
	}
	if vector.FixtureUse == "" {
		t.Fatal("challenge vector is not marked for tests")
	}
	wireBytes, err := base64.RawStdEncoding.DecodeString(vector.WirePacketBase64)
	if err != nil {
		t.Fatalf("decode packet: %v", err)
	}
	packet := new(awpv1.WirePacket)
	if err := proto.Unmarshal(wireBytes, packet); err != nil {
		t.Fatalf("unmarshal packet: %v", err)
	}
	challenge := packet.GetServerChallenge()
	if packet.GetWireMajor() != vector.WireMajor || packet.GetWireMinor() != vector.WireMinor ||
		challenge == nil || challenge.GetConnectionGeneration() != vector.ConnectionGeneration ||
		challenge.GetServerTimeMs() != vector.ServerTimeMS ||
		challenge.GetTrustManifestRevision() != vector.TrustManifestRevision ||
		challenge.GetCredentialStatusRevision() != vector.CredentialStatusRevision {
		t.Fatalf("decoded packet does not match vector: %#v", packet)
	}
	for label, value := range map[string]struct {
		bytes  []byte
		want   byte
		length int
	}{
		"packet ID":        {packet.GetPacketId(), vector.PacketIDByte, 16},
		"connection ID":    {challenge.GetConnectionId(), vector.ConnectionIDByte, 16},
		"server nonce":     {challenge.GetServerNonce(), vector.ServerNonceByte, 32},
		"server signature": {challenge.GetServerSignature(), vector.ServerSignatureByte, 64},
	} {
		if len(value.bytes) != value.length || !bytes.Equal(value.bytes, bytes.Repeat([]byte{value.want}, value.length)) {
			t.Fatalf("%s does not match vector", label)
		}
	}
	reencoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(packet)
	if err != nil {
		t.Fatalf("re-encode packet: %v", err)
	}
	if !bytes.Equal(reencoded, wireBytes) {
		t.Fatal("Go binding did not preserve deterministic packet bytes")
	}
}
