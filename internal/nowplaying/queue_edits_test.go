package nowplaying

import "testing"

// Queue edits (owner, 2026-10-28: "Remote and Party Play devices need the
// ability to add songs to party play queue, and remove songs"). The leader
// owns its queue; the server checks each ask, wakes the leader, and keeps
// the edit until the leader acknowledges it.

func editingLeader(s *Store, queue []QueueItem) (State, ReportOpts) {
	s.SetQueue("p1", queue)
	phone := ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), EditsQueue: true}
	st, _ := s.Report("p1", State{Source: "plex", ItemID: "plex:a", QueueIndex: 0, IsPlaying: true}, phone)
	return st, phone
}

func song(id string) QueueItem { return QueueItem{Source: "plex", ItemID: id, Title: id} }

// A leader that predates edits never says it carries them out: refused,
// not left waiting forever.
func TestQueueEditRefusedWhileTheLeaderCannotEdit(t *testing.T) {
	s := NewStore()
	s.SetQueue("p1", []QueueItem{song("plex:a")})
	s.Report("p1", State{Source: "plex", ItemID: "plex:a", IsPlaying: true},
		ReportOpts{DeviceID: "tok-old", BasedOnRevision: rev(0)})
	_, out := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}})
	if out != OutcomeLeaderCannotEdit {
		t.Fatalf("edit for a leader that cannot edit: %v", out)
	}
	if cur, _ := s.Get("p1"); len(cur.QueueEdits) != 0 {
		t.Fatalf("refused edit is waiting: %+v", cur.QueueEdits)
	}
}

// An add waits in the state for the leader, wakes it (the revision moves),
// and goes once the leader acknowledges its seq.
func TestAddWaitsForTheLeaderUntilAcknowledged(t *testing.T) {
	s := NewStore()
	before, phone := editingLeader(s, []QueueItem{song("plex:a"), song("plex:b")})

	st, out := s.EditQueue("p1", "Living Room TV", QueueEditRequest{
		Type: IntentAddToQueue, Next: true, Items: []QueueItem{song("plex:x"), song("plex:y")}})
	if out != OutcomeOK {
		t.Fatalf("add refused: %v", out)
	}
	if st.Revision <= before.Revision {
		t.Fatalf("revision did not move: the leader's long-poll would sleep through it")
	}
	if len(st.QueueEdits) != 1 {
		t.Fatalf("edits = %+v", st.QueueEdits)
	}
	e := st.QueueEdits[0]
	if e.Type != IntentAddToQueue || !e.Next || len(e.Items) != 2 || e.Items[0].ItemID != "plex:x" ||
		e.ByLabel != "Living Room TV" || e.Seq == 0 {
		t.Fatalf("edit = %+v", e)
	}

	// The leader carried it out: its report acks the seq.
	ack := e.Seq
	phone.BasedOnRevision = rev(st.Revision)
	phone.QueueEditAck = &ack
	after, out := s.Report("p1", State{Source: "plex", ItemID: "plex:a", QueueIndex: 0, IsPlaying: true}, phone)
	if out != OutcomeOK || len(after.QueueEdits) != 0 {
		t.Fatalf("acked edit still waiting: %v %+v", out, after.QueueEdits)
	}
}

// An ack covers only what the leader has seen: a later edit stays.
func TestAckDropsOnlyUpToItsSeq(t *testing.T) {
	s := NewStore()
	_, phone := editingLeader(s, []QueueItem{song("plex:a")})
	first, _ := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}})
	second, _ := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:y")}})
	if len(second.QueueEdits) != 2 || second.QueueEdits[1].Seq <= second.QueueEdits[0].Seq {
		t.Fatalf("seqs not increasing: %+v", second.QueueEdits)
	}
	ack := first.QueueEdits[0].Seq
	phone.BasedOnRevision = rev(second.Revision)
	phone.QueueEditAck = &ack
	st, _ := s.Report("p1", State{Source: "plex", ItemID: "plex:a", IsPlaying: true}, phone)
	if len(st.QueueEdits) != 1 || st.QueueEdits[0].Items[0].ItemID != "plex:y" {
		t.Fatalf("after a partial ack: %+v", st.QueueEdits)
	}
}

