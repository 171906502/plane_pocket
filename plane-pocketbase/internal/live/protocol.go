// Package live implements the collaboration endpoint Plane's page editor talks
// to, so a single Go binary can replace the Node `apps/live` service (and the
// Redis it insists on) for a small single-instance deployment.
//
// # What this is, and what it deliberately is not
//
// The editor speaks Hocuspocus over a WebSocket: every frame is
//
//	[varstring documentName][varuint messageType][payload]
//
// and the payload of a Sync message is a y-protocols sync message
// (`[varuint syncType][varuint8array data]`). The real service decodes those
// updates with Yjs, keeps a CRDT document in memory, and answers peers with a
// *diff computed against their state vector*.
//
// This package does not implement a CRDT. It cannot: merging Yjs updates is the
// whole point of Yjs. What it does instead is relay:
//
//   - every update it receives is appended to a per-document log on disk and
//     forwarded verbatim to the other connections;
//   - a connecting client is given the whole log, one Update message per record;
//   - to a client's SyncStep1 it answers with an *empty* SyncStep2 (which is what
//     marks the client as synced, see `MessageReceiver.applySyncMessage`) and
//     then pushes the log. An empty diff is not a lie that matters: the log is
//     the truth, and it follows immediately.
//
// That is sufficient because Yjs updates are commutative and idempotent — a
// client that receives "the whole history, in order" ends up exactly where the
// real server would have put it. The price is that the log only grows (no
// compaction) and that the server cannot derive HTML from the document, so the
// web app PATCHes `description_html` / `description_json` itself (see
// `apps/web/hooks/use-collab-description-sync.ts`).
//
// # Authentication
//
// The upgrade request carries the same `session-id` cookie as the REST API, so
// the user is resolved before the socket is accepted. The Hocuspocus `Auth`
// frame (which carries a JSON blob the editor builds from the current user, not
// a secret) is then checked against that user id, mirroring
// `apps/live/src/lib/auth.ts`.
package live

// Message types. Values come from `@hocuspocus/provider`'s MessageType enum
// (provider/src/types.ts); they are on the wire, so they must never be renumbered.
const (
	msgSync           = 0
	msgAwareness      = 1
	msgAuth           = 2
	msgQueryAwareness = 3
	msgStateless      = 5
	msgClose          = 7
	msgSyncStatus     = 8
)

// y-protocols/sync message kinds, the first varuint inside a Sync payload.
const (
	syncStep1  = 0 // "here is my state vector, send me what I am missing"
	syncStep2  = 1 // "here is the diff you asked for" (also used to mark synced)
	syncUpdate = 2 // "here is an update, apply it"
)

// Auth sub-message kinds from `@hocuspocus/common`'s auth.ts.
const (
	authToken  = 0
	authDenied = 1
	authOK     = 2
)

const scopeReadWrite = "read-write"

// emptyUpdate is `Y.encodeStateAsUpdate(new Y.Doc())` — verified against the
// real library (see protocol_test.go). It decodes to a no-op update.
var emptyUpdate = []byte{0x00, 0x00}

// emptyStateVector is `Y.encodeStateVector(new Y.Doc())`: zero known clients.
// Sending it is how we ask a peer for its complete state.
var emptyStateVector = []byte{0x00}

// appendVarUint writes an unsigned LEB128 integer, the encoding lib0 uses for
// every length and enum on the wire.
func appendVarUint(dst []byte, v uint64) []byte {
	for v > 0x7f {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// readVarUint returns the value and how many bytes it consumed.
func readVarUint(b []byte) (value uint64, n int, ok bool) {
	var shift uint
	for i := 0; i < len(b) && i < 10; i++ {
		c := b[i]
		value |= uint64(c&0x7f) << shift
		if c < 0x80 {
			return value, i + 1, true
		}
		shift += 7
	}
	return 0, 0, false
}

// appendVarString writes a length-prefixed UTF-8 string.
func appendVarString(dst []byte, s string) []byte {
	dst = appendVarUint(dst, uint64(len(s)))
	return append(dst, s...)
}

func readVarString(b []byte) (value string, n int, ok bool) {
	size, k, ok := readVarUint(b)
	if !ok || uint64(len(b)-k) < size {
		return "", 0, false
	}
	end := k + int(size)
	return string(b[k:end]), end, true
}

// appendVarBytes writes a length-prefixed byte slice.
func appendVarBytes(dst, data []byte) []byte {
	dst = appendVarUint(dst, uint64(len(data)))
	return append(dst, data...)
}

func readVarBytes(b []byte) (value []byte, n int, ok bool) {
	size, k, ok := readVarUint(b)
	if !ok || uint64(len(b)-k) < size {
		return nil, 0, false
	}
	end := k + int(size)
	return b[k:end], end, true
}

// buildSyncMessage frames a y-protocols sync message.
func buildSyncMessage(documentName string, syncType uint64, payload []byte) []byte {
	out := make([]byte, 0, len(documentName)+len(payload)+8)
	out = appendVarString(out, documentName)
	out = appendVarUint(out, msgSync)
	out = appendVarUint(out, syncType)
	return appendVarBytes(out, payload)
}

// buildAwarenessMessage frames an awareness update (cursor / presence).
func buildAwarenessMessage(documentName string, payload []byte) []byte {
	out := make([]byte, 0, len(documentName)+len(payload)+8)
	out = appendVarString(out, documentName)
	out = appendVarUint(out, msgAwareness)
	return appendVarBytes(out, payload)
}

// buildAuthMessage frames the server's auth verdict. The editor only reacts to
// it by marking itself authenticated; the connection is already authorized by
// the cookie, so this is a formality the client expects.
func buildAuthMessage(documentName string, authType uint64, scope string) []byte {
	out := make([]byte, 0, len(documentName)+len(scope)+8)
	out = appendVarString(out, documentName)
	out = appendVarUint(out, msgAuth)
	out = appendVarUint(out, authType)
	return appendVarString(out, scope)
}

// awarenessState is one presence record extracted from an awareness update.
// The wire format (y-protocols/awareness) is:
//
//	varuint numStates, then per state:
//	  varuint clientID, varuint clock, varstring json
type awarenessState struct {
	clientID uint32
	clock    uint64
	json     []byte
}

// parseAwarenessUpdate splits an awareness update into its states. It is only
// used for bookkeeping (which connection owns which clientID, and how to write
// the removal record when that connection drops), so a malformed update is
// simply not tracked — the bytes are still relayed untouched.
func parseAwarenessUpdate(b []byte) ([]awarenessState, bool) {
	count, n, ok := readVarUint(b)
	if !ok {
		return nil, false
	}
	rest := b[n:]
	states := make([]awarenessState, 0, count)
	for i := uint64(0); i < count; i++ {
		clientID, k, ok := readVarUint(rest)
		if !ok {
			return nil, false
		}
		rest = rest[k:]
		clock, k, ok := readVarUint(rest)
		if !ok {
			return nil, false
		}
		rest = rest[k:]
		payload, k, ok := readVarString(rest)
		if !ok {
			return nil, false
		}
		rest = rest[k:]
		states = append(states, awarenessState{clientID: uint32(clientID), clock: clock, json: []byte(payload)})
	}
	return states, true
}

// buildAwarenessState encodes a single-state awareness update. `json` is the
// state payload; passing nil writes `null`, which is how y-protocols removes a
// client from the awareness map.
func buildAwarenessState(clientID uint32, clock uint64, payload []byte) []byte {
	out := appendVarUint(nil, 1)
	out = appendVarUint(out, uint64(clientID))
	out = appendVarUint(out, clock)
	return appendVarString(out, string(payload))
}
