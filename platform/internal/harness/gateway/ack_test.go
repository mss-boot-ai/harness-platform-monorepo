package gateway

import (
	"testing"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/protocol/awpv1"
)

func TestACKTranscriptAndRangeValidation(t *testing.T) {
	ack := &awpv1.AckFrame{
		AckId: bytesOf(1, 16), ChannelId: bytesOf(2, 16), SessionId: bytesOf(3, 16),
		EndpointId: bytesOf(4, 16), AcknowledgedDirection: awpv1.Direction_DIRECTION_HC_TO_ABA,
		HighestContiguousSequence: 5, KeyGeneration: 2, CreatedAtMs: 1_800_000_000_000,
		ReceivedRanges: []*awpv1.SequenceRange{{Start: 7, End: 9}, {Start: 11, End: 12}},
	}
	transcript, err := ackTranscript(ack)
	if err != nil || len(transcript) != 146 {
		t.Fatalf("ACK transcript length=%d error=%v", len(transcript), err)
	}
	ranges, err := ackRanges(ack.GetHighestContiguousSequence(), ack.GetReceivedRanges())
	if err != nil || len(ranges) != 2 || ranges[1].End != 12 {
		t.Fatalf("ACK ranges=%#v error=%v", ranges, err)
	}
	ack.ReceivedRanges[1].Start = 9
	if _, err := ackRanges(ack.GetHighestContiguousSequence(), ack.GetReceivedRanges()); err == nil {
		t.Fatal("overlapping ACK ranges were accepted")
	}
}

func bytesOf(value byte, count int) []byte {
	result := make([]byte, count)
	for index := range result {
		result[index] = value
	}
	return result
}
