package gateway

import (
	"bytes"
	"encoding/binary"
	"errors"
)

func serverChallengeTranscript(
	connectionID []byte,
	connectionGeneration uint64,
	serverNonce []byte,
	serverTimeMS int64,
	manifestRevision uint64,
	credentialRevision uint64,
	endpointID []byte,
) ([]byte, error) {
	if len(connectionID) != 16 || connectionGeneration == 0 || len(serverNonce) != 32 || len(endpointID) != 16 {
		return nil, errors.New("server challenge transcript input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-awp-server-challenge-v1")
	output.Write(connectionID)
	writeUint64(&output, connectionGeneration)
	output.Write(serverNonce)
	writeInt64(&output, serverTimeMS)
	writeUint64(&output, manifestRevision)
	writeUint64(&output, credentialRevision)
	output.Write(endpointID)
	return output.Bytes(), nil
}

func clientChallengeTranscript(
	connectionID []byte,
	connectionGeneration uint64,
	serverNonce []byte,
	clientNonce []byte,
	endpointID []byte,
	credentialID []byte,
	subprotocol string,
	manifestRevision uint64,
	credentialRevision uint64,
) ([]byte, error) {
	if len(connectionID) != 16 || connectionGeneration == 0 || len(serverNonce) != 32 ||
		len(clientNonce) != 32 || len(endpointID) != 16 || len(credentialID) != 16 || subprotocol != protocolName {
		return nil, errors.New("client challenge transcript input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-awp-client-challenge-v1")
	output.Write(connectionID)
	writeUint64(&output, connectionGeneration)
	output.Write(serverNonce)
	output.Write(clientNonce)
	output.Write(endpointID)
	output.Write(credentialID)
	output.WriteString(subprotocol)
	writeUint64(&output, manifestRevision)
	writeUint64(&output, credentialRevision)
	return output.Bytes(), nil
}

func connectionReadyTranscript(
	connectionID []byte,
	connectionGeneration uint64,
	fencingToken []byte,
	readyAtMS int64,
	maxPacketBytes uint32,
	maxInflightFrames uint32,
	heartbeatIntervalMS uint32,
	endpointID []byte,
) ([]byte, error) {
	if len(connectionID) != 16 || connectionGeneration == 0 || len(fencingToken) != 32 || len(endpointID) != 16 ||
		maxPacketBytes == 0 || maxInflightFrames == 0 || heartbeatIntervalMS == 0 {
		return nil, errors.New("connection ready transcript input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-awp-connection-ready-v1")
	output.Write(connectionID)
	writeUint64(&output, connectionGeneration)
	output.Write(fencingToken)
	writeInt64(&output, readyAtMS)
	writeUint32(&output, maxPacketBytes)
	writeUint32(&output, maxInflightFrames)
	writeUint32(&output, heartbeatIntervalMS)
	output.Write(endpointID)
	return output.Bytes(), nil
}

func controlTranscript(
	messageID []byte,
	senderEndpointID []byte,
	receiverEndpointID []byte,
	sequence uint64,
	createdAtMS int64,
	controlType uint32,
	payload []byte,
) ([]byte, error) {
	if len(messageID) != 16 || len(senderEndpointID) != 16 || len(receiverEndpointID) != 16 ||
		sequence == 0 || controlType == 0 || len(payload) == 0 || len(payload) > maxWirePacketBytes {
		return nil, errors.New("control transcript input is invalid")
	}
	var output bytes.Buffer
	output.WriteString("mss-awp-control-v1")
	output.Write(messageID)
	output.Write(senderEndpointID)
	output.Write(receiverEndpointID)
	writeUint64(&output, sequence)
	writeInt64(&output, createdAtMS)
	writeUint32(&output, controlType)
	writeUint32(&output, uint32(len(payload)))
	output.Write(payload)
	return output.Bytes(), nil
}

func writeUint64(output *bytes.Buffer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	output.Write(encoded[:])
}

func writeInt64(output *bytes.Buffer, value int64) { writeUint64(output, uint64(value)) }

func writeUint32(output *bytes.Buffer, value uint32) {
	var encoded [4]byte
	binary.BigEndian.PutUint32(encoded[:], value)
	output.Write(encoded[:])
}
