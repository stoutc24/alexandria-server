package database

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func Connect(dbPath string) error {
	var err error
	// modernc.org/sqlite wants _pragma=..., NOT the mattn/go-sqlite3
	// spelling "_foreign_keys=on" — which it ignores silently, leaving
	// every foreign key unenforced. Verified: the old form yielded
	// PRAGMA foreign_keys = 0.
	DB, err = sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err = DB.Ping(); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	return nil
}

func Close() error {
	if DB != nil {
		return DB.Close()
	}
	return nil
}
