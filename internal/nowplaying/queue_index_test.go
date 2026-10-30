package nowplaying

import (
	"encoding/json"
	"strings"
	"testing"
)

// A leader on its FIRST song (index 0) must still publish where it is: with
// omitempty the field went missing, and an Apple TV that read a missing
// index as unknown could not steer that party (2026-10-30).
func TestQueueIndexZeroIsSent(t *testing.T) {
	b, err := json.Marshal(State{ItemID: "plex:1", QueueIndex: 0, QueueLen: 8})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"queueIndex":0`) {
		t.Fatalf("queueIndex 0 left out: %s", b)
	}
	var back State
	if err := json.Unmarshal(b, &back); err != nil || back.QueueIndex != 0 || back.QueueLen != 8 {
		t.Fatalf("round trip: %+v %v", back, err)
	}
}
