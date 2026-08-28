package repository

import (
	"testing"
	"time"
)

// The core isolation property: one person's numbers never include
// another's, no matter what order things were written in.
func TestStatsAreScopedToOneProfile(t *testing.T) {
	profiles, stats := newProfileDB(t)

	sarah, _ := mkProfile(t, profiles, "Sarah")
	chris, _ := mkProfile(t, profiles, "Chris")

	now := time.Now().UTC()
	if _, err := stats.AppendEvents(sarah.ID, []Event{
		{Title: "Unsouled", Author: "Will Wight", MediaType: "book",
			EventType: "progress", Seconds: 1200, OccurredAt: now},
		{Title: "Unsouled", Author: "Will Wight", MediaType: "book",
			EventType: "finished", Seconds: 600, OccurredAt: now},
	}); err != nil {
		t.Fatalf("sarah: %v", err)
	}
	if _, err := stats.AppendEvents(chris.ID, []Event{
		{Title: "Discovery", Author: "Daft Punk", MediaType: "music",
			EventType: "progress", Seconds: 300, OccurredAt: now},
	}); err != nil {
		t.Fatalf("chris: %v", err)
	}

	s, err := stats.Summarize(sarah.ID)
	if err != nil {
		t.Fatalf("summarize sarah: %v", err)
	}
	if s.TotalEvents != 2 {
		t.Errorf("Sarah TotalEvents = %d, want 2", s.TotalEvents)
	}
	if s.TotalSeconds != 1800 {
		t.Errorf("Sarah TotalSeconds = %d, want 1800", s.TotalSeconds)
	}
	if s.Finished != 1 {
		t.Errorf("Sarah Finished = %d, want 1", s.Finished)
	}
	if s.SecondsByType["music"] != 0 {
		t.Errorf("Chris's music bled into Sarah's totals: %v", s.SecondsByType)
	}
	if len(s.TopAuthors) != 1 || s.TopAuthors[0].Author != "Will Wight" {
		t.Errorf("Sarah TopAuthors = %+v", s.TopAuthors)
	}

	c, err := stats.Summarize(chris.ID)
	if err != nil {
		t.Fatalf("summarize chris: %v", err)
	}
	if c.TotalSeconds != 300 {
		t.Errorf("Chris TotalSeconds = %d, want 300", c.TotalSeconds)
	}
	if c.Finished != 0 {
		t.Errorf("Chris Finished = %d, want 0", c.Finished)
	}
}

func TestSummaryOfAnEmptyProfileIsZeroNotAnError(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "New Person")

	s, err := stats.Summarize(p.ID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.TotalEvents != 0 || s.TotalSeconds != 0 {
		t.Errorf("expected zeros, got %+v", s)
	}
	if s.FirstEventAt != nil || s.LastEventAt != nil {
		t.Errorf("expected nil timestamps, got %v / %v",
			s.FirstEventAt, s.LastEventAt)
	}
	if s.SecondsByType == nil || s.TopAuthors == nil {
		t.Error("maps/slices must be non-nil so JSON renders {} and [], not null")
	}
}

