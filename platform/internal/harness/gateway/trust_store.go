package gateway

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const trustStateVersion = 1
const maxTrustStateBytes = 4096

type trustState struct {
	Version  int    `json:"version"`
	RootD    string `json:"rootD"`
	OnlineD  string `json:"onlineD"`
	Revision uint64 `json:"revision"`
}

// LoadOrCreateTrustState owns a local signer file for the single-node MVP.
// Production KMS/HSM adapters can replace this loader without changing AWP.
func LoadOrCreateTrustState(path string, random io.Reader, now time.Time) (*TrustBundle, error) {
	if path == "" || random == nil || now.IsZero() || !filepath.IsAbs(path) && filepath.Clean(path) == "." {
		return nil, errors.New("Gateway trust state path, randomness, and current time are required")
	}
	path = filepath.Clean(path)
	directory := filepath.Dir(path)
	if err := ensurePrivateDirectory(directory); err != nil {
		return nil, err
	}
	state, err := readTrustState(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		root, generateErr := ecdsa.GenerateKey(elliptic.P256(), random)
		if generateErr != nil {
			return nil, fmt.Errorf("generate Gateway root signer: %w", generateErr)
		}
		online, generateErr := ecdsa.GenerateKey(elliptic.P256(), random)
		if generateErr != nil {
			return nil, fmt.Errorf("generate Gateway online signer: %w", generateErr)
		}
		state = trustState{
			Version: trustStateVersion, RootD: encodeScalar(root.D), OnlineD: encodeScalar(online.D), Revision: 1,
		}
	case err != nil:
		return nil, err
	default:
		if state.Revision == ^uint64(0) {
			return nil, errors.New("Gateway trust revision is exhausted")
		}
		state.Revision++
	}
	if err := writeTrustState(path, state); err != nil {
		return nil, err
	}
	root, err := keyFromScalar(state.RootD)
	if err != nil {
		return nil, fmt.Errorf("load Gateway root signer: %w", err)
	}
	online, err := keyFromScalar(state.OnlineD)
	if err != nil {
		return nil, fmt.Errorf("load Gateway online signer: %w", err)
	}
	if root.D.Cmp(online.D) == 0 {
		return nil, errors.New("Gateway root and online signing keys must be distinct")
	}
	return &TrustBundle{Root: root, Online: online, Revision: state.Revision, ExpiresAt: now.Add(24 * time.Hour)}, nil
}

func ensurePrivateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create Gateway trust directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect Gateway trust directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("Gateway trust directory must be a real directory")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return errors.New("Gateway trust directory permissions are too broad")
	}
	return nil
}

func readTrustState(path string) (trustState, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return trustState{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return trustState{}, errors.New("Gateway trust state must be a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return trustState{}, errors.New("Gateway trust state permissions are too broad")
	}
	file, err := os.Open(path)
	if err != nil {
		return trustState{}, fmt.Errorf("open Gateway trust state: %w", err)
	}
	defer file.Close()
	contents, err := io.ReadAll(io.LimitReader(file, maxTrustStateBytes+1))
	if err != nil {
		return trustState{}, fmt.Errorf("read Gateway trust state: %w", err)
	}
	if len(contents) == 0 || len(contents) > maxTrustStateBytes {
		return trustState{}, errors.New("Gateway trust state length is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	var state trustState
	if err := decoder.Decode(&state); err != nil {
		return trustState{}, errors.New("Gateway trust state is invalid")
	}
	if state.Version != trustStateVersion || state.Revision == 0 {
		return trustState{}, errors.New("Gateway trust state version or revision is invalid")
	}
	if _, err := keyFromScalar(state.RootD); err != nil {
		return trustState{}, errors.New("Gateway root signer is invalid")
	}
	if _, err := keyFromScalar(state.OnlineD); err != nil {
		return trustState{}, errors.New("Gateway online signer is invalid")
	}
	return state, nil
}

func writeTrustState(path string, state trustState) error {
	contents, err := json.Marshal(state)
	if err != nil {
		return errors.New("encode Gateway trust state")
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".gateway-trust-*")
	if err != nil {
		return fmt.Errorf("create temporary Gateway trust state: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict Gateway trust state: %w", err)
	}
	if _, err := temporary.Write(contents); err != nil {
		return fmt.Errorf("write Gateway trust state: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync Gateway trust state: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close Gateway trust state: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("publish Gateway trust state: %w", err)
	}
	cleanup = false
	return nil
}

func encodeScalar(value *big.Int) string {
	encoded := make([]byte, 32)
	value.FillBytes(encoded)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func keyFromScalar(value string) (*ecdsa.PrivateKey, error) {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return nil, errors.New("signing scalar must be 32-byte base64url")
	}
	d := new(big.Int).SetBytes(decoded)
	curve := elliptic.P256()
	if d.Sign() <= 0 || d.Cmp(curve.Params().N) >= 0 {
		return nil, errors.New("signing scalar is outside P-256")
	}
	x, y := curve.ScalarBaseMult(decoded)
	return &ecdsa.PrivateKey{PublicKey: ecdsa.PublicKey{Curve: curve, X: x, Y: y}, D: d}, nil
}
