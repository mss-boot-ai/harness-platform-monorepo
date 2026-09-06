package gateway

import (
	"bytes"
	"errors"
)

const (
	keyPackageSuiteID   = uint16(1)
	keyPackageSuiteName = "MSS-AWP-SUITE-0001"
)

func keyPackageInfo(
	sessionID []byte,
	generation uint64,
	abaEndpointID []byte,
	hcEndpointID []byte,
	policyRevision uint64,
) ([]byte, error) {
	if len(sessionID) != 16 || generation == 0 || len(abaEndpointID) != 16 ||
		len(hcEndpointID) != 16 || bytes.Equal(abaEndpointID, hcEndpointID) || policyRevision == 0 {
		return nil, errors.New("key package info input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-key-package-v1")
	output.Write(sessionID)
	writeUint64(&output, generation)
	output.Write(abaEndpointID)
	output.Write(hcEndpointID)
	output.Write([]byte{0, byte(keyPackageSuiteID)})
	writeUint64(&output, policyRevision)
	if output.Len() != 84 {
		return nil, errors.New("key package info length is invalid")
	}
	return output.Bytes(), nil
}

func keyPackageEnvelopeTranscript(
	keyPackageID []byte,
	sessionID []byte,
	generation uint64,
	abaEndpointID []byte,
	hcEndpointID []byte,
	credentialID []byte,
	policyRevision uint64,
	notBeforeMS int64,
	expiresAtMS int64,
	enc []byte,
	ciphertext []byte,
) ([]byte, error) {
	if len(keyPackageID) != 16 || len(sessionID) != 16 || generation == 0 ||
		len(abaEndpointID) != 16 || len(hcEndpointID) != 16 || len(credentialID) != 16 ||
		policyRevision == 0 || notBeforeMS <= 0 || expiresAtMS <= notBeforeMS ||
		len(enc) != 65 || len(ciphertext) == 0 || len(ciphertext) > 16*1024 {
		return nil, errors.New("key package envelope input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-key-package-envelope-v1")
	output.Write(keyPackageID)
	output.Write(sessionID)
	writeUint64(&output, generation)
	output.Write(abaEndpointID)
	output.Write(hcEndpointID)
	output.Write(credentialID)
	output.Write([]byte{0, byte(keyPackageSuiteID)})
	writeUint64(&output, policyRevision)
	writeInt64(&output, notBeforeMS)
	writeInt64(&output, expiresAtMS)
	writeUint32(&output, uint32(len(enc)))
	output.Write(enc)
	writeUint32(&output, uint32(len(ciphertext)))
	output.Write(ciphertext)
	return output.Bytes(), nil
}
