package gateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
)

type TrustBundle struct {
	Root      *ecdsa.PrivateKey
	Online    *ecdsa.PrivateKey
	Revision  uint64
	ExpiresAt time.Time
}

type TrustManifest struct {
	ExpiresAt          time.Time               `json:"expiresAt"`
	PayloadBase64URL   string                  `json:"payloadBase64Url"`
	Revision           uint64                  `json:"revision"`
	RootPublicJWK      awpcrypto.P256PublicJWK `json:"rootPublicJwk"`
	SignatureBase64URL string                  `json:"signatureBase64Url"`
}

type trustManifestPayload struct {
	ExpiresAtMS     int64                   `json:"expiresAtMs"`
	OnlinePublicJWK awpcrypto.P256PublicJWK `json:"onlinePublicJwk"`
	Revision        uint64                  `json:"revision"`
	RootJKT         string                  `json:"rootJkt"`
}

func NewEphemeralTrust(random io.Reader, now time.Time) (*TrustBundle, error) {
	if random == nil || now.IsZero() {
		return nil, errors.New("trust key randomness and current time are required")
	}
	root, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, fmt.Errorf("generate local root signer: %w", err)
	}
	online, err := ecdsa.GenerateKey(elliptic.P256(), random)
	if err != nil {
		return nil, fmt.Errorf("generate local online signer: %w", err)
	}
	return &TrustBundle{Root: root, Online: online, Revision: 1, ExpiresAt: now.Add(24 * time.Hour)}, nil
}

func (trust *TrustBundle) Manifest() (TrustManifest, error) {
	if trust == nil || trust.Root == nil || trust.Online == nil || trust.Revision == 0 || trust.ExpiresAt.IsZero() {
		return TrustManifest{}, errors.New("Gateway trust bundle is invalid")
	}
	rootJWK, err := awpcrypto.PublicJWK(&trust.Root.PublicKey)
	if err != nil {
		return TrustManifest{}, err
	}
	onlineJWK, err := awpcrypto.PublicJWK(&trust.Online.PublicKey)
	if err != nil {
		return TrustManifest{}, err
	}
	rootJKT, err := rootJWK.Thumbprint()
	if err != nil {
		return TrustManifest{}, err
	}
	payload, err := json.Marshal(trustManifestPayload{
		ExpiresAtMS: trust.ExpiresAt.UnixMilli(), OnlinePublicJWK: onlineJWK,
		Revision: trust.Revision, RootJKT: rootJKT,
	})
	if err != nil {
		return TrustManifest{}, fmt.Errorf("encode trust manifest: %w", err)
	}
	signature, err := awpcrypto.SignP1363LowS(trust.Root, payload)
	if err != nil {
		return TrustManifest{}, err
	}
	return TrustManifest{
		ExpiresAt: trust.ExpiresAt, PayloadBase64URL: base64.RawURLEncoding.EncodeToString(payload),
		Revision: trust.Revision, RootPublicJWK: rootJWK,
		SignatureBase64URL: base64.RawURLEncoding.EncodeToString(signature),
	}, nil
}
