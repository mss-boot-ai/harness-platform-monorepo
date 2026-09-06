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
	errConnectionFenced       = errors.New("endpoint connection was fenced by a newer generation")
)

type connectionDirectory interface {
	activate(*activeConnection) (*activeConnection, bool)
	remove(*activeConnection)
	send(domain.ID, []byte) error
	sendNextControl(domain.ID, func(uint64) ([]byte, error)) error
	online(domain.ID) bool
	withCurrent(*activeConnection, func() error) error
}

type memoryConnectionDirectory struct {
	mu       sync.RWMutex
	byTarget map[domain.ID]*connectionLane
}

type connectionLane struct {
	mu      sync.Mutex
	current atomic.Pointer[activeConnection]
}

type activeConnection struct {
	endpointID             domain.ID
	generation             uint64
	connectionID           [16]byte
	fencingToken           [32]byte
	socket                 *websocket.Conn
	outbound               chan []byte
	done                   chan struct{}
	queuedBytes            atomic.Int64
	controlSequence        atomic.Uint64
	controlMu              sync.Mutex
	inboundControlSequence atomic.Uint64
	closeOnce              sync.Once
}

func newMemoryConnectionDirectory() *memoryConnectionDirectory {
	return &memoryConnectionDirectory{byTarget: make(map[domain.ID]*connectionLane)}
}

func newActiveConnection(
	endpointID domain.ID,
	generation uint64,
	connectionID [16]byte,
	fencingToken [32]byte,
	socket *websocket.Conn,
) *activeConnection {
	return &activeConnection{
		endpointID: endpointID, generation: generation, connectionID: connectionID,
		fencingToken: fencingToken, socket: socket,
		outbound: make(chan []byte, maxInflightFrames), done: make(chan struct{}),
	}
}

func (directory *memoryConnectionDirectory) lane(endpointID domain.ID, create bool) *connectionLane {
	directory.mu.RLock()
	lane := directory.byTarget[endpointID]
	directory.mu.RUnlock()
	if lane != nil || !create {
		return lane
	}
	directory.mu.Lock()
	defer directory.mu.Unlock()
	lane = directory.byTarget[endpointID]
	if lane == nil {
		lane = new(connectionLane)
		directory.byTarget[endpointID] = lane
	}
	return lane
}

func (directory *memoryConnectionDirectory) activate(candidate *activeConnection) (*activeConnection, bool) {
	if candidate == nil || candidate.endpointID.IsZero() || candidate.generation == 0 {
		return nil, false
	}
	lane := directory.lane(candidate.endpointID, true)
	lane.mu.Lock()
	defer lane.mu.Unlock()
	current := lane.current.Load()
	if current != nil && current.generation >= candidate.generation {
		return current, false
	}
	lane.current.Store(candidate)
	return current, true
}

func (directory *memoryConnectionDirectory) remove(candidate *activeConnection) {
	if candidate == nil {
		return
	}
	lane := directory.lane(candidate.endpointID, false)
	if lane == nil {
		return
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.current.Load() == candidate {
		lane.current.Store(nil)
	}
}

func (directory *memoryConnectionDirectory) withCurrent(candidate *activeConnection, operation func() error) error {
	if candidate == nil || operation == nil {
		return errConnectionFenced
	}
	lane := directory.lane(candidate.endpointID, false)
	if lane == nil {
		return errConnectionFenced
	}
	lane.mu.Lock()
	defer lane.mu.Unlock()
	if lane.current.Load() != candidate {
		return errConnectionFenced
	}
	return operation()
}

func (directory *memoryConnectionDirectory) send(endpointID domain.ID, packet []byte) error {
	lane := directory.lane(endpointID, false)
	if lane == nil {
		return errConnectionOffline
	}
	connection := lane.current.Load()
	if connection == nil {
		return errConnectionOffline
	}
	return connection.enqueue(packet)
}

func (directory *memoryConnectionDirectory) sendNextControl(
	endpointID domain.ID,
	build func(uint64) ([]byte, error),
) error {
	if build == nil {
		return errConnectionBackpressure
	}
	lane := directory.lane(endpointID, false)
	if lane == nil {
		return errConnectionOffline
	}
	connection := lane.current.Load()
	if connection == nil {
		return errConnectionOffline
	}
	connection.controlMu.Lock()
	defer connection.controlMu.Unlock()
	current := connection.controlSequence.Load()
	if current == ^uint64(0) {
		return errConnectionBackpressure
	}
	sequence := current + 1
	packet, err := build(sequence)
	if err != nil {
		return err
	}
	if err := connection.enqueue(packet); err != nil {
		return err
	}
	connection.controlSequence.Store(sequence)
	return nil
}

func (directory *memoryConnectionDirectory) online(endpointID domain.ID) bool {
	lane := directory.lane(endpointID, false)
	return lane != nil && lane.current.Load() != nil
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

func (connection *activeConnection) acceptInboundControlSequence(sequence uint64) bool {
	for {
		current := connection.inboundControlSequence.Load()
		if sequence != current+1 {
			return false
		}
		if connection.inboundControlSequence.CompareAndSwap(current, sequence) {
			return true
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
