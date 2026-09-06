package gateway

import (
	"errors"
	"testing"
	"time"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestConnectionDirectoryFencesStaleGeneration(t *testing.T) {
	directory := newMemoryConnectionDirectory()
	endpointID := gatewayID(90)
	first := newActiveConnection(endpointID, 2, [16]byte{1}, [32]byte{}, nil)
	if replaced, active := directory.activate(first); !active || replaced != nil {
		t.Fatalf("first activation active=%v replaced=%v", active, replaced)
	}
	stale := newActiveConnection(endpointID, 1, [16]byte{2}, [32]byte{}, nil)
	if current, active := directory.activate(stale); active || current != first {
		t.Fatalf("stale activation active=%v current=%v", active, current)
	}
	newer := newActiveConnection(endpointID, 3, [16]byte{3}, [32]byte{}, nil)
	if replaced, active := directory.activate(newer); !active || replaced != first {
		t.Fatalf("newer activation active=%v replaced=%v", active, replaced)
	}
	directory.remove(first)
	if !directory.online(endpointID) {
		t.Fatal("removing fenced connection removed the active generation")
	}
	directory.remove(newer)
	if directory.online(endpointID) {
		t.Fatal("active connection remained after removal")
	}
}

func TestConnectionQueueIsBoundedAndCopiesPackets(t *testing.T) {
	connection := newActiveConnection(domain.ID{1}, 1, [16]byte{1}, [32]byte{}, nil)
	packet := []byte{1, 2, 3}
	if err := connection.enqueue(packet); err != nil {
		t.Fatalf("enqueue packet: %v", err)
	}
	packet[0] = 9
	queued := <-connection.outbound
	connection.queuedBytes.Add(-int64(len(queued)))
	if queued[0] != 1 {
		t.Fatalf("queued packet shared caller storage: %v", queued)
	}
	for index := 0; index < maxInflightFrames; index++ {
		if err := connection.enqueue([]byte{byte(index)}); err != nil {
			t.Fatalf("fill queue index=%d: %v", index, err)
		}
	}
	if err := connection.enqueue([]byte{1}); !errors.Is(err, errConnectionBackpressure) {
		t.Fatalf("full queue error=%v", err)
	}
	connection.close(0, "test")
	if err := connection.enqueue([]byte{1}); !errors.Is(err, errConnectionOffline) {
		t.Fatalf("closed queue error=%v", err)
	}
}

func TestConnectionDirectoryAllocatesMonotonicControlSequence(t *testing.T) {
	directory := newMemoryConnectionDirectory()
	connection := newActiveConnection(domain.ID{2}, 1, [16]byte{1}, [32]byte{}, nil)
	if _, active := directory.activate(connection); !active {
		t.Fatal("connection did not activate")
	}
	var sequences []uint64
	for index := 0; index < 2; index++ {
		err := directory.sendNextControl(connection.endpointID, func(sequence uint64) ([]byte, error) {
			sequences = append(sequences, sequence)
			return []byte{byte(sequence)}, nil
		})
		if err != nil {
			t.Fatalf("send control %d: %v", index, err)
		}
	}
	if len(sequences) != 2 || sequences[0] != 1 || sequences[1] != 2 {
		t.Fatalf("control sequences=%v", sequences)
	}
}

func TestConnectionRejectsInboundControlReplayAndGap(t *testing.T) {
	connection := newActiveConnection(domain.ID{3}, 1, [16]byte{1}, [32]byte{}, nil)
	if !connection.acceptInboundControlSequence(1) {
		t.Fatal("first inbound control sequence was rejected")
	}
	if connection.acceptInboundControlSequence(1) {
		t.Fatal("inbound control replay was accepted")
	}
	if connection.acceptInboundControlSequence(3) {
		t.Fatal("inbound control gap was accepted")
	}
	if !connection.acceptInboundControlSequence(2) {
		t.Fatal("next inbound control sequence was rejected after gap")
	}
}

func TestConnectionDirectoryWaitsForInflightPacketBeforeActivatingNewGeneration(t *testing.T) {
	directory := newMemoryConnectionDirectory()
	endpointID := gatewayID(91)
	first := newActiveConnection(endpointID, 1, [16]byte{1}, [32]byte{1}, nil)
	if _, active := directory.activate(first); !active {
		t.Fatal("first connection did not activate")
	}

	started := make(chan struct{})
	release := make(chan struct{})
	processed := make(chan error, 1)
	go func() {
		processed <- directory.withCurrent(first, func() error {
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	newer := newActiveConnection(endpointID, 2, [16]byte{2}, [32]byte{2}, nil)
	activated := make(chan bool, 1)
	go func() {
		_, ok := directory.activate(newer)
		activated <- ok
	}()

	select {
	case <-activated:
		t.Fatal("new generation activated before the old packet finished")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-processed; err != nil {
		t.Fatalf("process old packet: %v", err)
	}
	if !<-activated {
		t.Fatal("new generation did not activate after the old packet finished")
	}
	if err := directory.withCurrent(first, func() error { return nil }); !errors.Is(err, errConnectionFenced) {
		t.Fatalf("fenced connection error = %v", err)
	}
	if err := directory.withCurrent(newer, func() error { return nil }); err != nil {
		t.Fatalf("current connection was rejected: %v", err)
	}
}

func TestConnectionDirectoryDoesNotBurnControlSequenceOnFailure(t *testing.T) {
	directory := newMemoryConnectionDirectory()
	connection := newActiveConnection(domain.ID{4}, 1, [16]byte{1}, [32]byte{}, nil)
	if _, active := directory.activate(connection); !active {
		t.Fatal("connection did not activate")
	}

	buildFailure := errors.New("build failed")
	if err := directory.sendNextControl(connection.endpointID, func(sequence uint64) ([]byte, error) {
		if sequence != 1 {
			t.Fatalf("failed build sequence = %d", sequence)
		}
		return nil, buildFailure
	}); !errors.Is(err, buildFailure) {
		t.Fatalf("build failure = %v", err)
	}
	if sequence := connection.controlSequence.Load(); sequence != 0 {
		t.Fatalf("failed build burned sequence %d", sequence)
	}

	for index := 0; index < maxInflightFrames; index++ {
		if err := connection.enqueue([]byte{byte(index)}); err != nil {
			t.Fatalf("fill queue index=%d: %v", index, err)
		}
	}
	if err := directory.sendNextControl(connection.endpointID, func(sequence uint64) ([]byte, error) {
		if sequence != 1 {
			t.Fatalf("backpressured build sequence = %d", sequence)
		}
		return []byte{1}, nil
	}); !errors.Is(err, errConnectionBackpressure) {
		t.Fatalf("backpressure failure = %v", err)
	}
	if sequence := connection.controlSequence.Load(); sequence != 0 {
		t.Fatalf("backpressure burned sequence %d", sequence)
	}

	queued := <-connection.outbound
	connection.queuedBytes.Add(-int64(len(queued)))
	if err := directory.sendNextControl(connection.endpointID, func(sequence uint64) ([]byte, error) {
		if sequence != 1 {
			t.Fatalf("successful sequence = %d", sequence)
		}
		return []byte{1}, nil
	}); err != nil {
		t.Fatalf("send after failure: %v", err)
	}
	if sequence := connection.controlSequence.Load(); sequence != 1 {
		t.Fatalf("committed sequence = %d", sequence)
	}
}
