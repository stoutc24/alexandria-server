package repository

import (
	"testing"
	"time"
)

// LoginKey is the database's own comparison: trimmed, ASCII letters
// lowered, and nothing a username cannot be.
func TestLoginKeyFoldsAsTheDatabaseCompares(t *testing.T) {
	for typed, want := range map[string]string{
		"sam": "sam", "  Sam ": "sam", "KATE.R": "kate.r", "a-b_c.9": "a-b_c.9",
	} {
		if key, ok := LoginKey(typed); !ok || key != want {
			t.Errorf("LoginKey(%q) = %q, %v; want %q, true", typed, key, ok, want)
		}
	}
	for _, typed := range []string{
		"", "ab", "Kate", "İan", "sam smith", ".sam", "a" + string(make([]byte, 40)),
		"abcdefghijklmnopqrstuvwxyz0123456",
	} {
		if key, ok := LoginKey(typed); ok {
			t.Errorf("LoginKey(%q) = %q, true; want no username", typed, key)
		}
	}
}

// "Ten in a row" lapses: a wrong answer more than LoginFailureWindow after
// the one before starts the count again.
func TestWrongAnswersInARowLapse(t *testing.T) {
	accounts, profiles := newAccountsDB(t, false)
	p, err := profiles.Create("Sam")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	if _, _, inv, err := accounts.CreateInvite(p.ID, "invite", time.Hour, now); err != nil {
		t.Fatal(err)
	} else if err := accounts.AcceptInvite(inv, "sam", "hash", now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < LoginFailuresBeforeLock-1; i++ {
		if until, err := accounts.RecordFailure(p.ID, now); err != nil || until != 0 {
			t.Fatalf("wrong answer %d: until %d, %v", i+1, until, err)
		}
	}
	// The tenth, long after: the first of a new run, not a pause.
	later := now.Add(LoginFailureWindow + time.Minute)
	if until, err := accounts.RecordFailure(p.ID, later); err != nil || until != 0 {
		t.Fatalf("a wrong answer after the window: until %d, %v; want no pause", until, err)
	}
	// Nine more inside the window do pause.
	var until int64
	for i := 0; i < LoginFailuresBeforeLock-1; i++ {
		if until, err = accounts.RecordFailure(p.ID, later.Add(time.Duration(i+1)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if until == 0 {
		t.Fatal("ten wrong answers inside the window did not pause the sign-in")
	}
	if got, err := accounts.LockedUntil(p.ID, later.Add(time.Minute)); err != nil || got != until {
		t.Fatalf("LockedUntil = %d, %v; want %d", got, err, until)
	}
	if got, err := accounts.LockedUntil(p.ID, time.Unix(until+1, 0)); err != nil || got != 0 {
		t.Fatalf("LockedUntil after it lifts = %d, %v; want 0", got, err)
	}
}
