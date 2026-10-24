package live

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The vectors below were produced by the real libraries — yjs, y-protocols and
// @hocuspocus/provider — so these tests fail if the Go encoder ever drifts from
// the wire format the editor speaks. Regenerate with _scratch/gen-vectors2.cjs.
const (
	vecEmptyUpdate     = "0000"
	vecEmptyStateVec   = "00"
	vecMsgSyncStep1    = "04646f633100000701dba592a10102"
	vecMsgSyncStep2    = "04646f63310001160101dba592a1010004010764656661756c7402686900"
	vecMsgUpdate       = "04646f63310002160101dba592a1010004010764656661756c7402686900"
	vecAwarenessUpdate = "01dba592a10101247b2275736572223a7b226964223a227531222c226e616d65223a2257696c626572227d7d"
	vecMsgQueryAware   = "04646f633103"
	vecMsgAuth         = "04646f633102000b7b226964223a227531227d"
	vecVarUint300      = "ac02"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad test vector %q: %v", s, err)
	}
	return b
}

func TestVarUintRoundTrip(t *testing.T) {
	cases := []struct {
		value uint64
		hex   string
	}{
		{0, "00"},
		{1, "01"},
		{127, "7f"},
		{128, "8001"},
		{300, vecVarUint300},
		{1 << 21, "80808001"},
	}
	for _, tc := range cases {
		got := appendVarUint(nil, tc.value)
		if want := mustHex(t, tc.hex); !bytes.Equal(got, want) {
			t.Errorf("appendVarUint(%d) = %x, want %s", tc.value, got, tc.hex)
		}
		value, n, ok := readVarUint(got)
		if !ok || value != tc.value || n != len(got) {
			t.Errorf("readVarUint(%x) = (%d, %d, %v), want (%d, %d, true)", got, value, n, ok, tc.value, len(got))
		}
	}
}

func TestVarUintRejectsTruncated(t *testing.T) {
	if _, _, ok := readVarUint([]byte{0x80}); ok {
		t.Fatal("a continuation byte with no terminator must not decode")
	}
}

func TestSplitFrameParsesSyncStep1(t *testing.T) {
	doc, msgType, payload, ok := splitFrame(mustHex(t, vecMsgSyncStep1))
	if !ok {
		t.Fatal("splitFrame failed")
	}
	if doc != "doc1" {
		t.Errorf("documentName = %q, want %q", doc, "doc1")
	}
	if msgType != msgSync {
		t.Errorf("messageType = %d, want %d", msgType, msgSync)
	}
	syncType, n, ok := readVarUint(payload)
	if !ok || syncType != syncStep1 {
		t.Fatalf("syncType = %d (ok=%v), want %d", syncType, ok, syncStep1)
	}
	stateVector, _, ok := readVarBytes(payload[n:])
	if !ok {
		t.Fatal("state vector did not decode")
	}
	if !bytes.Equal(stateVector, []byte{0x01, 0xdb, 0xa5, 0x92, 0xa1, 0x01, 0x02}) {
		t.Errorf("state vector = %x", stateVector)
	}
}

func TestSplitFrameParsesQueryAwareness(t *testing.T) {
	doc, msgType, payload, ok := splitFrame(mustHex(t, vecMsgQueryAware))
	if !ok || doc != "doc1" || msgType != msgQueryAwareness {
		t.Fatalf("got doc=%q type=%d ok=%v", doc, msgType, ok)
	}
	if len(payload) != 0 {
		t.Errorf("QueryAwareness should carry no payload, got %x", payload)
	}
}

