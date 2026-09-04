package gateway

import (
	"errors"
	"testing"

	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

func TestConnectionDirectoryFencesStaleGeneration(t *testing.T) {
	directory := newMemoryConnectionDirectory()
	endpointID := gatewayID(90)
	first := newActiveConnection(endpointID, 2, [16]byte{1}, nil)
	if replaced, active := directory.activate(first); !active || replaced != nil {
		t.Fatalf("first activation active=%v replaced=%v", active, replaced)
	}
	stale := newActiveConnection(endpointID, 1, [16]byte{2}, nil)
	if current, active := directory.activate(stale); active || current != first {
		t.Fatalf("stale activation active=%v current=%v", active, current)
	}
	newer := newActiveConnection(endpointID, 3, [16]byte{3}, nil)
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
	connection := newActiveConnection(domain.ID{1}, 1, [16]byte{1}, nil)
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
