package gateway

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mss-boot-ai/harness-platform-monorepo/platform/internal/harness/domain"
)

const maxConnectionQueueBytes = 8 << 20

var (
	errConnectionOffline      = errors.New("endpoint connection is offline")
	errConnectionBackpressure = errors.New("endpoint connection send queue is full")
)

type connectionDirectory interface {
	activate(*activeConnection) (*activeConnection, bool)
	remove(*activeConnection)
	send(domain.ID, []byte) error
	online(domain.ID) bool
}

type memoryConnectionDirectory struct {
	mu       sync.RWMutex
	byTarget map[domain.ID]*activeConnection
}

type activeConnection struct {
	endpointID   domain.ID
	generation   uint64
	connectionID [16]byte
	socket       *websocket.Conn
	outbound     chan []byte
	done         chan struct{}
	queuedBytes  atomic.Int64
	closeOnce    sync.Once
}

func newMemoryConnectionDirectory() *memoryConnectionDirectory {
	return &memoryConnectionDirectory{byTarget: make(map[domain.ID]*activeConnection)}
}

func newActiveConnection(
	endpointID domain.ID,
	generation uint64,
	connectionID [16]byte,
	socket *websocket.Conn,
) *activeConnection {
	return &activeConnection{
		endpointID: endpointID, generation: generation, connectionID: connectionID, socket: socket,
		outbound: make(chan []byte, maxInflightFrames), done: make(chan struct{}),
	}
}

func (directory *memoryConnectionDirectory) activate(candidate *activeConnection) (*activeConnection, bool) {
	if candidate == nil || candidate.endpointID.IsZero() || candidate.generation == 0 {
		return nil, false
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	current := directory.byTarget[candidate.endpointID]
	if current != nil && current.generation >= candidate.generation {
		return current, false
	}
	directory.byTarget[candidate.endpointID] = candidate
	return current, true
}

func (directory *memoryConnectionDirectory) remove(candidate *activeConnection) {
	if candidate == nil {
		return
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	current := directory.byTarget[candidate.endpointID]
	if current == candidate {
		delete(directory.byTarget, candidate.endpointID)
	}
}

func (directory *memoryConnectionDirectory) send(endpointID domain.ID, packet []byte) error {
	directory.mu.RLock()
	connection := directory.byTarget[endpointID]
	directory.mu.RUnlock()
	if connection == nil {
		return errConnectionOffline
	}
	return connection.enqueue(packet)
}

func (directory *memoryConnectionDirectory) online(endpointID domain.ID) bool {
	directory.mu.RLock()
	defer directory.mu.RUnlock()
	return directory.byTarget[endpointID] != nil
}

func (connection *activeConnection) enqueue(packet []byte) error {
	if len(packet) == 0 || len(packet) > maxWirePacketBytes {
		return errConnectionBackpressure
	}
	select {
	case <-connection.done:
		return errConnectionOffline
	default:
	}
	length := int64(len(packet))
	for {
		current := connection.queuedBytes.Load()
		if current+length > maxConnectionQueueBytes {
			return errConnectionBackpressure
		}
		if connection.queuedBytes.CompareAndSwap(current, current+length) {
			break
		}
	}
	copyOfPacket := append([]byte(nil), packet...)
	select {
	case connection.outbound <- copyOfPacket:
		return nil
	case <-connection.done:
		connection.queuedBytes.Add(-length)
		return errConnectionOffline
	default:
		connection.queuedBytes.Add(-length)
		return errConnectionBackpressure
	}
}

func (connection *activeConnection) runWriter() {
	ticker := time.NewTicker(time.Duration(heartbeatIntervalMS) * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case packet := <-connection.outbound:
			connection.queuedBytes.Add(-int64(len(packet)))
			if connection.socket == nil {
				connection.close(websocket.CloseInternalServerErr, "connection unavailable")
				return
			}
			_ = connection.socket.SetWriteDeadline(time.Now().Add(challengeTimeout))
			if err := connection.socket.WriteMessage(websocket.BinaryMessage, packet); err != nil {
				connection.close(websocket.CloseGoingAway, "connection write failed")
				return
			}
		case <-ticker.C:
			if connection.socket == nil || connection.socket.WriteControl(
				websocket.PingMessage, nil, time.Now().Add(challengeTimeout),
			) != nil {
				connection.close(websocket.CloseGoingAway, "heartbeat failed")
				return
			}
		case <-connection.done:
			return
		}
	}
}

func (connection *activeConnection) close(code int, reason string) {
	connection.closeOnce.Do(func() {
		close(connection.done)
		if connection.socket == nil {
			return
		}
		_ = connection.socket.WriteControl(
			websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason),
			time.Now().Add(time.Second),
		)
		_ = connection.socket.Close()
	})
}
