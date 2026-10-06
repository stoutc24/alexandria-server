package adminauth

import (
	"sync"
	"time"
)

// GuessLimit is an allowance of wrong guesses at the admin login's
// password and codes, one per key: Burst wrong guesses, then one more each
// Refill. The guess is taken before the check, so nothing slow runs once
// an allowance is spent, and a right answer gives it back.
//
// The server keeps two (see handlers.AdminAuthHandler):
//
//   - Sign-ins share one allowance, every visitor together. The rate
//     limiters in front of the sign-in routes count each visitor on its
//     own, which is no use against a guesser with many addresses (an IPv6
//     block, a botnet, a forged X-Forwarded-For through a proxy that
//     passes it on). This caps them all at once. It refills faster than
//     any one visitor may ask, so no single visitor can ever use it up
//     and lock the owner out; only several guessing at once can, and then
//     the owner's sign-in waits its turn.
//   - A signed-in phone's step-up codes have an allowance per phone: a
//     stolen phone token cannot grind codes, and nobody else can use up
//     the owner's.
//
// Both live in memory; a restart clears them.
type GuessLimit struct {
	mu      sync.Mutex
	burst   float64
	refill  time.Duration
	now     func() time.Time
	buckets map[string]*guessBucket
}

// guessBucket is one key's allowance. A key with no bucket has a full
// one.
type guessBucket struct {
	tokens float64
	at     time.Time
	// noted is when a refusal was last announced, so a caller logs an
	// attack about once a minute rather than on every refused request.
	noted time.Time
}

// The two allowances: sign-ins, every visitor together, and each signed-in
// phone's step-ups.
const (
	SignInGuessBurst  = 60
	SignInGuessRefill = 3 * time.Second // 20 a minute: twice one visitor's limit
	StepUpGuessBurst  = 10
	StepUpGuessRefill = time.Minute
)

// maxGuessBuckets is how many keys are tracked before full buckets (which
// are the same as none) are swept out.
const maxGuessBuckets = 256

// NewGuessLimit allows burst wrong guesses per key, then one more every
// refill.
func NewGuessLimit(burst int, refill time.Duration) *GuessLimit {
	return newGuessLimit(burst, refill, time.Now)
}

func newGuessLimit(burst int, refill time.Duration, now func() time.Time) *GuessLimit {
	return &GuessLimit{
		burst:   float64(burst),
		refill:  refill,
		now:     now,
		buckets: make(map[string]*guessBucket),
	}
}

// Take spends one of key's guesses, before a password or code is checked.
// When none is left it refuses, with how long until the next one; announce
// is true at most once a minute while it refuses, for the caller's log.
func (g *GuessLimit) Take(key string) (ok bool, wait time.Duration, announce bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	b := g.bucketLocked(key, now)
	if b.tokens >= 1 {
		b.tokens--
		return true, 0, false
	}
	if b.noted.IsZero() || now.Sub(b.noted) >= time.Minute || now.Before(b.noted) {
		b.noted = now
		announce = true
	}
	return false, time.Duration((1 - b.tokens) * float64(g.refill)), announce
}

// Refund gives back the guess a right answer took.
func (g *GuessLimit) Refund(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.bucketLocked(key, g.now())
	b.tokens = min(g.burst, b.tokens+1)
}

// bucketLocked returns key's bucket, refilled to now: what time has paid
// for since it was last touched, never past the burst. A clock that ran
// backwards earns nothing and restarts the count from now.
func (g *GuessLimit) bucketLocked(key string, now time.Time) *guessBucket {
	b, ok := g.buckets[key]
	if !ok {
		if len(g.buckets) >= maxGuessBuckets {
			g.sweepLocked(now)
		}
		b = &guessBucket{tokens: g.burst, at: now}
		g.buckets[key] = b
		return b
	}
	if elapsed := now.Sub(b.at); elapsed > 0 {
		b.tokens = min(g.burst, b.tokens+float64(elapsed)/float64(g.refill))
	}
	b.at = now
	return b
}

// sweepLocked drops every bucket that time has refilled: a full bucket is
// the same as none.
func (g *GuessLimit) sweepLocked(now time.Time) {
	for key, b := range g.buckets {
		if b.tokens+float64(now.Sub(b.at))/float64(g.refill) >= g.burst {
			delete(g.buckets, key)
		}
	}
}
