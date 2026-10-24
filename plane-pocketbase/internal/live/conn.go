package live

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// writeWait bounds a single frame write. A peer that cannot absorb one
	// frame in 10s is not coming back.
	writeWait = 10 * time.Second
	// pongWait is how long a connection may stay silent before it is reaped.
	// The client sends pongs automatically in response to our pings.
	pongWait = 90 * time.Second
	// pingPeriod must be shorter than pongWait, otherwise the deadline fires
	// before the pong that would have refreshed it.
	pingPeriod = 30 * time.Second
	// maxMessageSize caps a single frame. Page edits are small; a larger frame
	// means a client is misbehaving, and gorilla closes the connection for us.
	maxMessageSize = 8 << 20
	// sendQueue is per-connection. A document edit produces one message per
	// keystroke burst, so this absorbs a normal burst without blocking the room.
	sendQueue = 256
)

// conn is one WebSocket peer.
//
// Writes go through a single goroutine (writeLoop) because gorilla forbids
// concurrent writers, while the relay broadcasts from whichever goroutine is
// handling another peer's frame.
type conn struct {
	ws   *websocket.Conn
	room *room
	// user is PocketBase's record id; userAPIID is the `uuid` the REST API and
	// therefore the frontend know the user by. Both are accepted in the Auth
	// frame, because which one the editor sends depends on where it read the
	// current user from.
	user      string
	userAPIID string

	send chan []byte
	// ids are the awareness clientIDs this connection has announced, so their
	// presence can be retracted when it drops.
	ids map[uint32]struct{}

	// joinedAt and synced decide whether this peer may be asked for a document
	// snapshot: it must have received the replay, and long enough ago that the
	// handshake's own SyncStep2 can no longer be in flight (see compactMinAge).
	joinedAt time.Time
	synced   atomic.Bool

	done      chan struct{}
	closeOnce sync.Once
}

func newConn(ws *websocket.Conn, userID, userAPIID string) *conn {
	return &conn{
		ws:        ws,
		user:      userID,
		userAPIID: userAPIID,
		send:      make(chan []byte, sendQueue),
		ids:       map[uint32]struct{}{},
		joinedAt:  time.Now(),
		done:      make(chan struct{}),
	}
}

// snapshotReady reports whether this peer can answer a snapshot request.
func (c *conn) snapshotReady() bool {
	return c.synced.Load() && time.Since(c.joinedAt) >= compactMinAge
}

// trySend queues a frame without ever blocking the caller.
//
// Dropping the connection when the queue is full is deliberate: the alternative
// is a slow client stalling every other editor in the room. On reconnect the
// client replays from the log, so nothing is lost.
func (c *conn) trySend(msg []byte) {
	select {
	case <-c.done:
		return
	default:
	}

	select {
	case c.send <- msg:
	case <-c.done:
	default:
		c.close()
	}
}

// writeLoop is the only goroutine allowed to write to the socket.
func (c *conn) writeLoop() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-c.done:
			return
		case msg := <-c.send:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
				c.close()
				return
			}
		case <-ticker.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

func (c *conn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.ws.Close()
	})
}
