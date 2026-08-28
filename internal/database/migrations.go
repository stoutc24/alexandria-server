package database

import (
	"fmt"
	"log"
	"strings"
)

// RunMigrations brings the schema up to date.
//
// This server used to carry users, families, media-server registries and
// per-user access grants. That was an identity system — passwords,
// registration, credential custody — and it was removed. What remains is
// deliberately thinner: a shared library index, plus PROFILES, which are
// name tags rather than accounts. Nobody logs in; a profile token simply
// says which bucket the stats belong in.
//
// Existing databases keep their old tables; nothing here drops them.
// Dropping data is the owner's call, not a migration's.
func RunMigrations() error {
	migrations := []struct {
		name string
		sql  string
	}{
		{
			// The household's merged library index.
			//
			// This mirrors what the media servers already hold; it is a
			// CACHE, never the system of record. Dropping a row here
			// removes an index entry, never media on anybody's server.
			//
			// content_key is computed on write (catalog.ContentKey) and
			// is NULL when the item cannot be grouped safely — see
			// internal/catalog/dedupe.go for why that asymmetry matters.
			name: "create_catalog_items_table",
			sql: `
				CREATE TABLE IF NOT EXISTS catalog_items (
					id TEXT PRIMARY KEY,
					source TEXT NOT NULL,
					item_id TEXT NOT NULL,
					content_key TEXT,
					media_type TEXT NOT NULL,
					title TEXT NOT NULL,
					author TEXT,
					series TEXT,
					genre TEXT,
					year INTEGER,
					duration_sec INTEGER,
					synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					UNIQUE(source, item_id)
				);
				CREATE INDEX IF NOT EXISTS idx_catalog_key ON catalog_items(content_key);
				CREATE INDEX IF NOT EXISTS idx_catalog_source ON catalog_items(source);
				CREATE INDEX IF NOT EXISTS idx_catalog_type ON catalog_items(media_type);
			`,
		},
		{
			// A profile is a name. That is the whole model.
			//
			// No password, no email, no registration: it exists only to
			// keep one person's stats out of another person's totals.
			name: "create_profiles_table",
			sql: `
				CREATE TABLE IF NOT EXISTS profiles (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL UNIQUE,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
			`,
		},
		{
			// One row per DEVICE, not per person.
			//
			// Sarah's phone, watch and TV each hold their own token, so
			// linking a new device never logs the others out, and a lost
			// phone is revoked on its own rather than by resetting
			// everything she owns.
			//
			// Only the SHA-256 of each token is stored, so the database
			// holds nothing replayable. Plain SHA-256 is right here:
			// tokens are 32 bytes from crypto/rand, and the slow hashes
			// exist for low-entropy human passwords.
			name: "create_profile_tokens_table",
			sql: `
				CREATE TABLE IF NOT EXISTS profile_tokens (
					id TEXT PRIMARY KEY,
					profile_id TEXT NOT NULL,
					token_hash TEXT NOT NULL UNIQUE,
					label TEXT,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					last_seen_at DATETIME,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_profile_tokens_hash ON profile_tokens(token_hash);
				CREATE INDEX IF NOT EXISTS idx_profile_tokens_profile ON profile_tokens(profile_id);
			`,
		},
		{
			// One row per listening/reading event, owned by a profile.
			//
			// title and author are denormalised on purpose: a catalog
			// resync REPLACES catalog_items wholesale, and someone's
			// history must not turn into a list of blanks because an
			// album was reorganised on the server.
			name: "create_stats_events_table",
			sql: `
				CREATE TABLE IF NOT EXISTS stats_events (
					id TEXT PRIMARY KEY,
					profile_id TEXT NOT NULL,
					client_event_id TEXT,
					source TEXT,
					item_id TEXT,
					content_key TEXT,
					media_type TEXT,
					title TEXT,
					author TEXT,
					event_type TEXT NOT NULL,
					seconds INTEGER NOT NULL DEFAULT 0,
					occurred_at DATETIME NOT NULL,
					occurred_at_unix INTEGER,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_time ON stats_events(profile_id, occurred_at);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_key ON stats_events(profile_id, content_key);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_item ON stats_events(profile_id, source, item_id);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_when ON stats_events(profile_id, occurred_at_unix);
			`,
		},
		{
			// Idempotent ingest.
			//
			// A person's phone and watch both report, syncs retry after a
			// dropped connection, and an offline queue replays whatever
			// it buffered. None of that may inflate someone's hours. The
			// client stamps each event with an id that is stable for that
			// event forever, and this index makes a repeat a no-op
			// instead of a second row.
			//
			// Partial (WHERE NOT NULL) so events sent by older clients,
			// which carry no id, are still accepted — they simply do not
			// get deduplicated.
			name: "add_stats_client_event_id",
			sql: `
				CREATE UNIQUE INDEX IF NOT EXISTS idx_stats_client_event
					ON stats_events(profile_id, client_event_id)
					WHERE client_event_id IS NOT NULL;
			`,
		},
		{
			// Device linking, in the Plex style.
			//
			// The app asks for a code and shows it to the person. They
			// read it to the admin, who approves it. The direction
			// matters: the short code travels TOWARD the admin and is
			// worthless on its own — approving it requires the admin
			// token, and collecting the result requires claim_hash,
			// which only the requesting app ever had.
			//
			// So the thing spoken out loud is not a credential. Nothing
			// sensitive is transmitted between people at all.
			name: "create_link_requests_table",
			sql: `
				CREATE TABLE IF NOT EXISTS link_requests (
					id TEXT PRIMARY KEY,
					code TEXT NOT NULL UNIQUE,
					claim_hash TEXT NOT NULL UNIQUE,
					device_label TEXT,
					profile_id TEXT,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					expires_at DATETIME NOT NULL,
					approved_at DATETIME,
					claimed_at DATETIME,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_link_code ON link_requests(code);
				CREATE INDEX IF NOT EXISTS idx_link_claim ON link_requests(claim_hash);
			`,
		},
	}

	for _, m := range migrations {
		log.Printf("Running migration: %s", m.name)
		if _, err := DB.Exec(m.sql); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.name, err)
		}
	}

	// Columns added to a table that already exists elsewhere. CREATE
	// TABLE IF NOT EXISTS will not alter it, so these run separately and
	// tolerate "duplicate column" — the only way to be idempotent
	// against both a fresh database and one already in service.
	for _, alter := range []string{
		`ALTER TABLE stats_events ADD COLUMN client_event_id TEXT`,
		// Epoch seconds alongside the DATETIME. Overlap detection needs
		// arithmetic on instants, and SQLite's text dates make that
		// depend on the exact string the driver happened to write —
		// which fails silently rather than loudly. An integer cannot be
		// ambiguous.
		`ALTER TABLE stats_events ADD COLUMN occurred_at_unix INTEGER`,
	} {
		if _, err := DB.Exec(alter); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				return fmt.Errorf("alter failed (%s): %w", alter, err)
			}
		}
	}

	log.Println("All migrations completed successfully")
	return nil
}
