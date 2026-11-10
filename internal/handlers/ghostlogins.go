package handlers

import (
	"strings"
	"sync"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// ghostLogins pauses usernames that do not exist the way a real sign-in is
// paused (security review, 2026-11-10).
//
// A real sign-in pauses for LoginLockDuration after LoginFailuresBeforeLock
// wrong answers in a row, and says so (429 locked) — that is what tells
// its owner to stop and wait. A name nobody has used to answer 401 for
// ever, so ten tries at a name said whether it existed. Here a made-up
// name counts its wrong answers and pauses by the same rule, with the same
// reply, so the pause says nothing about who exists.
//
// In memory, bounded, and keyed by the name as the database compares it
// (trimmed, case folded). A restart forgets the ghosts and not the real
// pauses; a caller cannot cause one. When the table is full the entry
// touched longest ago makes room: filling it takes maxGhostLogins names,
// each a guess from the allowance every visitor shares (20 a minute).
type ghostLogins struct {
	mu      sync.Mutex
	entries map[string]*ghostLogin
}

type ghostLogin struct {
	failures    int
	lockedUntil int64
	touched     int64
}

const maxGhostLogins = 4096

func newGhostLogins() *ghostLogins {
	return &ghostLogins{entries: make(map[string]*ghostLogin)}
}

func ghostKey(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

// lockedUntil is when name's pause lifts, 0 when it is not paused.
func (g *ghostLogins) lockedUntil(username string, now time.Time) int64 {
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[ghostKey(username)]
	if e == nil || e.lockedUntil <= now.Unix() {
		return 0
	}
	return e.lockedUntil
}

// fail counts a wrong answer for a name nobody has, and pauses it once
// LoginFailuresBeforeLock are in a row (AccountRepository.RecordFailure's
// rule).
func (g *ghostLogins) fail(username string, now time.Time) {
	key := ghostKey(username)
	if key == "" {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil {
		if len(g.entries) >= maxGhostLogins {
			g.evictLocked()
		}
		e = &ghostLogin{}
		g.entries[key] = e
	}
	e.touched = now.UnixNano()
	e.failures++
	if e.failures >= repository.LoginFailuresBeforeLock {
		e.lockedUntil = now.Add(repository.LoginLockDuration).Unix()
		e.failures = 0
	}
}

// evictLocked drops the entry touched longest ago.
func (g *ghostLogins) evictLocked() {
	oldest, at := "", int64(0)
	for key, e := range g.entries {
		if oldest == "" || e.touched < at {
			oldest, at = key, e.touched
		}
	}
	delete(g.entries, oldest)
}
