package handlers

import (
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
// What makes the two alike (the checker's findings, same day):
//
//   - The key is repository.LoginKey: the very comparison the database
//     makes to find a sign-in (trimmed, ASCII letters lowered). A string no
//     username can be finds nobody and is never tracked — it answers 401 for
//     ever, which is also all a real sign-in can say about it.
//   - "In a row" lapses after LoginFailureWindow for both
//     (AccountRepository.RecordFailure), so an entry here need not last.
//   - A restart forgets these, and AccountRepository.ForgetGuessState
//     forgets the real ones with them.
//
// In memory and bounded: an entry is at most a 32-character name, lapsed
// entries are dropped as new ones arrive, and each new name costs a guess
// from the allowance every visitor shares (20 a minute), so the entries
// alive at once are a few hundred.
type ghostLogins struct {
	mu      sync.Mutex
	entries map[string]*ghostLogin
}

type ghostLogin struct {
	failures    int
	lockedUntil int64
	lastFailed  int64
}

// maxGhostLogins is the most names tracked. Never reached by guessing
// alone (see above); a backstop.
const maxGhostLogins = 4096

func newGhostLogins() *ghostLogins {
	return &ghostLogins{entries: make(map[string]*ghostLogin)}
}

// lockedUntil is when name's pause lifts, 0 when it is not paused.
func (g *ghostLogins) lockedUntil(username string, now time.Time) int64 {
	key, ok := repository.LoginKey(username)
	if !ok {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil || e.lockedUntil <= now.Unix() {
		return 0
	}
	return e.lockedUntil
}

// fail counts a wrong answer for a name nobody has, and pauses it once
// LoginFailuresBeforeLock are in a row (AccountRepository.RecordFailure's
// rule, lapse included).
func (g *ghostLogins) fail(username string, now time.Time) {
	key, ok := repository.LoginKey(username)
	if !ok {
		return
	}
	window := int64(repository.LoginFailureWindow / time.Second)
	g.mu.Lock()
	defer g.mu.Unlock()
	e := g.entries[key]
	if e == nil {
		g.sweepLocked(now, window)
		if len(g.entries) >= maxGhostLogins {
			g.evictLocked()
		}
		e = &ghostLogin{}
		g.entries[key] = e
	}
	if now.Unix()-e.lastFailed > window {
		e.failures = 0
	}
	e.lastFailed = now.Unix()
	e.failures++
	if e.failures >= repository.LoginFailuresBeforeLock {
		e.lockedUntil = now.Add(repository.LoginLockDuration).Unix()
		e.failures = 0
	}
}

// sweepLocked drops every entry that says nothing any more: not paused,
// and its last wrong answer lapsed. A dropped entry and a kept one answer
// the same from here on.
func (g *ghostLogins) sweepLocked(now time.Time, window int64) {
	for key, e := range g.entries {
		if e.lockedUntil <= now.Unix() && now.Unix()-e.lastFailed > window {
			delete(g.entries, key)
		}
	}
}

// evictLocked drops the entry whose last wrong answer is oldest.
func (g *ghostLogins) evictLocked() {
	oldest, at := "", int64(0)
	for key, e := range g.entries {
		if oldest == "" || e.lastFailed < at {
			oldest, at = key, e.lastFailed
		}
	}
	delete(g.entries, oldest)
}
