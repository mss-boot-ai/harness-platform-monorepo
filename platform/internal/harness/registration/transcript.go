package registration

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

const transcriptDomain = "MSS-HC-REGISTER-V1\x00"

type Assurance string

const (
	AssuranceWebEphemeral Assurance = "web-ephemeral"
	AssuranceWebSoftware  Assurance = "web-software"
)

type TranscriptInput struct {
	ChallengeID     domain.ID
	Challenge       []byte
	SigningJKT      string
	KEMJKT          string
	Origin          string
	EndpointName    string
	Assurance       Assurance
	SoftwareVersion string
}

func BuildTranscript(input TranscriptInput) ([]byte, error) {
	if input.ChallengeID.IsZero() || len(input.Challenge) != 32 {
		return nil, errors.New("HC registration challenge must contain a 16-byte ID and 32-byte value")
	}
	signingJKT, err := decodeJKT(input.SigningJKT)
	if err != nil {
		return nil, fmt.Errorf("decode signing JKT: %w", err)
	}
	kemJKT, err := decodeJKT(input.KEMJKT)
	if err != nil {
		return nil, fmt.Errorf("decode KEM JKT: %w", err)
	}
	if bytes.Equal(signingJKT, kemJKT) {
		return nil, errors.New("signing and KEM JKT must be distinct")
	}
	assuranceCode, err := encodeAssurance(input.Assurance)
	if err != nil {
		return nil, err
	}

	var output bytes.Buffer
	output.Grow(len(transcriptDomain) + 16 + 32 + 32 + 32 + len(input.Origin) + len(input.EndpointName) + len(input.SoftwareVersion) + 7)
	output.WriteString(transcriptDomain)
	output.Write(input.ChallengeID[:])
	output.Write(input.Challenge)
	output.Write(signingJKT)
	output.Write(kemJKT)
	if err := writeText(&output, input.Origin, 512, "origin"); err != nil {
		return nil, err
	}
	if err := writeText(&output, input.EndpointName, 120, "endpoint name"); err != nil {
		return nil, err
	}
	output.WriteByte(assuranceCode)
	if err := writeText(&output, input.SoftwareVersion, 64, "software version"); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func writeText(output *bytes.Buffer, value string, maxBytes int, label string) error {
	if !utf8.ValidString(value) || len(value) == 0 || len(value) > maxBytes || len(value) > int(^uint16(0)) {
		return fmt.Errorf("HC registration %s is invalid", label)
	}
	var length [2]byte
	binary.BigEndian.PutUint16(length[:], uint16(len(value)))
	output.Write(length[:])
	output.WriteString(value)
	return nil
}

func decodeJKT(value string) ([]byte, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("JKT must be an unpadded base64url SHA-256 value")
	}
	return decoded, nil
}

func encodeAssurance(value Assurance) (byte, error) {
	switch value {
	case AssuranceWebEphemeral:
		return 1, nil
	case AssuranceWebSoftware:
		return 2, nil
	default:
		return 0, errors.New("HC registration assurance is unsupported")
	}
}
