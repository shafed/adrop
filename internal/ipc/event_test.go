package ipc

import (
	"bytes"
	"encoding/json"
	"testing"
)

// TestEventRoundTrip ensures a Response carrying an Event survives newline-JSON
// encode/decode unchanged, and that a subscribe-style stream message does not
// set Done.
func TestEventRoundTrip(t *testing.T) {
	want := Response{Event: &Event{
		Kind:      "recv-progress",
		Peer:      "phone",
		File:      "IMG_001.jpg",
		Index:     0,
		Count:     3,
		BytesDone: 700,
		Total:     1000,
	}}

	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(want); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got Response
	if err := json.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Event == nil {
		t.Fatal("decoded Event is nil")
	}
	if *got.Event != *want.Event {
		t.Errorf("event mismatch:\n got %+v\nwant %+v", *got.Event, *want.Event)
	}
	if got.Done {
		t.Error("stream message should not set Done")
	}
}

// TestSubscribeCommand checks the new command constant serializes as expected.
func TestSubscribeCommand(t *testing.T) {
	if CmdSubscribe != "subscribe" {
		t.Errorf("CmdSubscribe = %q, want %q", CmdSubscribe, "subscribe")
	}
	b, _ := json.Marshal(Request{Cmd: CmdSubscribe})
	if !bytes.Contains(b, []byte(`"cmd":"subscribe"`)) {
		t.Errorf("marshaled request missing subscribe cmd: %s", b)
	}
}
