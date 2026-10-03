package repository

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
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
	// …and every phone's admin sign-in, for the same reason: the lost
	// device may be a phone.
	if _, err := tx.Exec(`DELETE FROM admin_device_tokens`); err != nil {
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

// ClaimTOTPStep spends a TOTP time step, so the same code cannot be
// replayed inside its 30-second window. It reports false when the step
// is already spent.
//
// The UPDATE is the check, as in SpendRecoveryCode. Recording the step
// with a plain write after checking it against a value read earlier let
// every request carrying one code win, as long as they all arrived
// before the first finished hashing the password — 8 of 8 concurrent
// logins in the security review. Here two requests carrying one code
// cannot both change the row.
//
// ceiling is adminauth.StepCeiling(now): a stored step beyond it is
// corrupt rather than spent (see adminauth.Verify), so a genuine claim
// may still overwrite it instead of locking the owner out for good.
func (r *AdminAuthRepository) ClaimTOTPStep(step, ceiling int64) (bool, error) {
	res, err := database.DB.Exec(`
		UPDATE admin_credentials SET last_totp_step = ?
		WHERE id = 1
		  AND (COALESCE(last_totp_step, 0) < ? OR COALESCE(last_totp_step, 0) > ?)`,
		step, step, ceiling)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
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

// --- admin device tokens ---------------------------------------------
//
// A phone's admin sign-in: issued for a fresh password plus second
// factor (handlers.AdminAuthHandler.IssueDeviceToken), presented as
// "Authorization: Bearer alxa_…", and accepted only by
// middleware.RequireAdmin. Every method takes now, so tests own the
// clock.

// AdminDeviceIdleLimit ends a "stay signed in" phone that has gone
// unused this long. Every use renews it and there is no absolute cap:
// the owner asked for a phone that stays signed in (plan amendment A1).
// Sign-out, revocation on the web page, re-enrolment and "Disconnect"
// still end it at once.
const AdminDeviceIdleLimit = 180 * 24 * time.Hour

// AdminDeviceSessionLifetime is a sign-in that did not ask to stay: the
// browser session's 12 hours from issue, never renewed by use.
const AdminDeviceSessionLifetime = SessionLifetime

// adminDeviceTouchResolution throttles last_used_at, and with it the
// sliding expiry: at most one write per token per minute. Minute
// resolution is all "last used" means on the devices list, and a phone
// polls every few seconds while its screen is open.
const adminDeviceTouchResolution = time.Minute

// How a token was issued: with a TOTP code or a recovery code.
const (
	AdminDeviceViaTOTP     = "totp"
	AdminDeviceViaRecovery = "recovery"
)

var ErrAdminDeviceNotFound = errors.New("admin device not found")

// AdminDevice is one phone's admin sign-in. It never carries the token
// or its hash: those cannot be read back out of this repository.
type AdminDevice struct {
	ID          string
	Label       string
	Persistent  bool
	CreatedVia  string
	SchemeBasis string
	CreatedAt   time.Time
	LastUsedAt  time.Time
	ExpiresAt   time.Time
}

// NewAdminDevice is what issuing a sign-in records beside the hash.
type NewAdminDevice struct {
	Label       string
	Persistent  bool
	CreatedVia  string
	SchemeBasis string
}

// DeviceTokenState is the verdict of LookupDeviceToken.
type DeviceTokenState int

const (
	DeviceTokenUnknown DeviceTokenState = iota
	DeviceTokenValid
	DeviceTokenExpired
)

// live is the one expiry rule, shared by lookup, list and purge.
func (d *AdminDevice) live(now time.Time) bool {
	t := now.Unix()
	if t >= d.ExpiresAt.Unix() {
		return false
	}
	// Belt and braces for a persistent sign-in: expires_at already
	// slides to last use + the idle limit, and this holds the line even
	// if a row's expiry was written under a longer limit.
	if d.Persistent && t-d.LastUsedAt.Unix() >= int64(AdminDeviceIdleLimit/time.Second) {
		return false
	}
	return true
}

const adminDeviceColumns = `id, label, persistent, created_via, scheme_basis,
	created_at, last_used_at, expires_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanAdminDevice(row rowScanner) (*AdminDevice, error) {
	var d AdminDevice
	var persistent int
	var created, lastUsed, expires int64
	if err := row.Scan(&d.ID, &d.Label, &persistent, &d.CreatedVia,
		&d.SchemeBasis, &created, &lastUsed, &expires); err != nil {
		return nil, err
	}
	d.Persistent = persistent == 1
	d.CreatedAt = time.Unix(created, 0).UTC()
	d.LastUsedAt = time.Unix(lastUsed, 0).UTC()
	d.ExpiresAt = time.Unix(expires, 0).UTC()
	return &d, nil
}

// scanAdminDevices drains rows, which for a DELETE … RETURNING is also
// what carries the delete out.
func scanAdminDevices(rows *sql.Rows) ([]AdminDevice, error) {
	defer rows.Close()
	out := []AdminDevice{}
	for rows.Next() {
		d, err := scanAdminDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// CreateDeviceToken stores a new sign-in under the hash of its token.
func (r *AdminAuthRepository) CreateDeviceToken(
	tokenHash string, in NewAdminDevice, now time.Time,
) (*AdminDevice, error) {
	t := time.Unix(now.Unix(), 0).UTC()
	d := &AdminDevice{
		ID:          uuid.New().String(),
		Label:       in.Label,
		Persistent:  in.Persistent,
		CreatedVia:  in.CreatedVia,
		SchemeBasis: in.SchemeBasis,
		CreatedAt:   t,
		LastUsedAt:  t,
	}
	if d.CreatedVia == "" {
		d.CreatedVia = AdminDeviceViaTOTP
	}
	if d.Persistent {
		d.ExpiresAt = t.Add(AdminDeviceIdleLimit)
	} else {
		d.ExpiresAt = t.Add(AdminDeviceSessionLifetime)
	}
	persistent := 0
	if d.Persistent {
		persistent = 1
	}
	_, err := database.DB.Exec(`
		INSERT INTO admin_device_tokens
			(id, token_hash, label, persistent, created_via, scheme_basis,
			 created_at, last_used_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		d.ID, tokenHash, d.Label, persistent, d.CreatedVia, d.SchemeBasis,
		t.Unix(), t.Unix(), d.ExpiresAt.Unix())
	if err != nil {
		return nil, err
	}
	return d, nil
}

// LookupDeviceToken resolves a token hash. A sign-in found past its
// expiry is deleted on the spot, so it cannot come back even if the
// clock does, and reported as DeviceTokenExpired together with what it
// was, for the log.
func (r *AdminAuthRepository) LookupDeviceToken(
	tokenHash string, now time.Time,
) (*AdminDevice, DeviceTokenState, error) {
	d, err := scanAdminDevice(database.DB.QueryRow(
		`SELECT `+adminDeviceColumns+` FROM admin_device_tokens
		 WHERE token_hash = ?`, tokenHash))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, DeviceTokenUnknown, nil
	}
	if err != nil {
		return nil, DeviceTokenUnknown, err
	}
	if !d.live(now) {
		// Best-effort, as in SessionValid: the answer is "expired"
		// whether or not the delete lands, and the hourly purge retries.
		_, _ = database.DB.Exec(
			`DELETE FROM admin_device_tokens WHERE id = ?`, d.ID)
		return d, DeviceTokenExpired, nil
	}
	return d, DeviceTokenValid, nil
}

