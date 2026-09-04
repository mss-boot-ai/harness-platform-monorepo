// Package crypto implements the small, shared cryptographic contract used by
// Harness endpoint identity and DPoP. It intentionally does not own keys.
package crypto

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
)

const p256CoordinateBytes = 32

var rawBase64URL = base64.RawURLEncoding.Strict()

type P256PublicJWK struct {
	Curve   string `json:"crv"`
	KeyType string `json:"kty"`
	X       string `json:"x"`
	Y       string `json:"y"`
}

func PublicJWK(publicKey *ecdsa.PublicKey) (P256PublicJWK, error) {
	if publicKey == nil || publicKey.Curve != elliptic.P256() || publicKey.X == nil || publicKey.Y == nil ||
		!publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return P256PublicJWK{}, errors.New("valid P-256 public key is required")
	}
	return P256PublicJWK{
		Curve: "P-256", KeyType: "EC",
		X: base64.RawURLEncoding.EncodeToString(fixedCoordinate(publicKey.X)),
		Y: base64.RawURLEncoding.EncodeToString(fixedCoordinate(publicKey.Y)),
	}, nil
}

func ParseP256PublicJWK(input []byte) (P256PublicJWK, error) {
	var jwk P256PublicJWK
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&jwk); err != nil {
		return P256PublicJWK{}, fmt.Errorf("decode P-256 public JWK: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return P256PublicJWK{}, errors.New("P-256 public JWK must contain one JSON value")
	}
	if _, err := jwk.PublicKey(); err != nil {
		return P256PublicJWK{}, err
	}
	return jwk, nil
}

func (jwk P256PublicJWK) PublicKey() (*ecdsa.PublicKey, error) {
	if jwk.Curve != "P-256" || jwk.KeyType != "EC" {
		return nil, errors.New("JWK must use EC P-256")
	}
	xBytes, err := decodeCoordinate("x", jwk.X)
	if err != nil {
		return nil, err
	}
	yBytes, err := decodeCoordinate("y", jwk.Y)
	if err != nil {
		return nil, err
	}
	x := new(big.Int).SetBytes(xBytes)
	y := new(big.Int).SetBytes(yBytes)
	curve := elliptic.P256()
	if !curve.IsOnCurve(x, y) {
		return nil, errors.New("JWK point is not on P-256")
	}
	return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
}

func (jwk P256PublicJWK) Thumbprint() (string, error) {
	if _, err := jwk.PublicKey(); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Curve   string `json:"crv"`
		KeyType string `json:"kty"`
		X       string `json:"x"`
		Y       string `json:"y"`
	}{jwk.Curve, jwk.KeyType, jwk.X, jwk.Y})
	if err != nil {
		return "", fmt.Errorf("marshal JWK thumbprint input: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}

func AccessTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func VerifyP1363LowS(publicKey *ecdsa.PublicKey, message, signature []byte) bool {
	if publicKey == nil || publicKey.Curve != elliptic.P256() || len(signature) != 64 {
		return false
	}
	r := new(big.Int).SetBytes(signature[:p256CoordinateBytes])
	s := new(big.Int).SetBytes(signature[p256CoordinateBytes:])
	order := publicKey.Curve.Params().N
	if r.Sign() <= 0 || s.Sign() <= 0 || r.Cmp(order) >= 0 || s.Cmp(order) >= 0 {
		return false
	}
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(order), 1)
	if s.Cmp(halfOrder) > 0 {
		return false
	}
	digest := sha256.Sum256(message)
	return ecdsa.Verify(publicKey, digest[:], r, s)
}

func SignP1363LowS(privateKey *ecdsa.PrivateKey, message []byte) ([]byte, error) {
	if privateKey == nil || privateKey.Curve != elliptic.P256() || privateKey.D == nil {
		return nil, errors.New("P-256 private key is required")
	}
	digest := sha256.Sum256(message)
	r, s, err := ecdsa.Sign(rand.Reader, privateKey, digest[:])
	if err != nil {
		return nil, fmt.Errorf("sign P-256 message: %w", err)
	}
	order := privateKey.Curve.Params().N
	halfOrder := new(big.Int).Rsh(new(big.Int).Set(order), 1)
	if s.Cmp(halfOrder) > 0 {
		s.Sub(order, s)
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:p256CoordinateBytes])
	s.FillBytes(signature[p256CoordinateBytes:])
	return signature, nil
}

func decodeCoordinate(name, value string) ([]byte, error) {
	decoded, err := rawBase64URL.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode JWK %s coordinate: %w", name, err)
	}
	if len(decoded) != p256CoordinateBytes {
		return nil, fmt.Errorf("JWK %s coordinate must be 32 bytes", name)
	}
	return decoded, nil
}

func fixedCoordinate(value *big.Int) []byte {
	result := make([]byte, p256CoordinateBytes)
	value.FillBytes(result)
	return result
}
