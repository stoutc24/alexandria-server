package database

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

// Connect opens the database and tunes it for a small always-on box.
//
// The defaults are wrong for this workload in ways that cost real
// resources: rollback journaling serialises readers behind every write,
// synchronous=FULL fsyncs on every commit, and database/sql will happily
// open an unbounded number of connections to a file that only has one
// writer.
func Connect(dbPath string) error {
	// modernc.org/sqlite wants _pragma=..., NOT the mattn/go-sqlite3
	// spelling "_foreign_keys=on" — which it ignores silently, leaving
	// every foreign key unenforced. Verified: the old form yielded
	// PRAGMA foreign_keys = 0.
	//
	// journal_mode(WAL):   readers no longer block on the writer, which
	//                      is the whole shape of this workload — several
	//                      devices reading a catalogue while one syncs.
	// synchronous(NORMAL): safe under WAL; avoids an fsync per commit.
	//                      A power cut can lose the last transaction,
	//                      which for an index that rebuilds itself is an
	//                      acceptable trade for the write cost.
	// busy_timeout(5000):  wait for a lock rather than failing instantly.
	// cache_size(-8000):   8 MB of page cache, in KB, negative meaning
	//                      KB rather than pages. Enough for the working
	//                      set of a large catalogue; small enough to sit
	//                      on a NAS.
	// temp_store(MEMORY):  GROUP BY and ORDER BY scratch space stays off
	//                      the disk.
	dsn := dbPath + "?" + strings.Join([]string{
		"_pragma=foreign_keys(1)",
		"_pragma=journal_mode(WAL)",
		"_pragma=synchronous(NORMAL)",
		"_pragma=busy_timeout(5000)",
		"_pragma=cache_size(-8000)",
		"_pragma=temp_store(MEMORY)",
	}, "&")

	var err error
	DB, err = sql.Open("sqlite", dsn)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite has exactly one writer. An unbounded pool does not make it
	// faster — it makes lock contention and memory worse. A handful of
	// connections covers a household's devices with room to spare.
	DB.SetMaxOpenConns(4)
	DB.SetMaxIdleConns(2)
	DB.SetConnMaxIdleTime(5 * time.Minute)

	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

// Vacuum reclaims space left by deleted rows. Cheap on a database this
// size, and worth doing when a lot has been removed at once.
func Vacuum() error {
	_, err := DB.Exec("PRAGMA incremental_vacuum")
	return err
}

func Close() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}
