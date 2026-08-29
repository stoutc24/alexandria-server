package repository

import (
	"database/sql"
	"errors"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// AdminAuthRepository stores the one admin credential and its sessions.
//
// One credential, not a user table: this server has no accounts and is
// not gaining any. There is exactly one person who runs the box, and the
// row is pinned to id = 1 by a CHECK constraint so that stays true.
type AdminAuthRepository struct{}

func NewAdminAuthRepository() *AdminAuthRepository {
	return &AdminAuthRepository{}
}

type AdminCredential struct {
	PasswordHash string
	TOTPSecret   string
	LastTOTPStep int64
}

var ErrNoCredentials = adminauth.ErrNoCredentials

// Enrolled reports whether a login has been set up.
//
// This is the switch that decides whether the environment token still
// opens /admin: before enrolment it must, or there would be no way in to
// enrol; after enrolment it must not, or the login page is decoration.
func (r *AdminAuthRepository) Enrolled() (bool, error) {
	var n int
	err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM admin_credentials`).Scan(&n)
	return n > 0, err
}

func (r *AdminAuthRepository) Get() (*AdminCredential, error) {
	var c AdminCredential
	err := database.DB.QueryRow(`
		SELECT password_hash, totp_secret, COALESCE(last_totp_step, 0)
		FROM admin_credentials WHERE id = 1`).
		Scan(&c.PasswordHash, &c.TOTPSecret, &c.LastTOTPStep)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoCredentials
	}
	return &c, err
}

// Enroll writes the credential and its recovery codes in one
// transaction, replacing anything already there.
//
// Replacing rather than refusing is deliberate: this is also the
// re-enrolment path for a lost authenticator, reached with the
// environment token, and a half-written credential would lock the owner
// out of their own server.
func (r *AdminAuthRepository) Enroll(
	passwordHash, totpSecret string,
	recoveryCodeHashes []string,
) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM admin_credentials`); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM admin_recovery_codes`); err != nil {
		return err
	}
	// Every existing session dies with the old credential. Re-enrolment
	// is what someone does after losing a device; it must not leave a
	// session open on the device they lost.
	if _, err := tx.Exec(`DELETE FROM admin_sessions`); err != nil {
		return err
	}

	if _, err := tx.Exec(`
		INSERT INTO admin_credentials (id, password_hash, totp_secret)
		VALUES (1, ?, ?)`, passwordHash, totpSecret); err != nil {
		return err
	}

	stmt, err := tx.Prepare(
		`INSERT INTO admin_recovery_codes (code_hash) VALUES (?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, h := range recoveryCodeHashes {
		if _, err := stmt.Exec(h); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// NoteTOTPStep records the time step a code was accepted at, so the same
// code cannot be replayed inside its 30-second window.
func (r *AdminAuthRepository) NoteTOTPStep(step int64) error {
	_, err := database.DB.Exec(
		`UPDATE admin_credentials SET last_totp_step = ? WHERE id = 1`,
		step)
	return err
}

// SpendRecoveryCode consumes a code, returning false if it was unknown
// or already used.
//
// The UPDATE is the check: making the write itself conditional on
// used_at IS NULL means two simultaneous attempts cannot both win.
func (r *AdminAuthRepository) SpendRecoveryCode(hash string) (bool, error) {
	res, err := database.DB.Exec(`
		UPDATE admin_recovery_codes SET used_at = CURRENT_TIMESTAMP
		WHERE code_hash = ? AND used_at IS NULL`, hash)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *AdminAuthRepository) RecoveryCodesRemaining() (int, error) {
	var n int
	err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM admin_recovery_codes WHERE used_at IS NULL`).
		Scan(&n)
	return n, err
}

// --- sessions -------------------------------------------------------

// SessionLifetime is how long a login lasts.
//
// Long enough not to nag someone managing their own server, short enough
// that a browser left open on a shared machine does not stay admin for
// a month.
const SessionLifetime = 12 * time.Hour

func (r *AdminAuthRepository) CreateSession(tokenHash string) error {
	_, err := database.DB.Exec(`
		INSERT INTO admin_sessions (token_hash, expires_at)
		VALUES (?, ?)`, tokenHash, time.Now().Add(SessionLifetime).UTC())
	return err
}

// SessionValid reports whether a session token is live, and clears it if
// it has expired.
func (r *AdminAuthRepository) SessionValid(tokenHash string) (bool, error) {
	var expires time.Time
	err := database.DB.QueryRow(
		`SELECT expires_at FROM admin_sessions WHERE token_hash = ?`,
		tokenHash).Scan(&expires)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if time.Now().After(expires) {
		_, _ = database.DB.Exec(
			`DELETE FROM admin_sessions WHERE token_hash = ?`, tokenHash)
		return false, nil
	}
	return true, nil
}

func (r *AdminAuthRepository) DeleteSession(tokenHash string) error {
	_, err := database.DB.Exec(
		`DELETE FROM admin_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// PurgeExpiredSessions is housekeeping, called at startup.
func (r *AdminAuthRepository) PurgeExpiredSessions() error {
	_, err := database.DB.Exec(
		`DELETE FROM admin_sessions WHERE expires_at < ?`, time.Now().UTC())
	return err
}
