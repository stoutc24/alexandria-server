package database

import (
	"context"
	"path/filepath"
	"testing"
)

// The DSN is the one place where a wrong string fails SILENTLY: SQLite
// drivers ignore parameters they do not recognise. This project already
// lost months of cascade enforcement to exactly that, with
// "?_foreign_keys=on" — mattn's spelling, which modernc.org/sqlite
// accepts and discards.
//
// So assert the settings on the server's OWN connections. Reading them
// from another process proves nothing: cache_size, temp_store and
// foreign_keys are per-connection, and only journal_mode is persisted
// in the file.
func TestPragmasAreActuallyApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pragma.db")
	if err := Connect(path); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer Close()

	checks := []struct {
		pragma string
		want   int64
		why    string
	}{
		{"foreign_keys", 1, "cascades are inert without it"},
		{"synchronous", 1, "NORMAL: safe under WAL, no fsync per commit"},
		{"cache_size", -8000, "8 MB of page cache, negative meaning KB"},
		{"temp_store", 2, "MEMORY: sorting scratch stays off disk"},
	}

	for _, c := range checks {
		var got int64
		if err := DB.QueryRow("PRAGMA " + c.pragma).Scan(&got); err != nil {
			t.Errorf("%s: %v", c.pragma, err)
			continue
		}
		if got != c.want {
			t.Errorf("PRAGMA %s = %d, want %d — %s",
				c.pragma, got, c.want, c.why)
		}
	}

	var journal string
	if err := DB.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal — readers block on the "+
			"writer without it", journal)
	}

	// The pool must stay small: SQLite has one writer, and an unbounded
	// pool trades memory for lock contention.
	if got := DB.Stats().MaxOpenConnections; got != 4 {
		t.Errorf("MaxOpenConnections = %d, want 4", got)
	}
}

// Every pooled connection must carry the settings, not just the first.
// A pragma applied once at open would leave later connections running
// with defaults — foreign keys off, cache tiny — under exactly the
// concurrency this tuning is for.
func TestPragmasHoldOnEveryPooledConnection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pragma_pool.db")
	if err := Connect(path); err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer Close()

	// Hold several connections open at once, forcing the pool to make
	// new ones rather than reuse a single tuned connection.
	ctx := context.Background()
	const n = 4
	for i := 0; i < n; i++ {
		conn, err := DB.Conn(ctx)
		if err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
		defer conn.Close()

		var fk, temp int64
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").
			Scan(&fk); err != nil {
			t.Fatalf("conn %d foreign_keys: %v", i, err)
		}
		if err := conn.QueryRowContext(ctx, "PRAGMA temp_store").
			Scan(&temp); err != nil {
			t.Fatalf("conn %d temp_store: %v", i, err)
		}
		if fk != 1 {
			t.Errorf("connection %d has foreign_keys = %d", i, fk)
		}
		if temp != 2 {
			t.Errorf("connection %d has temp_store = %d", i, temp)
		}
	}
}
