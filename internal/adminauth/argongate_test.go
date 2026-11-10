package adminauth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Password hashes run two at a time, however many are asked for at once
// (security review, 2026-11-10): each holds 19 MiB while it runs, and
// twenty sign-ins sent together took the server past 400 MB.
func TestPasswordHashesRunTwoAtATime(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	var running, most int32
	TestHookArgonRunning = func() {
		n := atomic.AddInt32(&running, 1)
		for {
			m := atomic.LoadInt32(&most)
			if n <= m || atomic.CompareAndSwapInt32(&most, m, n) {
				break
			}
		}
		// Long enough for every other caller to arrive and wait.
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&running, -1)
	}
	defer func() { TestHookArgonRunning = nil }()

	const callers = 12
	var wg sync.WaitGroup
	var right int32
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			password := "correct horse battery staple"
			if i%2 == 1 {
				password = "not the password"
			}
			if VerifyPassword(hash, password) {
				atomic.AddInt32(&right, 1)
			}
		}(i)
	}
	wg.Wait()
	if most > argonConcurrency {
		t.Fatalf("%d hashes ran at once, want at most %d", most, argonConcurrency)
	}
	if most < argonConcurrency {
		t.Fatalf("only %d hashes ever ran at once: the gate is tighter than %d", most, argonConcurrency)
	}
	// Waiting changes no answer.
	if right != callers/2 {
		t.Fatalf("%d of %d right passwords verified, want %d", right, callers, callers/2)
	}
}