func TestBuildSyncMessageMatchesLibrary(t *testing.T) {
	// The update carried by vecMsgUpdate / vecMsgSyncStep2.
	update := mustHex(t, "0101dba592a1010004010764656661756c7402686900")

	if got := buildSyncMessage("doc1", syncUpdate, update); !bytes.Equal(got, mustHex(t, vecMsgUpdate)) {
		t.Errorf("Update frame = %x, want %s", got, vecMsgUpdate)
	}
	if got := buildSyncMessage("doc1", syncStep2, update); !bytes.Equal(got, mustHex(t, vecMsgSyncStep2)) {
		t.Errorf("SyncStep2 frame = %x, want %s", got, vecMsgSyncStep2)
	}
	if got := buildSyncMessage("doc1", syncStep1, []byte{0x01, 0xdb, 0xa5, 0x92, 0xa1, 0x01, 0x02}); !bytes.Equal(got, mustHex(t, vecMsgSyncStep1)) {
		t.Errorf("SyncStep1 frame = %x, want %s", got, vecMsgSyncStep1)
	}
}

func TestBuildAuthMessageMatchesLibrary(t *testing.T) {
	got := buildAuthMessage("doc1", authToken, `{"id":"u1"}`)
	if !bytes.Equal(got, mustHex(t, vecMsgAuth)) {
		t.Errorf("Auth frame = %x, want %s", got, vecMsgAuth)
	}
}

func TestEmptyDocumentConstants(t *testing.T) {
	if !bytes.Equal(emptyUpdate, mustHex(t, vecEmptyUpdate)) {
		t.Errorf("emptyUpdate = %x, want %s", emptyUpdate, vecEmptyUpdate)
	}
	if !bytes.Equal(emptyStateVector, mustHex(t, vecEmptyStateVec)) {
		t.Errorf("emptyStateVector = %x, want %s", emptyStateVector, vecEmptyStateVec)
	}
	if !isNoopUpdate(emptyUpdate) {
		t.Error("the empty update must be recognised as a no-op")
	}
	if isNoopUpdate([]byte{0x01, 0x02, 0x03}) {
		t.Error("a real update must not be treated as a no-op")
	}
}

func TestParseAwarenessUpdate(t *testing.T) {
	states, ok := parseAwarenessUpdate(mustHex(t, vecAwarenessUpdate))
	if !ok {
		t.Fatal("parse failed")
	}
	if len(states) != 1 {
		t.Fatalf("got %d states, want 1", len(states))
	}
	if states[0].clientID != 337941211 {
		t.Errorf("clientID = %d, want 337941211", states[0].clientID)
	}
	if states[0].clock != 1 {
		t.Errorf("clock = %d, want 1", states[0].clock)
	}
	want := `{"user":{"id":"u1","name":"Wilber"}}`
	if string(states[0].json) != want {
		t.Errorf("state = %s, want %s", states[0].json, want)
	}
}

func TestBuildAwarenessStateRoundTrip(t *testing.T) {
	payload := []byte(`{"user":{"id":"u1"}}`)
	encoded := buildAwarenessState(42, 7, payload)
	states, ok := parseAwarenessUpdate(encoded)
	if !ok || len(states) != 1 {
		t.Fatalf("round trip failed: ok=%v states=%d", ok, len(states))
	}
	if states[0].clientID != 42 || states[0].clock != 7 || !bytes.Equal(states[0].json, payload) {
		t.Errorf("round trip mismatch: %+v", states[0])
	}
}

func TestBuildAwarenessMessageFraming(t *testing.T) {
	msg := buildAwarenessMessage("doc1", mustHex(t, vecAwarenessUpdate))
	doc, msgType, payload, ok := splitFrame(msg)
	if !ok || doc != "doc1" || msgType != msgAwareness {
		t.Fatalf("frame = doc=%q type=%d ok=%v", doc, msgType, ok)
	}
	// The frame payload still carries the varuint8array length prefix, which is
	// what handleFrame reads off with readVarBytes.
	awareness, _, ok := readVarBytes(payload)
	if !ok {
		t.Fatal("awareness bytes did not decode")
	}
	if !bytes.Equal(awareness, mustHex(t, vecAwarenessUpdate)) {
		t.Errorf("awareness payload = %x", awareness)
	}
}