// TouchDeviceToken records a use of a sign-in that LookupDeviceToken
// just found valid and, when it is persistent, slides its expiry to now
// plus the idle limit.
//
// Throttled to one write per token per minute, judged from the
// last_used_at the lookup read — so it holds across restarts with no
// map to grow — and guarded in the UPDATE, so two concurrent requests
// carrying one stale reading cannot both write. Best-effort: a failure
// here must never fail the request the caller actually made.
func (r *AdminAuthRepository) TouchDeviceToken(d *AdminDevice, now time.Time) {
	if now.Sub(d.LastUsedAt) < adminDeviceTouchResolution {
		return
	}
	t := now.Unix()
	_, _ = database.DB.Exec(`
		UPDATE admin_device_tokens
		SET last_used_at = ?,
		    expires_at = CASE WHEN persistent = 1 THEN ? ELSE expires_at END
		WHERE id = ? AND last_used_at <= ?`,
		t, t+int64(AdminDeviceIdleLimit/time.Second), d.ID,
		t-int64(adminDeviceTouchResolution/time.Second))
}

// ListDeviceTokens returns every live sign-in, most recently used
// first. Expired rows are left out; LookupDeviceToken and the hourly
// purge delete them.
func (r *AdminAuthRepository) ListDeviceTokens(now time.Time) ([]AdminDevice, error) {
	rows, err := database.DB.Query(`SELECT ` + adminDeviceColumns + `
		FROM admin_device_tokens
		ORDER BY last_used_at DESC, created_at DESC, id`)
	if err != nil {
		return nil, err
	}
	all, err := scanAdminDevices(rows)
	if err != nil {
		return nil, err
	}
	out := []AdminDevice{}
	for i := range all {
		if all[i].live(now) {
			out = append(out, all[i])
		}
	}
	return out, nil
}

// RevokeDeviceToken ends one sign-in at once and returns what it was.
// A phone signing itself out is the same call with its own id.
func (r *AdminAuthRepository) RevokeDeviceToken(id string) (*AdminDevice, error) {
	rows, err := database.DB.Query(`DELETE FROM admin_device_tokens
		WHERE id = ? RETURNING `+adminDeviceColumns, id)
	if err != nil {
		return nil, err
	}
	gone, err := scanAdminDevices(rows)
	if err != nil {
		return nil, err
	}
	if len(gone) == 0 {
		return nil, ErrAdminDeviceNotFound
	}
	return &gone[0], nil
}

// RevokeAllDeviceTokens ends every phone's sign-in and returns what was
// ended, in one statement, so a sign-in racing it cannot be listed as
// ended and survive.
func (r *AdminAuthRepository) RevokeAllDeviceTokens() ([]AdminDevice, error) {
	rows, err := database.DB.Query(`DELETE FROM admin_device_tokens
		RETURNING ` + adminDeviceColumns)
	if err != nil {
		return nil, err
	}
	return scanAdminDevices(rows)
}

// PurgeExpiredDeviceTokens deletes every sign-in past its expiry and
// returns them, for the log. Housekeeping, run at startup and hourly.
// The predicate is live() negated.
func (r *AdminAuthRepository) PurgeExpiredDeviceTokens(now time.Time) ([]AdminDevice, error) {
	t := now.Unix()
	rows, err := database.DB.Query(`DELETE FROM admin_device_tokens
		WHERE expires_at <= ?
		   OR (persistent = 1 AND last_used_at <= ?)
		RETURNING `+adminDeviceColumns,
		t, t-int64(AdminDeviceIdleLimit/time.Second))
	if err != nil {
		return nil, err
	}
	return scanAdminDevices(rows)
}
