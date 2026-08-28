package repository

import (
	"testing"
	"time"
)

func finish(clientID, key, title, author string, at time.Time) Event {
	return Event{
		ClientEventID: clientID,
		Source:        "abs",
		ItemID:        key,
		ContentKey:    key,
		MediaType:     "audiobook",
		Title:         title,
		Author:        author,
		EventType:     "finished",
		Seconds:       0,
		OccurredAt:    at,
	}
}

// The stat that should see the person: reading one book five times in a
// year is ONE book read and FOUR rereads — not five books.
func TestFiveReadsOfOneBookIsOneBookAndFourRereads(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	base := time.Now().UTC().Add(-300 * 24 * time.Hour)
	events := make([]Event, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, finish(
			"read:"+string(rune('a'+i)), "unsouled|will wight",
			"Unsouled", "Will Wight",
			base.Add(time.Duration(i)*45*24*time.Hour), // ~6 weeks apart
		))
	}

	r, err := stats.AppendEvents(p.ID, events)
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Accepted != 5 {
		t.Fatalf("expected all 5 finishes stored, got %+v", r)
	}

	s, err := stats.Summarize(p.ID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.Finished != 5 {
		t.Errorf("Finished = %d, want 5 (every completion counts)", s.Finished)
	}
	if s.DistinctWorksFinished != 1 {
		t.Errorf("DistinctWorksFinished = %d, want 1 — five reads of one "+
			"book is one book", s.DistinctWorksFinished)
	}
	if s.Rereads != 4 {
		t.Errorf("Rereads = %d, want 4", s.Rereads)
	}
	if len(s.MostReread) != 1 || s.MostReread[0].Times != 5 {
		t.Errorf("MostReread = %+v, want Unsouled x5", s.MostReread)
	}
	if s.MostReread[0].Title != "Unsouled" {
		t.Errorf("MostReread title = %q", s.MostReread[0].Title)
	}
}

// Five different books is five books and no rereads — the counterpart
// that proves the statistic is not just subtracting.
func TestFiveDifferentBooksIsFiveBooksAndNoRereads(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	base := time.Now().UTC().Add(-300 * 24 * time.Hour)
	events := make([]Event, 0, 5)
	for i := 0; i < 5; i++ {
		title := "Book " + string(rune('A'+i))
		events = append(events, finish(
			"read:"+string(rune('a'+i)),
			"book-"+string(rune('a'+i))+"|author",
			title, "Author",
			base.Add(time.Duration(i)*30*24*time.Hour),
		))
	}
	if _, err := stats.AppendEvents(p.ID, events); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, _ := stats.Summarize(p.ID)
	if s.DistinctWorksFinished != 5 {
		t.Errorf("DistinctWorksFinished = %d, want 5", s.DistinctWorksFinished)
	}
	if s.Rereads != 0 {
		t.Errorf("Rereads = %d, want 0", s.Rereads)
	}
	if len(s.MostReread) != 0 {
		t.Errorf("MostReread should be empty, got %+v", s.MostReread)
	}
}

// Finishing the same work on two different services is a REREAD of one
// work, not two books — the cross-service content key earning its keep.
func TestFinishingTheSameWorkOnTwoServicesIsOneWork(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	base := time.Now().UTC().Add(-100 * 24 * time.Hour)

	plex := finish("p:1", "discovery|daft punk", "Discovery", "Daft Punk", base)
	plex.Source = "plex"
	navi := finish("n:1", "discovery|daft punk", "discovery", "daft punk",
		base.Add(40*24*time.Hour))
	navi.Source = "navidrome"

	if _, err := stats.AppendEvents(p.ID, []Event{plex, navi}); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, _ := stats.Summarize(p.ID)
	if s.DistinctWorksFinished != 1 {
		t.Errorf("DistinctWorksFinished = %d, want 1 — same work, two "+
			"servers", s.DistinctWorksFinished)
	}
	if s.Rereads != 1 {
		t.Errorf("Rereads = %d, want 1", s.Rereads)
	}
}

// Two devices both noticing one finish must not become a reread. This is
// the failure that would quietly fabricate rereads for everybody.
func TestTwoDevicesNoticingOneFinishIsNotAReread(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	at := time.Now().UTC().Add(-2 * time.Hour)

	phone := finish("phone:1", "unsouled|will wight", "Unsouled", "Will Wight", at)
	// The watch crossed the threshold a few minutes later, with its own
	// local row id — so id-dedupe cannot catch it.
	watch := finish("watch:1", "unsouled|will wight", "Unsouled", "Will Wight",
		at.Add(7*time.Minute))

	r, err := stats.AppendEvents(p.ID, []Event{phone, watch})
	if err != nil {
		t.Fatalf("append: %v", err)
	}
	if r.Accepted != 1 {
		t.Errorf("both finishes were stored: %+v", r)
	}

	s, _ := stats.Summarize(p.ID)
	if s.Finished != 1 {
		t.Errorf("Finished = %d, want 1", s.Finished)
	}
	if s.Rereads != 0 {
		t.Errorf("Rereads = %d, want 0 — a fabricated reread", s.Rereads)
	}
}

// ...but a genuine reread weeks later must still register.
func TestAGenuineRereadWeeksLaterStillCounts(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	first := time.Now().UTC().Add(-90 * 24 * time.Hour)
	again := time.Now().UTC().Add(-10 * 24 * time.Hour)

	if _, err := stats.AppendEvents(p.ID, []Event{
		finish("a:1", "unsouled|will wight", "Unsouled", "Will Wight", first),
		finish("a:2", "unsouled|will wight", "Unsouled", "Will Wight", again),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, _ := stats.Summarize(p.ID)
	if s.Finished != 2 {
		t.Errorf("Finished = %d, want 2", s.Finished)
	}
	if s.Rereads != 1 {
		t.Errorf("Rereads = %d, want 1 — a real reread was swallowed",
			s.Rereads)
	}
}
