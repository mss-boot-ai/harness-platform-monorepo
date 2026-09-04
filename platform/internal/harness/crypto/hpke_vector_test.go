package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/hpke"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

type hpkeSessionVector struct {
	FixtureUse string `json:"fixtureUse"`
	SuiteID    uint16 `json:"suiteId"`
	HPKE       struct {
		KEMID  uint16 `json:"kemId"`
		KDFID  uint16 `json:"kdfId"`
		AEADID uint16 `json:"aeadId"`
	} `json:"hpke"`
	Recipient struct {
		PrivateD string `json:"privateD"`
	} `json:"recipient"`
	Context struct {
		SessionIDHex             string `json:"sessionIdHex"`
		Generation               uint64 `json:"generation"`
		SenderABAEndpointIDHex   string `json:"senderAbaEndpointIdHex"`
		RecipientHCEndpointIDHex string `json:"recipientHcEndpointIdHex"`
		PolicyRevision           uint64 `json:"policyRevision"`
	} `json:"context"`
	Material struct {
		SRKBase64URL                string `json:"srkBase64Url"`
		SessionNonceBase64URL       string `json:"sessionNonceBase64Url"`
		HCToABANoncePrefixBase64URL string `json:"hcToAbaNoncePrefixBase64Url"`
		ABAToHCNoncePrefixBase64URL string `json:"abaToHcNoncePrefixBase64Url"`
	} `json:"material"`
	InfoBase64URL        string `json:"infoBase64Url"`
	PlaintextBase64URL   string `json:"plaintextBase64Url"`
	EncBase64URL         string `json:"encBase64Url"`
	CiphertextBase64URL  string `json:"ciphertextBase64Url"`
	ContextHashBase64URL string `json:"contextHashBase64Url"`
	DirectionKeys        struct {
		HCToABABase64URL string `json:"hcToAbaBase64Url"`
		ABAToHCBase64URL string `json:"abaToHcBase64Url"`
	} `json:"directionKeys"`
	Nonces struct {
		HCToABASequence1Base64URL string `json:"hcToAbaSequence1Base64Url"`
		ABAToHCSequence1Base64URL string `json:"abaToHcSequence1Base64Url"`
	} `json:"nonces"`
}

