package handlers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// Only a string a username can be is ever kept: anything else finds nobody
// in the database either, and keeping it let 4,096 sixteen-kilobyte "names"
// hold 64 MiB until the next restart (checker, 2026-11-10).
func TestGhostsKeepOnlyNamesAUsernameCanBe(t *testing.T) {
	g := newGhostLogins()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < 50; i++ {
		g.fail(strings.Repeat("a", 16_000)+fmt.Sprint(i), now)
		g.fail("Kate", now)
		g.fail("two words", now)
		g.fail("", now)
	}
	if n := len(g.entries); n != 0 {
		t.Fatalf("%d entries kept for strings no username can be, want none", n)
	}
	g.fail("  Kate ", now)
	g.fail("kate", now)
	if n := len(g.entries); n != 1 {
		t.Fatalf("%d entries for one name in two spellings, want 1", n)
	}
}

// A made-up name pauses by the real sign-in's rule: ten wrong answers in a
// row, in a row lapsing after the window, for the lock's length.
func TestGhostsPauseLapseAndAreSwept(t *testing.T) {
	g := newGhostLogins()
	now := time.Unix(1_800_000_000, 0)
	for i := 0; i < repository.LoginFailuresBeforeLock-1; i++ {
		g.fail("nobody", now)
	}
	if g.lockedUntil("nobody", now) != 0 {
		t.Fatal("paused after nine wrong answers")
	}
	// The tenth, after the window: the first of a new run.
	later := now.Add(repository.LoginFailureWindow + time.Minute)
	g.fail("nobody", later)
	if g.lockedUntil("nobody", later) != 0 {
		t.Fatal("a wrong answer after the window counted with the nine before it")
	}
	for i := 0; i < repository.LoginFailuresBeforeLock-1; i++ {
		g.fail("NOBODY", later)
	}
	until := g.lockedUntil("nobody", later)
	if want := later.Add(repository.LoginLockDuration).Unix(); until != want {
		t.Fatalf("paused until %d, want %d", until, want)
	}
	if g.lockedUntil("nobody", time.Unix(until+1, 0)) != 0 {
		t.Fatal("still paused after the pause lifted")
	}

	// Entries that say nothing any more make room as new names arrive.
	for i := 0; i < 300; i++ {
		g.fail(fmt.Sprintf("name%03d", i), later)
	}
	muchLater := later.Add(repository.LoginLockDuration + repository.LoginFailureWindow + time.Minute)
	g.fail("someone.new", muchLater)
	if n := len(g.entries); n != 1 {
		t.Fatalf("%d entries after everything lapsed and one new name, want 1", n)
	}
}