// Timestamps come back through SQLite's loose typing; if the parse
// regresses the app silently shows "never".
func TestFirstAndLastTimestampsSurviveTheRoundTrip(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	early := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	late := time.Date(2026, 8, 27, 10, 30, 0, 0, time.UTC)

	if _, err := stats.AppendEvents(p.ID, []Event{
		{Title: "B", EventType: "progress", Seconds: 60, OccurredAt: late},
		{Title: "A", EventType: "progress", Seconds: 60, OccurredAt: early},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	s, err := stats.Summarize(p.ID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.FirstEventAt == nil || s.LastEventAt == nil {
		t.Fatal("timestamps came back nil; the SQLite time parse regressed")
	}
	if !s.FirstEventAt.Equal(early) {
		t.Errorf("FirstEventAt = %v, want %v", s.FirstEventAt, early)
	}
	if !s.LastEventAt.Equal(late) {
		t.Errorf("LastEventAt = %v, want %v", s.LastEventAt, late)
	}
}

// Erasing yourself must not require the admin, and must not touch
// anybody else.
func TestDeleteOwnStatsIsScoped(t *testing.T) {
	profiles, stats := newProfileDB(t)

	sarah, _ := mkProfile(t, profiles, "Sarah")
	chris, _ := mkProfile(t, profiles, "Chris")
	mustAppend(t, stats, sarah.ID, 4)
	mustAppend(t, stats, chris.ID, 3)

	deleted, err := stats.DeleteForProfile(sarah.ID)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if deleted != 4 {
		t.Errorf("deleted = %d, want 4", deleted)
	}

	s, _ := stats.Summarize(sarah.ID)
	if s.TotalEvents != 0 {
		t.Errorf("Sarah still has %d events", s.TotalEvents)
	}
	c, _ := stats.Summarize(chris.ID)
	if c.TotalEvents != 3 {
		t.Errorf("Chris lost events: %d, want 3", c.TotalEvents)
	}

	// The profile itself survives erasing its history.
	if _, err := profiles.List(); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestOversizedBatchIsRejectedWholesale(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	events := make([]Event, maxEventsPerBatch+1)
	for i := range events {
		events[i] = Event{EventType: "progress", Seconds: 1,
			OccurredAt: time.Now().UTC()}
	}
	if _, err := stats.AppendEvents(p.ID, events); err == nil {
		t.Fatal("an oversized batch was accepted")
	}

	s, _ := stats.Summarize(p.ID)
	if s.TotalEvents != 0 {
		t.Errorf("a rejected batch still wrote %d events", s.TotalEvents)
	}
}

// The double-counting guard. A phone and a watch reporting the same
// session, or any retry, must not inflate someone's hours.
func TestRepeatedEventsAreIgnoredNotDuplicated(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	event := Event{
		ClientEventID: "device-a:42",
		Title:         "Unsouled",
		Author:        "Will Wight",
		MediaType:     "book",
		EventType:     "progress",
		Seconds:       600,
		OccurredAt:    time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC),
	}

	res, err := stats.AppendEvents(p.ID, []Event{event})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if res.Accepted != 1 {
		t.Fatalf("first append accepted %d, want 1", res.Accepted)
	}

	// The same event again — a retry, or the watch reporting what the
	// phone already sent.
	res, err = stats.AppendEvents(p.ID, []Event{event, event})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if res.Accepted != 0 {
		t.Errorf("replay accepted %d new rows, want 0", res.Accepted)
	}
	if res.Duplicates != 2 {
		t.Errorf("replay reported %d duplicates, want 2", res.Duplicates)
	}

	s, err := stats.Summarize(p.ID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.TotalEvents != 1 {
		t.Errorf("TotalEvents = %d, want 1 — the event was counted twice",
			s.TotalEvents)
	}
	if s.TotalSeconds != 600 {
		t.Errorf("TotalSeconds = %d, want 600 — hours were inflated",
			s.TotalSeconds)
	}
}

// Two people may legitimately use the same local row id on their own
// devices; dedupe is per profile, not global.
func TestTheSameClientIDForDifferentPeopleIsNotADuplicate(t *testing.T) {
	profiles, stats := newProfileDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")
	chris, _ := mkProfile(t, profiles, "Chris")

	e := Event{ClientEventID: "row-1", Title: "T", EventType: "progress",
		Seconds: 60, OccurredAt: time.Now().UTC()}

	if r, err := stats.AppendEvents(sarah.ID, []Event{e}); err != nil || r.Accepted != 1 {
		t.Fatalf("sarah: %+v err=%v", r, err)
	}
	if r, err := stats.AppendEvents(chris.ID, []Event{e}); err != nil || r.Accepted != 1 {
		t.Fatalf("chris's identical id was swallowed: %+v err=%v", r, err)
	}
}

// Older clients send no id. They must still be accepted, and must not
// collide with each other via a shared NULL.
func TestEventsWithoutAClientIDAreStillAccepted(t *testing.T) {
	profiles, stats := newProfileDB(t)
	p, _ := mkProfile(t, profiles, "Sarah")

	e := Event{Title: "T", EventType: "progress", Seconds: 60,
		OccurredAt: time.Now().UTC()}

	if r, err := stats.AppendEvents(p.ID, []Event{e, e, e}); err != nil || r.Accepted != 3 {
		t.Fatalf("unidentified events: %+v err=%v, want 3 accepted", r, err)
	}
}