func TestSuite0001HPKEAndDirectionKDFVector(t *testing.T) {
	vector := readHPKESessionVector(t)
	if vector.FixtureUse == "" || vector.SuiteID != 1 || vector.HPKE.KEMID != 0x0010 ||
		vector.HPKE.KDFID != 0x0001 || vector.HPKE.AEADID != 0x0002 {
		t.Fatalf("invalid Suite 0001 vector metadata: %#v", vector.HPKE)
	}
	sessionID := decodeHex(t, vector.Context.SessionIDHex, 16)
	abaID := decodeHex(t, vector.Context.SenderABAEndpointIDHex, 16)
	hcID := decodeHex(t, vector.Context.RecipientHCEndpointIDHex, 16)
	var info bytes.Buffer
	info.WriteString("mss-key-package-v1")
	info.Write(sessionID)
	writeHPKEUint64(&info, vector.Context.Generation)
	info.Write(abaID)
	info.Write(hcID)
	_ = binary.Write(&info, binary.BigEndian, vector.SuiteID)
	writeHPKEUint64(&info, vector.Context.PolicyRevision)
	if info.Len() != 84 || base64.RawURLEncoding.EncodeToString(info.Bytes()) != vector.InfoBase64URL {
		t.Fatalf("key package info length=%d", info.Len())
	}
	contextHash := sha256.Sum256(info.Bytes())
	if base64.RawURLEncoding.EncodeToString(contextHash[:]) != vector.ContextHashBase64URL {
		t.Fatal("key package context hash mismatch")
	}

	privateRaw := decodeBase64URL(t, vector.Recipient.PrivateD, 32)
	private, err := ecdh.P256().NewPrivateKey(privateRaw)
	if err != nil {
		t.Fatalf("parse recipient private key: %v", err)
	}
	hpkePrivate, err := hpke.NewDHKEMPrivateKey(private)
	if err != nil {
		t.Fatalf("wrap recipient private key: %v", err)
	}
	enc := decodeBase64URL(t, vector.EncBase64URL, 65)
	ciphertext := decodeBase64URL(t, vector.CiphertextBase64URL, 157)
	recipient, err := hpke.NewRecipient(enc, hpkePrivate, hpke.HKDFSHA256(), hpke.AES256GCM(), info.Bytes())
	if err != nil {
		t.Fatalf("create HPKE recipient: %v", err)
	}
	plaintext, err := recipient.Open(info.Bytes(), ciphertext)
	if err != nil {
		t.Fatalf("open HPKE key package: %v", err)
	}
	if base64.RawURLEncoding.EncodeToString(plaintext) != vector.PlaintextBase64URL {
		t.Fatal("HPKE plaintext mismatch")
	}

	srk := decodeBase64URL(t, vector.Material.SRKBase64URL, 32)
	sessionNonce := decodeBase64URL(t, vector.Material.SessionNonceBase64URL, 32)
	prk, err := hkdf.Extract(sha256.New, srk, sessionNonce)
	if err != nil {
		t.Fatalf("extract Session PRK: %v", err)
	}
	prefix := fmt.Sprintf(
		"mss-awp/v1/session/%s/generation/%d/endpoint/%s",
		vector.Context.SessionIDHex, vector.Context.Generation, vector.Context.RecipientHCEndpointIDHex,
	)
	hcToABA, err := hkdf.Expand(sha256.New, prk, prefix+"/hc-to-aba", 32)
	if err != nil {
		t.Fatalf("derive HC to ABA key: %v", err)
	}
	abaToHC, err := hkdf.Expand(sha256.New, prk, prefix+"/aba-to-hc", 32)
	if err != nil {
		t.Fatalf("derive ABA to HC key: %v", err)
	}
	if base64.RawURLEncoding.EncodeToString(hcToABA) != vector.DirectionKeys.HCToABABase64URL ||
		base64.RawURLEncoding.EncodeToString(abaToHC) != vector.DirectionKeys.ABAToHCBase64URL ||
		bytes.Equal(hcToABA, abaToHC) {
		t.Fatal("Session direction key mismatch")
	}
	if got := append(decodeBase64URL(t, vector.Material.HCToABANoncePrefixBase64URL, 4), make([]byte, 8)...); base64.RawURLEncoding.EncodeToString(setSequenceOne(got)) != vector.Nonces.HCToABASequence1Base64URL {
		t.Fatal("HC to ABA nonce mismatch")
	}
	if got := append(decodeBase64URL(t, vector.Material.ABAToHCNoncePrefixBase64URL, 4), make([]byte, 8)...); base64.RawURLEncoding.EncodeToString(setSequenceOne(got)) != vector.Nonces.ABAToHCSequence1Base64URL {
		t.Fatal("ABA to HC nonce mismatch")
	}
}

func readHPKESessionVector(t *testing.T) hpkeSessionVector {
	t.Helper()
	contents, err := os.ReadFile("../../../../protocol/testdata/v1/suite-0001-hpke-session.json")
	if err != nil {
		t.Fatalf("read HPKE vector: %v", err)
	}
	var vector hpkeSessionVector
	if err := json.Unmarshal(contents, &vector); err != nil {
		t.Fatalf("decode HPKE vector: %v", err)
	}
	return vector
}

func decodeBase64URL(t *testing.T, value string, length int) []byte {
	t.Helper()
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != length {
		t.Fatalf("decode base64url length=%d error=%v", len(decoded), err)
	}
	return decoded
}

func decodeHex(t *testing.T, value string, length int) []byte {
	t.Helper()
	decoded := make([]byte, length)
	if len(value) != length*2 {
		t.Fatalf("hex length=%d", len(value))
	}
	for index := range decoded {
		_, err := fmt.Sscanf(value[index*2:index*2+2], "%02x", &decoded[index])
		if err != nil {
			t.Fatalf("decode hex: %v", err)
		}
	}
	return decoded
}

func writeHPKEUint64(output *bytes.Buffer, value uint64) {
	_ = binary.Write(output, binary.BigEndian, value)
}

func setSequenceOne(nonce []byte) []byte {
	nonce[len(nonce)-1] = 1
	return nonce
}
