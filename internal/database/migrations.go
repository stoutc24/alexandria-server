package database

import (
	"database/sql"
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
					session_id TEXT,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_time ON stats_events(profile_id, occurred_at);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_key ON stats_events(profile_id, content_key);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_item ON stats_events(profile_id, source, item_id);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_when ON stats_events(profile_id, occurred_at_unix);
				CREATE INDEX IF NOT EXISTS idx_stats_profile_session ON stats_events(profile_id, session_id);
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
		{
			// The admin login: one password, one TOTP secret.
			//
			// This is not the account system that was removed. That one
			// held every household member, their media-server
			// credentials, and a registry of servers. This holds exactly
			// one row -- the person with physical access to the box --
			// pinned by CHECK (id = 1) so it cannot grow into a user
			// table by accident.
			//
			// last_totp_step is what stops a code being replayed inside
			// its own 30-second window; see internal/adminauth/totp.go.
			name: "create_admin_auth_tables",
			sql: `
				CREATE TABLE IF NOT EXISTS admin_credentials (
					id INTEGER PRIMARY KEY CHECK (id = 1),
					password_hash TEXT NOT NULL,
					totp_secret TEXT NOT NULL,
					last_totp_step INTEGER DEFAULT 0,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE TABLE IF NOT EXISTS admin_recovery_codes (
					code_hash TEXT PRIMARY KEY,
					used_at DATETIME
				);
				CREATE TABLE IF NOT EXISTS admin_sessions (
					token_hash TEXT PRIMARY KEY,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					expires_at DATETIME NOT NULL
				);
				CREATE INDEX IF NOT EXISTS idx_admin_session_expiry
					ON admin_sessions(expires_at);
			`,
		},
		{
			// Admin device tokens: a phone's admin sign-in.
			//
			// A separate credential from the browser's session cookie,
			// and accepted ONLY by RequireAdmin — never by RequireProfile
			// or RequireAnyToken — so a phone signed in to manage the
			// server still cannot read anyone's stats or the party line.
			// Only the SHA-256 of each token is stored
			// (adminauth.HashToken), as with sessions.
			//
			// persistent = 1 is "stay signed in": expires_at slides to
			// 180 days after the last use and there is no absolute cap.
			// persistent = 0 is a 12-hour session that use never renews.
			// scheme_basis records how HTTPS was established when the
			// token was issued (tls, proxy, forwarded, cloudflare,
			// config or client; '' for no evidence of HTTPS — a header
			// said http, or none named a scheme and the sign-in did not
			// ask to stay). There is no revoked_at: revoking DELETES the row,
			// as with profile tokens and sessions.
			//
			// Times are INTEGER unix seconds, for the reason given for
			// occurred_at_unix below: a text date depends on the exact
			// string the driver happened to write.
			name: "create_admin_device_tokens_table",
			sql: `
				CREATE TABLE IF NOT EXISTS admin_device_tokens (
					id TEXT PRIMARY KEY,
					token_hash TEXT NOT NULL UNIQUE,
					label TEXT NOT NULL DEFAULT '',
					persistent INTEGER NOT NULL DEFAULT 0,
					created_via TEXT NOT NULL DEFAULT 'totp',
					scheme_basis TEXT NOT NULL DEFAULT '',
					created_at INTEGER NOT NULL,
					last_used_at INTEGER NOT NULL,
					expires_at INTEGER NOT NULL
				);
				CREATE INDEX IF NOT EXISTS idx_admin_device_expiry
					ON admin_device_tokens(expires_at);
			`,
		},
		{
			// Alexandria Accounts (owner, 2026-10-12): a person signs into
			// any app ONCE, and every service the admin set up for them
			// signs in with them. This revisits the August 2026 removal of
			// accounts on the owner's say-so, and stays thinner than what
			// was removed then:
			//
			//   - account_logins hangs a username and password off an
			//     existing PROFILE. The profile is still the person; a
			//     person without a login is still a person, linked by code
			//     as before.
			//   - account_invites are how a login comes to exist: the admin
			//     invites, the person sets the password. There is no open
			//     sign-up (decision 2A).
			//   - account_services are the logins the person's devices are
			//     handed, sealed with the server key (internal/vault), which
			//     never sits in this file (decision 1A).
			//   - account_device_keys is each signed-in device's public key:
			//     its services travel sealed to it.
			//
			// New tables only; nothing existing changes shape, so a server
			// that never uses accounts is untouched. Times are INTEGER unix
			// seconds, as in admin_device_tokens.
			name: "create_account_tables",
			sql: `
				CREATE TABLE IF NOT EXISTS account_logins (
					profile_id TEXT PRIMARY KEY,
					username TEXT NOT NULL UNIQUE COLLATE NOCASE,
					password_hash TEXT NOT NULL,
					totp_secret TEXT,
					totp_enabled INTEGER NOT NULL DEFAULT 0,
					last_totp_step INTEGER NOT NULL DEFAULT 0,
					failed_attempts INTEGER NOT NULL DEFAULT 0,
					locked_until INTEGER NOT NULL DEFAULT 0,
					created_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL,
					last_login_at INTEGER,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE TABLE IF NOT EXISTS account_invites (
					id TEXT PRIMARY KEY,
					profile_id TEXT NOT NULL,
					token_hash TEXT NOT NULL UNIQUE,
					code_hash TEXT NOT NULL UNIQUE,
					purpose TEXT NOT NULL DEFAULT 'invite',
					created_at INTEGER NOT NULL,
					expires_at INTEGER NOT NULL,
					used_at INTEGER,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_account_invites_profile
					ON account_invites(profile_id);
				CREATE TABLE IF NOT EXISTS account_services (
					id TEXT PRIMARY KEY,
					profile_id TEXT NOT NULL,
					service_type TEXT NOT NULL,
					name TEXT NOT NULL DEFAULT '',
					base_url TEXT NOT NULL,
					login_kind TEXT NOT NULL,
					credentials TEXT,
					priority INTEGER NOT NULL DEFAULT 0,
					created_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL,
					FOREIGN KEY (profile_id) REFERENCES profiles(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_account_services_profile
					ON account_services(profile_id, priority);
				CREATE TABLE IF NOT EXISTS account_device_keys (
					token_id TEXT PRIMARY KEY,
					public_key TEXT NOT NULL,
					created_at INTEGER NOT NULL,
					FOREIGN KEY (token_id) REFERENCES profile_tokens(id) ON DELETE CASCADE
				);
			`,
		},
		{
			// Server Admin's sign-ins to the household's servers, one per
			// service, the password sealed with the secrets key (see
			// repository.ServiceAdminRepository).
			name: "service admin logins",
			sql: `
				CREATE TABLE IF NOT EXISTS service_admin_logins (
					id TEXT PRIMARY KEY,
					service_type TEXT NOT NULL UNIQUE,
					base_url TEXT NOT NULL,
					username TEXT NOT NULL,
					secret TEXT NOT NULL,
					created_at INTEGER NOT NULL,
					updated_at INTEGER NOT NULL
				);
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
		// A client-minted id for one continuous playback session. When
		// two events carry the same one they ARE the same session, by
		// definition — no time arithmetic required. Temporal overlap
		// remains the fallback for events that carry none.
		`ALTER TABLE stats_events ADD COLUMN session_id TEXT`,
		// A Plex person the admin picked from their own Plex (owner,
		// 2026-10-13): the server's id, the person's name and Plex's
		// addresses for it, as JSON. NULL for every other service.
		`ALTER TABLE account_services ADD COLUMN plex TEXT`,
		// Admin access for a person's own account (owner, 2026-10-13: "a
		// switch in the server profile section ... full management
		// access, just server admin page, or just management access"):
		// "" none, "serveradmin", "manage" or "full". Kept on the login,
		// so removing the sign-in takes it away.
		`ALTER TABLE account_logins ADD COLUMN admin_access TEXT NOT NULL DEFAULT ''`,
		// A phone's admin sign-in made with a person's account: whose
		// account, and the access it was signed in with. Both "" for the
		// admin login's own sign-ins, which keep reaching everything.
		`ALTER TABLE admin_device_tokens ADD COLUMN profile_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE admin_device_tokens ADD COLUMN scope TEXT NOT NULL DEFAULT ''`,
		// Alexandria Link (owner, 2026-11-08): the device's public key,
		// sent WITH its code request, so the key its services are sealed
		// to is fixed before the admin approves — never by a later call a
		// copied device token could make first. NULL for a request
		// without one (an app from before Link).
		`ALTER TABLE link_requests ADD COLUMN public_key TEXT`,
		alterViaToken,
		// When a sign-in last got a wrong answer, so "ten in a row" lapses
		// (AccountRepository.RecordFailure).
		`ALTER TABLE account_logins ADD COLUMN last_failed_at INTEGER NOT NULL DEFAULT 0`,
		// Kids mode (owner, 2026-11-10): this person's devices show the
		// simple Kids screens. Switched on the admin page or in Manage
		// server, read by each device on GET /devices/me.
		`ALTER TABLE profiles ADD COLUMN kids INTEGER NOT NULL DEFAULT 0`,
		// A person's look (owner, 2026-11-11): their profile color, accent,
		// page background, Now Playing background, font and text size, as
		// a JSON object of allowlisted strings (repository.Look), and when
		// it last changed, in unix milliseconds (0: never). Set by their
		// own devices on PUT /devices/me/look, read by each of them on GET
		// /devices/me. Every existing person starts with '{}': the apps'
		// defaults, which is what they showed before.
		`ALTER TABLE profiles ADD COLUMN look TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE profiles ADD COLUMN look_updated_at INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := DB.Exec(alter); err != nil {
			if !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
				return fmt.Errorf("alter failed (%s): %w", alter, err)
			}
			continue
		}
		// The column was added just now: what must happen once with it.
		if alter == alterViaToken {
			// Manage server sign-ins made with a person's account BEFORE
			// this column name no device, so signing that device out could
			// never end them (checker, 2026-11-10). They go, once: each
			// phone signs in to Manage server again and is recorded. The
			// admin login's own sign-ins (no profile_id) are untouched.
			res, err := DB.Exec(`DELETE FROM admin_device_tokens WHERE profile_id != ''`)
			if err != nil {
				return fmt.Errorf("could not end the account sign-ins made before via_token: %w", err)
			}
			if n, _ := res.RowsAffected(); n > 0 {
				log.Printf("update: %d Manage server sign-in(s) made with a person's account were ended; "+
					"each phone signs in to Manage server once more", n)
			}
		}
	}

	log.Println("All migrations completed successfully")
	return nil
}

// alterViaToken adds which of a person's devices made a Manage server
// sign-in with their account (its profile_tokens id), so signing that ONE
// device out ends the sign-in it made (security review, 2026-11-10: the
// phone's link went and its admin sign-in stayed). "" for the admin login's
// own sign-ins.
const alterViaToken = `ALTER TABLE admin_device_tokens ADD COLUMN via_token TEXT NOT NULL DEFAULT ''`

// Meta is a tiny key/value table for facts about the database itself —
// today, just which version of the content-key algorithm the stored
// keys were computed with.
func ensureMeta() error {
	_, err := DB.Exec(`
		CREATE TABLE IF NOT EXISTS meta (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL
		)`)
	return err
}

// MetaGet returns the stored value, or "" when unset.
func MetaGet(key string) (string, error) {
	if err := ensureMeta(); err != nil {
		return "", err
	}
	var v string
	err := DB.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// MetaSet stores a value.
func MetaSet(key, value string) error {
	if err := ensureMeta(); err != nil {
		return err
	}
	_, err := DB.Exec(`
		INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}
