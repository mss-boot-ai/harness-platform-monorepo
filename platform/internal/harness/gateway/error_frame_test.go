package gateway

import (
	"testing"

	awpcrypto "github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/crypto"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
)

func TestErrorFrameTranscriptSignatureRejectsTampering(t *testing.T) {
	_, key := gatewaySigningKey(17)
	frame := &awpv1.ErrorFrame{
		ErrorId: bytesOf(1, 16), RelatedMessageId: bytesOf(2, 16),
		Code:        awpv1.ErrorCode_ERROR_CODE_LOCAL_DISPATCH_UNCERTAIN,
		SafeMessage: uncertainSafeMessage,
	}
	transcript, err := errorFrameTranscript(frame)
	if err != nil || len(transcript) != 104 {
		t.Fatalf("ErrorFrame transcript length=%d error=%v", len(transcript), err)
	}
	frame.Signature, err = awpcrypto.SignP1363LowS(key, transcript)
	if err != nil || !awpcrypto.VerifyP1363LowS(&key.PublicKey, transcript, frame.Signature) {
		t.Fatalf("ErrorFrame signature error=%v", err)
	}
	frame.SafeMessage += "!"
	tampered, err := errorFrameTranscript(frame)
	if err != nil {
		t.Fatalf("tampered ErrorFrame transcript: %v", err)
	}
	if awpcrypto.VerifyP1363LowS(&key.PublicKey, tampered, frame.Signature) {
		t.Fatal("tampered ErrorFrame signature was accepted")
	}
}
