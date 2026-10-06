package adminauth

import (
	"fmt"
	"testing"
	"time"
)

func frozenAt(now *time.Time) func() time.Time {
	return func() time.Time { return *now }
}

func TestGuessLimitAllowsABurstThenOneEachRefill(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(10, time.Minute, frozenAt(&now))
	for i := 0; i < 10; i++ {
		if ok, _, _ := g.Take("k"); !ok {
			t.Fatalf("guess %d of the burst refused", i+1)
		}
	}
	// Refused before anything is checked, whatever the answer would have
	// been: the trade-off that makes a spent allowance hold.
	ok, wait, announce := g.Take("k")
	if ok || !announce || wait != time.Minute {
		t.Fatalf("11th: ok=%v wait=%v announce=%v, want refused, a minute, announced", ok, wait, announce)
	}
	if ok, _, announce := g.Take("k"); ok || announce {
		t.Fatalf("12th: ok=%v announce=%v, want refused and not announced again", ok, announce)
	}

	now = now.Add(30 * time.Second)
	if ok, wait, _ := g.Take("k"); ok || wait != 30*time.Second {
		t.Fatalf("half a refill later: ok=%v wait=%v, want refused, 30s", ok, wait)
	}
	now = now.Add(30 * time.Second)
	if ok, _, _ := g.Take("k"); !ok {
		t.Fatal("a whole refill later the next guess was refused")
	}
	if ok, _, announce := g.Take("k"); ok || !announce {
		t.Fatalf("a minute after the last announcement: ok=%v announce=%v, want refused, announced", ok, announce)
	}

	// Quiet time refills up to the burst, never past it.
	now = now.Add(time.Hour)
	for i := 0; i < 10; i++ {
		if ok, _, _ := g.Take("k"); !ok {
			t.Fatalf("an hour later, guess %d refused", i+1)
		}
	}
	if ok, _, _ := g.Take("k"); ok {
		t.Fatal("an hour of quiet bought more than the burst")
	}
}

func TestGuessLimitRefundsARightAnswer(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(2, time.Minute, frozenAt(&now))
	for i := 0; i < 5; i++ {
		if ok, _, _ := g.Take("k"); !ok {
			t.Fatalf("right answer %d was refused", i+1)
		}
		g.Refund("k")
	}
	g.Take("k")
	g.Take("k")
	if ok, _, _ := g.Take("k"); ok {
		t.Fatal("refunds lifted the allowance past its burst")
	}
}

// One key's guesses never touch another's: a stranger cannot spend a
// phone's step-ups.
func TestGuessLimitKeysAreSeparate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(3, time.Minute, frozenAt(&now))
	for i := 0; i < 3; i++ {
		g.Take("device:a")
	}
	if ok, _, _ := g.Take("device:a"); ok {
		t.Fatal("device a's allowance is not spent")
	}
	if ok, _, _ := g.Take("device:b"); !ok {
		t.Fatal("device a's guesses spent device b's allowance")
	}
}

// The sign-ins' allowance refills faster than one visitor may ask (the
// per-visitor limit is 10 a minute), so one visitor at full speed never
// spends it.
func TestOneVisitorAtFullSpeedNeverSpendsTheSignInAllowance(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(SignInGuessBurst, SignInGuessRefill, frozenAt(&now))
	for i := 0; i < 24*60*10; i++ { // a day of 10 a minute
		if ok, _, _ := g.Take(""); !ok {
			t.Fatalf("refused one visitor's wrong guess %d at 10 a minute", i+1)
		}
		now = now.Add(6 * time.Second)
	}
	// Three at full speed together do.
	spent := false
	for i := 0; i < 60*30 && !spent; i++ { // up to an hour of 30 a minute
		ok, _, _ := g.Take("")
		spent = !ok
		now = now.Add(2 * time.Second)
	}
	if !spent {
		t.Fatal("three visitors at full speed never spent the allowance")
	}
}

func TestGuessLimitSweepsFullBucketsOnly(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(2, time.Minute, frozenAt(&now))
	g.Take("drained")
	g.Take("drained")
	// Touched, still full; at the cap, each new key sweeps the full ones.
	for i := 0; i < maxGuessBuckets; i++ {
		g.Refund(fmt.Sprintf("full-%d", i))
	}
	g.Take("one-more")
	if n := len(g.buckets); n > 3 {
		t.Fatalf("%d buckets after the sweep, want the drained one and a new one", n)
	}
	if ok, _, _ := g.Take("drained"); ok {
		t.Fatal("the sweep forgot a drained allowance")
	}
}

func TestGuessLimitSurvivesAClockGoingBackwards(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newGuessLimit(1, time.Minute, frozenAt(&now))
	g.Take("k")
	now = now.Add(-time.Hour)
	if ok, wait, _ := g.Take("k"); ok || wait != time.Minute {
		t.Fatalf("after the clock went back: ok=%v wait=%v, want refused, a minute", ok, wait)
	}
	now = now.Add(time.Minute)
	if ok, _, _ := g.Take("k"); !ok {
		t.Fatal("a minute after the clock went back, still refused")
	}
}
