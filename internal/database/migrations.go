package database

import (
	"fmt"
	"log"
)

func RunMigrations() error {
	migrations := []struct {
		name string
		sql  string
	}{
		{
			name: "create_users_table",
			sql: `
				CREATE TABLE IF NOT EXISTS users (
					id TEXT PRIMARY KEY,
					email TEXT UNIQUE NOT NULL,
					username TEXT UNIQUE NOT NULL,
					password_hash TEXT NOT NULL,
					display_name TEXT,
					avatar_url TEXT,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
				);
				CREATE INDEX IF NOT EXISTS idx_users_email ON users(email);
				CREATE INDEX IF NOT EXISTS idx_users_username ON users(username);
			`,
		},
		{
			name: "create_families_table",
			sql: `
				CREATE TABLE IF NOT EXISTS families (
					id TEXT PRIMARY KEY,
					name TEXT NOT NULL,
					invite_code TEXT UNIQUE,
					created_by TEXT NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (created_by) REFERENCES users(id)
				);
				CREATE INDEX IF NOT EXISTS idx_families_invite_code ON families(invite_code);
			`,
		},
		{
			name: "create_family_members_table",
			sql: `
				CREATE TABLE IF NOT EXISTS family_members (
					id TEXT PRIMARY KEY,
					family_id TEXT NOT NULL,
					user_id TEXT NOT NULL,
					role TEXT NOT NULL DEFAULT 'member',
					joined_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (family_id) REFERENCES families(id) ON DELETE CASCADE,
					FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
					UNIQUE(family_id, user_id)
				);
				CREATE INDEX IF NOT EXISTS idx_family_members_family ON family_members(family_id);
				CREATE INDEX IF NOT EXISTS idx_family_members_user ON family_members(user_id);
			`,
		},
		{
			name: "create_content_requests_table",
			sql: `
				CREATE TABLE IF NOT EXISTS content_requests (
					id TEXT PRIMARY KEY,
					family_id TEXT NOT NULL,
					requested_by TEXT NOT NULL,
					title TEXT NOT NULL,
					content_type TEXT NOT NULL,
					description TEXT,
					status TEXT NOT NULL DEFAULT 'pending',
					resolved_by TEXT,
					resolved_at DATETIME,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (family_id) REFERENCES families(id) ON DELETE CASCADE,
					FOREIGN KEY (requested_by) REFERENCES users(id),
					FOREIGN KEY (resolved_by) REFERENCES users(id)
				);
				CREATE INDEX IF NOT EXISTS idx_content_requests_family ON content_requests(family_id);
				CREATE INDEX IF NOT EXISTS idx_content_requests_status ON content_requests(status);
			`,
		},
		{
			name: "create_refresh_tokens_table",
			sql: `
				CREATE TABLE IF NOT EXISTS refresh_tokens (
					id TEXT PRIMARY KEY,
					user_id TEXT NOT NULL,
					token_hash TEXT NOT NULL,
					expires_at DATETIME NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
				);
				CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user ON refresh_tokens(user_id);
				CREATE INDEX IF NOT EXISTS idx_refresh_tokens_hash ON refresh_tokens(token_hash);
			`,
		},
		{
			name: "create_media_servers_table",
			sql: `
				CREATE TABLE IF NOT EXISTS media_servers (
					id TEXT PRIMARY KEY,
					family_id TEXT NOT NULL,
					name TEXT NOT NULL,
					type TEXT NOT NULL,
					url TEXT NOT NULL,
					encrypted_credentials TEXT NOT NULL,
					created_by TEXT NOT NULL,
					created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (family_id) REFERENCES families(id) ON DELETE CASCADE,
					FOREIGN KEY (created_by) REFERENCES users(id)
				);
				CREATE INDEX IF NOT EXISTS idx_media_servers_family ON media_servers(family_id);
				CREATE INDEX IF NOT EXISTS idx_media_servers_type ON media_servers(type);
			`,
		},
		{
			name: "create_server_access_table",
			sql: `
				CREATE TABLE IF NOT EXISTS server_access (
					id TEXT PRIMARY KEY,
					server_id TEXT NOT NULL,
					user_id TEXT NOT NULL,
					granted_by TEXT NOT NULL,
					granted_at DATETIME DEFAULT CURRENT_TIMESTAMP,
					FOREIGN KEY (server_id) REFERENCES media_servers(id) ON DELETE CASCADE,
					FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
					FOREIGN KEY (granted_by) REFERENCES users(id),
					UNIQUE(server_id, user_id)
				);
				CREATE INDEX IF NOT EXISTS idx_server_access_server ON server_access(server_id);
				CREATE INDEX IF NOT EXISTS idx_server_access_user ON server_access(user_id);
			`,
		},
	}

	for _, m := range migrations {
		log.Printf("Running migration: %s", m.name)
		if _, err := DB.Exec(m.sql); err != nil {
			return fmt.Errorf("migration %s failed: %w", m.name, err)
		}
	}

	log.Println("All migrations completed successfully")
	return nil
}