// A removal names the entry where the device saw it; anything that moved
// since is stale, the playing song is refused, and two rooms removing the
// same song make one removal.
func TestRemoveIsGuardedByWhatTheDeviceSaw(t *testing.T) {
	s := NewStore()
	st, _ := editingLeader(s, []QueueItem{song("plex:a"), song("plex:b"), song("plex:c")})
	qrev := st.QueueRevision

	cases := []struct {
		name string
		in   QueueEditRequest
		want Outcome
	}{
		{"old queue revision", QueueEditRequest{Type: IntentRemoveFromQueue, Index: 1, ItemID: "plex:b", IfQueueRevision: qrev - 1}, OutcomeStale},
		{"another song in that slot", QueueEditRequest{Type: IntentRemoveFromQueue, Index: 1, ItemID: "plex:c", IfQueueRevision: qrev}, OutcomeStale},
		{"past the end", QueueEditRequest{Type: IntentRemoveFromQueue, Index: 3, ItemID: "plex:c", IfQueueRevision: qrev}, OutcomeStale},
		{"the playing song", QueueEditRequest{Type: IntentRemoveFromQueue, Index: 0, ItemID: "plex:a", IfQueueRevision: qrev}, OutcomeIsCurrent},
	}
	for _, c := range cases {
		if _, out := s.EditQueue("p1", "TV", c.in); out != c.want {
			t.Errorf("%s: %v, want %v", c.name, out, c.want)
		}
	}
	if cur, _ := s.Get("p1"); len(cur.QueueEdits) != 0 {
		t.Fatalf("a refused removal is waiting: %+v", cur.QueueEdits)
	}

	ok := QueueEditRequest{Type: IntentRemoveFromQueue, Index: 2, ItemID: "plex:c", IfQueueRevision: qrev}
	if _, out := s.EditQueue("p1", "TV", ok); out != OutcomeOK {
		t.Fatalf("removal refused: %v", out)
	}
	twice, out := s.EditQueue("p1", "Phone", ok)
	if out != OutcomeOK || len(twice.QueueEdits) != 1 {
		t.Fatalf("second room's removal: %v %+v", out, twice.QueueEdits)
	}
	if e := twice.QueueEdits[0]; e.Index != 2 || e.ItemID != "plex:c" {
		t.Fatalf("removal = %+v", e)
	}
}

// What waits on the leader is bounded, by edits and by songs.
func TestPendingEditsAreBounded(t *testing.T) {
	s := NewStore()
	editingLeader(s, []QueueItem{song("plex:a")})
	for i := 0; i < maxPendingEdits; i++ {
		if _, out := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeOK {
			t.Fatalf("add %d refused: %v", i, out)
		}
	}
	if _, out := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeBusy {
		t.Fatalf("edit past the cap: %v", out)
	}

	s2 := NewStore()
	editingLeader(s2, []QueueItem{song("plex:a")})
	big := make([]QueueItem, MaxEditItems)
	for i := range big {
		big[i] = song("plex:x")
	}
	s2.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: big})
	s2.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: big})
	if _, out := s2.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeBusy {
		t.Fatalf("songs past the cap: %v", out)
	}
	if _, out := s2.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: append(big, song("plex:x"))}); out == OutcomeOK {
		t.Fatalf("an add over %d songs was taken", MaxEditItems)
	}
}

// An edit the leader never carried out stops being offered after editTTL.
func TestUnacknowledgedEditsExpire(t *testing.T) {
	s := NewStore()
	editingLeader(s, []QueueItem{song("plex:a")})
	s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}})
	s.mu.Lock()
	s.edits["p1"][0].AtMs -= editTTL.Milliseconds() + 1
	s.mu.Unlock()
	if cur, _ := s.Get("p1"); len(cur.QueueEdits) != 0 {
		t.Fatalf("expired edit still offered: %+v", cur.QueueEdits)
	}
}

// Nothing in a report's body can plant edits or claim the capability.
func TestReportBodyCannotPlantEdits(t *testing.T) {
	s := NewStore()
	s.SetQueue("p1", []QueueItem{song("plex:a")})
	planted := State{Source: "plex", ItemID: "plex:a", IsPlaying: true, LeaderEditsQueue: true,
		QueueEdits: []QueueEdit{{Seq: 1, Type: IntentAddToQueue, Items: []QueueItem{song("plex:evil")}}}}
	st, _ := s.Report("p1", planted, ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0)})
	if st.LeaderEditsQueue || len(st.QueueEdits) != 0 {
		t.Fatalf("body planted edit state: %+v", st)
	}
}

// A book has no queue; a party with nobody leading has no one to carry an
// edit out; an ended party takes none.
func TestEditsNeedAMusicPartyWithALeader(t *testing.T) {
	s := NewStore()
	s.Report("p1", State{Source: "abs", ItemID: "book-1", IsPlaying: true},
		ReportOpts{DeviceID: "tok-phone", BasedOnRevision: rev(0), EditsQueue: true})
	if _, out := s.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeNoQueue {
		t.Fatalf("edit to a book party: %v", out)
	}

	s2 := NewStore()
	editingLeader(s2, []QueueItem{song("plex:a")})
	s2.mu.Lock()
	st := s2.m["p1"]
	st.LeaderReportedAtMs -= leaderLostAfter.Milliseconds() + 1
	s2.m["p1"] = st
	s2.mu.Unlock()
	if _, out := s2.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeLeaderAsleep {
		t.Fatalf("edit with the leader asleep: %v", out)
	}

	s3 := NewStore()
	editingLeader(s3, []QueueItem{song("plex:a")})
	s3.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}})
	s3.EndBy("p1", "tok-phone", "Phone", "stop-all")
	if _, out := s3.EditQueue("p1", "TV", QueueEditRequest{Type: IntentAddToQueue, Items: []QueueItem{song("plex:x")}}); out != OutcomeNoSession {
		t.Fatalf("edit to an ended party: %v", out)
	}
	if cur, _ := s3.Get("p1"); len(cur.QueueEdits) != 0 {
		t.Fatalf("an ended party still offers edits: %+v", cur.QueueEdits)
	}
}
