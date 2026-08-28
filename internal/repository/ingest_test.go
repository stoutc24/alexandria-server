package repository

import (
	"testing"
	"time"
)

func session(clientID, item string, start time.Time, secs int) Event {
	return Event{
		ClientEventID: clientID,
		Source:        "abs",
		ItemID:        item,
		ContentKey:    "unsouled|will wight",
		MediaType:     "audiobook",
		Title:         "Unsouled",
		Author:        "Will Wight",
		EventType:     "progress",
		Seconds:       secs,
		OccurredAt:    start,
	}
}

// THE CASE A STABLE CLIENT ID DOES NOT COVER.
//
// The phone records a session as its row 42; the watch independently
// records the same twenty minutes as its row 17. Different ids, so
// dedupe-by-id lets both through and the person's hours double.
func TestIndependentReportsOfOneSessionAreNotAddedTwice(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	r1, err := stats.AppendEvents(p.ID, []Event{
		session("phone:42", "book-1", start, 1200), // 10:00–10:20
	})
	if err != nil || r1.Accepted != 1 {
		t.Fatalf("phone: %+v err=%v", r1, err)
	}

	// The watch saw the same listening, from its own local row.
	r2, err := stats.AppendEvents(p.ID, []Event{
		session("watch:17", "book-1", start, 1200),
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if r2.Accepted != 0 {
		t.Errorf("the watch's copy was added as new activity: %+v", r2)
	}
	if r2.Overlapped != 1 {
		t.Errorf("expected 1 overlap, got %+v", r2)
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalSeconds != 1200 {
		t.Errorf("TotalSeconds = %d, want 1200 — the session was counted "+
			"twice", s.TotalSeconds)
	}
	if s.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1", s.TotalEvents)
	}
}

// Partial overlap is still the same session: the watch caught the first
// fifteen minutes, the phone the whole twenty.
func TestAPartialOverlapKeepsTheFullerAccount(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	if _, err := stats.AppendEvents(p.ID, []Event{
		session("watch:1", "book-1", start, 900), // 15 min
	}); err != nil {
		t.Fatalf("watch: %v", err)
	}

	// The phone reports the same listening, but saw more of it.
	r, err := stats.AppendEvents(p.ID, []Event{
		session("phone:1", "book-1", start.Add(2*time.Minute), 1200),
	})
	if err != nil {
		t.Fatalf("phone: %v", err)
	}
	if r.Extended != 1 {
		t.Errorf("expected the longer report to extend the session: %+v", r)
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalSeconds != 1200 {
		t.Errorf("TotalSeconds = %d, want 1200 (the fuller account, not "+
			"900+1200)", s.TotalSeconds)
	}
	if s.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1", s.TotalEvents)
	}
}

// The rule must not swallow genuine re-listening. Same book, different
// day, is real activity.
func TestListeningToTheSameBookLaterStillCounts(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	monday := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	tuesday := monday.Add(24 * time.Hour)

	if _, err := stats.AppendEvents(p.ID, []Event{
		session("phone:1", "book-1", monday, 1200),
	}); err != nil {
		t.Fatalf("monday: %v", err)
	}
	r, err := stats.AppendEvents(p.ID, []Event{
		session("phone:2", "book-1", tuesday, 1800),
	})
	if err != nil {
		t.Fatalf("tuesday: %v", err)
	}
	if r.Accepted != 1 {
		t.Fatalf("a later session was swallowed as an overlap: %+v", r)
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalSeconds != 3000 {
		t.Errorf("TotalSeconds = %d, want 3000", s.TotalSeconds)
	}
}

// Two different books at the same time IS possible — an audiobook while
// an ebook sits open. Overlap only means "the same thing twice".
func TestDifferentItemsAtTheSameTimeBothCount(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	start := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)

	a := session("phone:1", "book-1", start, 600)
	b := session("phone:2", "book-2", start, 600)
	b.ContentKey = "another book|someone else"

	r, err := stats.AppendEvents(p.ID, []Event{a, b})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Accepted != 2 {
		t.Errorf("two different books were collapsed: %+v", r)
	}
}

// A device with a broken clock or a runaway timer must not be able to
// poison someone's totals.
func TestImplausibleEventsAreRejectedNotStored(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	now := time.Now().UTC()
	cases := []struct {
		name  string
		event Event
	}{
		{"a session longer than a day",
			session("bad:1", "b1", now.Add(-2*time.Hour), 200000)},
		{"reported from next week",
			session("bad:2", "b2", now.Add(7*24*time.Hour), 600)},
		{"a clock reset to the epoch",
			session("bad:3", "b3", time.Unix(0, 0).UTC(), 600)},
	}

	for _, c := range cases {
		r, err := stats.AppendEvents(p.ID, []Event{c.event})
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if r.Rejected != 1 || r.Accepted != 0 {
			t.Errorf("%s was not rejected: %+v", c.name, r)
		}
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalEvents != 0 {
		t.Errorf("implausible events reached storage: %d", s.TotalEvents)
	}
}

// A plausible session at the edge of tolerance must still be accepted —
// the guard must not eat real data from a slightly-off clock.
func TestMildClockSkewIsToleratedNotRejected(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	// An hour ahead: a timezone mistake, not a broken device.
	r, err := stats.AppendEvents(p.ID, []Event{
		session("skew:1", "b1", time.Now().UTC().Add(time.Hour), 600),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Accepted != 1 {
		t.Errorf("a mildly skewed clock had its data thrown away: %+v", r)
	}
}

// Old data is counted, but flagged, so the app can tell a month-old
// import from something that happened this morning.
func TestBackfillIsCountedButReported(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	old := time.Now().UTC().Add(-30 * 24 * time.Hour)
	r, err := stats.AppendEvents(p.ID, []Event{
		session("old:1", "b1", old, 1200),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Accepted != 1 {
		t.Fatalf("backfill was dropped: %+v", r)
	}
	if r.Backfilled != 1 {
		t.Errorf("backfill was not flagged: %+v", r)
	}

	recent, err := stats.AppendEvents(p.ID, []Event{
		session("new:1", "b2", time.Now().UTC().Add(-time.Hour), 600),
	})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if recent.Backfilled != 0 {
		t.Errorf("today's listening was called backfill: %+v", recent)
	}
}

// A month-offline device dumping its backlog: overlapping rows collapse,
// genuinely new ones land, and nothing is double counted.
func TestAnOfflineDeviceDumpingItsBacklogConverges(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	base := time.Now().UTC().Add(-20 * 24 * time.Hour)

	// The phone already reported ten sessions over ten days.
	phone := make([]Event, 0, 10)
	for i := 0; i < 10; i++ {
		phone = append(phone, session(
			"phone:"+string(rune('a'+i)), "book-1",
			base.Add(time.Duration(i)*24*time.Hour), 1800,
		))
	}
	r, err := stats.AppendEvents(p.ID, phone)
	if err != nil || r.Accepted != 10 {
		t.Fatalf("phone backlog: %+v err=%v", r, err)
	}

	// The watch comes back online with its own record of the same ten
	// days, plus two the phone never saw.
	watch := make([]Event, 0, 12)
	for i := 0; i < 12; i++ {
		watch = append(watch, session(
			"watch:"+string(rune('a'+i)), "book-1",
			base.Add(time.Duration(i)*24*time.Hour), 1800,
		))
	}
	r2, err := stats.AppendEvents(p.ID, watch)
	if err != nil {
		t.Fatalf("watch backlog: %v", err)
	}
	if r2.Overlapped != 10 {
		t.Errorf("expected 10 overlaps, got %+v", r2)
	}
	if r2.Accepted != 2 {
		t.Errorf("expected the 2 genuinely new sessions, got %+v", r2)
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalEvents != 12 {
		t.Errorf("TotalEvents = %d, want 12", s.TotalEvents)
	}
	if s.TotalSeconds != 12*1800 {
		t.Errorf("TotalSeconds = %d, want %d", s.TotalSeconds, 12*1800)
	}
}
