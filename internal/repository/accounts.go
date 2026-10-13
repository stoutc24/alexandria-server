package repository

// Alexandria Accounts (owner, 2026-10-12): a person signs into any app
// once, with a username and password the admin invited them to set, and
// every service the admin set up for them signs in with them.
//
// What this file holds, and what it deliberately does not:
//
//   - A login hangs off a PROFILE. The profile is still the person; this
//     adds a way for them to prove it without the admin approving each
//     device.
//   - Service logins are sealed with the server key (internal/vault) and
//     bound to their row. The admin can add, replace and remove them but
//     no route ever reads a password back out: the person's devices are
//     the only place they are opened, sealed to each device's own key.
//   - Nothing here is an entitlement. Premium stays on the device, against
//     RevenueCat (docs/COMPANION_DESIGN.md).

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

var (
	ErrNoLogin         = errors.New("this person has no sign-in yet")
	ErrUsernameTaken   = errors.New("that username is taken")
	ErrBadUsername     = errors.New("a username is 3 to 32 letters, digits, dots, dashes or underscores, starting with a letter or digit")
	ErrInviteInvalid   = errors.New("that invite is not valid: used, cancelled or expired")
	ErrServiceNotFound = errors.New("service not found")
	ErrBadService      = errors.New("unknown service type or login kind")
	ErrBadServiceURL   = errors.New("the address must be an http:// or https:// address with a host")
	ErrBadPlex         = errors.New("a Plex person needs their key, their server's id, their name and at least one address Plex gave for the server")
	ErrVaultOff        = errors.New("this server has no secrets key, so it cannot keep service logins (set ALEXANDRIA_SECRETS_KEY)")
	ErrDeviceKeyExists = errors.New("this device already has a key")
	ErrNoDeviceKey     = errors.New("this device has no key")
)

// MinAccountPasswordLength is the only password rule for a person's
// sign-in. Eight, as NIST suggests for a human-chosen secret behind a
// rate limit and a lockout; the admin's own login keeps its twelve
// (adminauth.MinPasswordLength), since that one guards the backup.
const MinAccountPasswordLength = 8

// Lockout: after this many wrong passwords or codes in a row, the sign-in
// refuses everything for LoginLockDuration. A right answer resets it.
const (
	LoginFailuresBeforeLock = 10
	LoginLockDuration       = 15 * time.Minute
)

// InviteTTL is how long an invite stays usable.
const InviteTTL = 7 * 24 * time.Hour

// ServiceTypes are the services the app can sign into from an account,
// keyed by the name the app uses for each.
var ServiceTypes = map[string]string{
	"abs":         "Audiobookshelf",
	"jellyfin":    "Jellyfin",
	"plex":        "Plex",
	"kavita":      "Kavita",
	"komga":       "Komga",
	"navidrome":   "Navidrome",
	"booklore":    "Grimmory",
	"calibre":     "Calibre",
	"calibreweb":  "Calibre-Web",
	"storyteller": "Storyteller",
}

// LoginKinds: what the admin hands over for a service. "none" is the
// address alone — a Plex friend with their own plex.tv account, who
// approves Alexandria on Plex's own code screen the first time.
// "plexuser" is a person the admin picked from their own Plex (owner,
// 2026-10-13): Token is that person's key to the admin's server, which
// the admin's browser or phone fetched from plex.tv, and Plex says which
// server and where it is. The server itself never talks to plex.tv.
var LoginKinds = map[string]bool{
	"password": true,
	"token":    true,
	"apikey":   true,
	"none":     true,
	"plexuser": true,
}

type AccountRepository struct {
	vault *vault.Vault
}

// NewAccountRepository takes the server's vault; nil when no secrets key
// is set, which leaves stored service logins off (ErrVaultOff).
func NewAccountRepository(v *vault.Vault) *AccountRepository {
	return &AccountRepository{vault: v}
}

// VaultOn reports whether service logins can be kept.
func (r *AccountRepository) VaultOn() bool { return r.vault != nil }

// ── logins ──────────────────────────────────────────────────────────

// AccountLogin is a person's sign-in.
type AccountLogin struct {
	ProfileID      string
	ProfileName    string
	Username       string
	PasswordHash   string
	TOTPSecret     string // sealed; "" when none
	TOTPEnabled    bool
	LastTOTPStep   int64
	FailedAttempts int
	LockedUntil    int64
	CreatedAt      int64
	LastLoginAt    int64 // 0 when never
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// NormalizeUsername lowercases and checks a username.
func NormalizeUsername(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if !usernameRe.MatchString(s) {
		return "", ErrBadUsername
	}
	return s, nil
}

const loginColumns = `l.profile_id, p.name, l.username, l.password_hash,
	COALESCE(l.totp_secret, ''), l.totp_enabled, l.last_totp_step,
	l.failed_attempts, l.locked_until, l.created_at, COALESCE(l.last_login_at, 0)`

func scanLogin(row interface{ Scan(...any) error }) (*AccountLogin, error) {
	l := &AccountLogin{}
	var enabled int
	err := row.Scan(&l.ProfileID, &l.ProfileName, &l.Username, &l.PasswordHash,
		&l.TOTPSecret, &enabled, &l.LastTOTPStep,
		&l.FailedAttempts, &l.LockedUntil, &l.CreatedAt, &l.LastLoginAt)
	if err == sql.ErrNoRows {
		return nil, ErrNoLogin
	}
	if err != nil {
		return nil, err
	}
	l.TOTPEnabled = enabled == 1
	return l, nil
}

// LoginByUsername finds a sign-in, case-insensitively.
func (r *AccountRepository) LoginByUsername(username string) (*AccountLogin, error) {
	return scanLogin(database.DB.QueryRow(`
		SELECT `+loginColumns+`
		FROM account_logins l JOIN profiles p ON p.id = l.profile_id
		WHERE l.username = ? COLLATE NOCASE`, strings.TrimSpace(username)))
}

// LoginByProfile finds a person's sign-in.
func (r *AccountRepository) LoginByProfile(profileID string) (*AccountLogin, error) {
	return scanLogin(database.DB.QueryRow(`
		SELECT `+loginColumns+`
		FROM account_logins l JOIN profiles p ON p.id = l.profile_id
		WHERE l.profile_id = ?`, profileID))
}

// RecordFailure counts a wrong password or code and locks the sign-in
// once LoginFailuresBeforeLock are in a row. It returns when the lock
// lifts, 0 when not locked.
func (r *AccountRepository) RecordFailure(profileID string, now time.Time) (int64, error) {
	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var failures int
	if err := tx.QueryRow(`SELECT failed_attempts FROM account_logins WHERE profile_id = ?`,
		profileID).Scan(&failures); err != nil {
		return 0, err
	}
	failures++
	var lockedUntil int64
	if failures >= LoginFailuresBeforeLock {
		lockedUntil = now.Add(LoginLockDuration).Unix()
		failures = 0
	}
	if _, err := tx.Exec(`
		UPDATE account_logins SET failed_attempts = ?,
			locked_until = CASE WHEN ? > 0 THEN ? ELSE locked_until END
		WHERE profile_id = ?`, failures, lockedUntil, lockedUntil, profileID); err != nil {
		return 0, err
	}
	return lockedUntil, tx.Commit()
}

// RecordSuccess clears the failure count and stamps the sign-in.
func (r *AccountRepository) RecordSuccess(profileID string, now time.Time) error {
	_, err := database.DB.Exec(`
		UPDATE account_logins SET failed_attempts = 0, locked_until = 0, last_login_at = ?
		WHERE profile_id = ?`, now.Unix(), profileID)
	return err
}

// SetPassword replaces a person's password hash.
func (r *AccountRepository) SetPassword(profileID, hash string, now time.Time) error {
	res, err := database.DB.Exec(`
		UPDATE account_logins SET password_hash = ?, updated_at = ?,
			failed_attempts = 0, locked_until = 0
		WHERE profile_id = ?`, hash, now.Unix(), profileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoLogin
	}
	return nil
}

// DeleteLogin removes a person's sign-in. Their devices stay linked and
// their services stay set up; they simply can no longer sign in anew.
func (r *AccountRepository) DeleteLogin(profileID string) error {
	res, err := database.DB.Exec(`DELETE FROM account_logins WHERE profile_id = ?`, profileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoLogin
	}
	return nil
}

// totpAD binds a sealed two-step secret to its person.
func totpAD(profileID string) string { return "account-totp|" + profileID }

// SetPendingTOTP stores a fresh two-step secret, not yet switched on.
func (r *AccountRepository) SetPendingTOTP(profileID, secret string, now time.Time) error {
	if r.vault == nil {
		return ErrVaultOff
	}
	sealed, err := r.vault.Seal([]byte(secret), totpAD(profileID))
	if err != nil {
		return err
	}
	res, err := database.DB.Exec(`
		UPDATE account_logins SET totp_secret = ?, totp_enabled = 0, updated_at = ?
		WHERE profile_id = ?`, sealed, now.Unix(), profileID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoLogin
	}
	return nil
}

// OpenTOTP opens a sign-in's two-step secret; "" when it has none.
func (r *AccountRepository) OpenTOTP(l *AccountLogin) (string, error) {
	if l.TOTPSecret == "" {
		return "", nil
	}
	if r.vault == nil {
		return "", ErrVaultOff
	}
	b, err := r.vault.Open(l.TOTPSecret, totpAD(l.ProfileID))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SpendTOTPStep records a code's time step, refusing one not newer than
// the last spent — atomically, so two sign-ins racing with the same code
// cannot both succeed. It also switches two-step on when enable is true.
func (r *AccountRepository) SpendTOTPStep(profileID string, step int64, enable bool) (bool, error) {
	q := `UPDATE account_logins SET last_totp_step = ? WHERE profile_id = ? AND last_totp_step < ?`
	if enable {
		q = `UPDATE account_logins SET last_totp_step = ?, totp_enabled = 1 WHERE profile_id = ? AND last_totp_step < ?`
	}
	res, err := database.DB.Exec(q, step, profileID, step)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// DisableTOTP switches two-step off and forgets the secret.
func (r *AccountRepository) DisableTOTP(profileID string, now time.Time) error {
	_, err := database.DB.Exec(`
		UPDATE account_logins SET totp_secret = NULL, totp_enabled = 0, updated_at = ?
		WHERE profile_id = ?`, now.Unix(), profileID)
	return err
}

// ── invites ─────────────────────────────────────────────────────────

// Invite is a pending way in for one person.
type Invite struct {
	ID          string `json:"id"`
	ProfileID   string `json:"profileId"`
	ProfileName string `json:"profileName"`
	// Purpose is "invite" for a first sign-in, "reset" for a new password
	// on an existing one.
	Purpose   string `json:"purpose"`
	CreatedAt int64  `json:"createdAt"`
	ExpiresAt int64  `json:"expiresAt"`
}

// inviteCodeAlphabet leaves out 0 O 1 I L, which get misread aloud.
const inviteCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// InviteTokenPrefix marks an invite token in a link or a pasted string.
const InviteTokenPrefix = "alxi_"

func hashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// NormalizeInviteCode makes typing forgiving: case, spaces and dashes are
// presentation.
func NormalizeInviteCode(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	code = strings.ReplaceAll(code, "-", "")
	return strings.ReplaceAll(code, " ", "")
}

func newInviteCode() (string, error) {
	n := len(inviteCodeAlphabet)
	limit := 256 - (256 % n)
	out := make([]byte, 0, 8)
	buf := make([]byte, 1)
	for len(out) < 8 {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if int(buf[0]) < limit {
			out = append(out, inviteCodeAlphabet[int(buf[0])%n])
		}
	}
	return string(out), nil
}

// CreateInvite makes a single-use invite for a person, replacing any
// earlier one still waiting. It returns the long token (for a link or a
// QR code) and a short code (to read out or type), both shown once; only
// their hashes are stored. The code is 8 characters from 31 (~40 bits),
// enough behind single use, a week's expiry and the sign-in rate limits.
func (r *AccountRepository) CreateInvite(
	profileID, purpose string, ttl time.Duration, now time.Time,
) (token, code string, inv *Invite, err error) {
	if purpose != "invite" && purpose != "reset" {
		return "", "", nil, fmt.Errorf("unknown invite purpose %q", purpose)
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", nil, err
	}
	token = InviteTokenPrefix + hex.EncodeToString(raw)
	code, err = newInviteCode()
	if err != nil {
		return "", "", nil, err
	}
	inv = &Invite{
		ID:        uuid.New().String(),
		ProfileID: profileID,
		Purpose:   purpose,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(ttl).Unix(),
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return "", "", nil, err
	}
	defer tx.Rollback()
	if err := tx.QueryRow(`SELECT name FROM profiles WHERE id = ?`, profileID).
		Scan(&inv.ProfileName); err == sql.ErrNoRows {
		return "", "", nil, ErrProfileNotFound
	} else if err != nil {
		return "", "", nil, err
	}
	if _, err := tx.Exec(`DELETE FROM account_invites WHERE profile_id = ? AND used_at IS NULL`,
		profileID); err != nil {
		return "", "", nil, err
	}
	if _, err := tx.Exec(`
		INSERT INTO account_invites (id, profile_id, token_hash, code_hash, purpose, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, profileID, hashSecret(token), hashSecret(code), purpose,
		inv.CreatedAt, inv.ExpiresAt); err != nil {
		return "", "", nil, err
	}
	if err := tx.Commit(); err != nil {
		return "", "", nil, err
	}
	return token, code[:4] + "-" + code[4:], inv, nil
}

// FindInvite resolves a token or a typed code to a live invite.
func (r *AccountRepository) FindInvite(tokenOrCode string, now time.Time) (*Invite, error) {
	s := strings.TrimSpace(tokenOrCode)
	if s == "" {
		return nil, ErrInviteInvalid
	}
	hash := hashSecret(s)
	if !strings.HasPrefix(s, InviteTokenPrefix) {
		hash = hashSecret(NormalizeInviteCode(s))
	}
	inv := &Invite{}
	err := database.DB.QueryRow(`
		SELECT i.id, i.profile_id, p.name, i.purpose, i.created_at, i.expires_at
		FROM account_invites i JOIN profiles p ON p.id = i.profile_id
		WHERE (i.token_hash = ? OR i.code_hash = ?)
		  AND i.used_at IS NULL AND i.expires_at > ?`,
		hash, hash, now.Unix()).Scan(&inv.ID, &inv.ProfileID, &inv.ProfileName,
		&inv.Purpose, &inv.CreatedAt, &inv.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, ErrInviteInvalid
	}
	return inv, err
}

// PendingInvite is a person's live invite, for the admin's view; nil when
// there is none.
func (r *AccountRepository) PendingInvite(profileID string, now time.Time) (*Invite, error) {
	inv := &Invite{}
	err := database.DB.QueryRow(`
		SELECT i.id, i.profile_id, p.name, i.purpose, i.created_at, i.expires_at
		FROM account_invites i JOIN profiles p ON p.id = i.profile_id
		WHERE i.profile_id = ? AND i.used_at IS NULL AND i.expires_at > ?
		ORDER BY i.created_at DESC LIMIT 1`, profileID, now.Unix()).
		Scan(&inv.ID, &inv.ProfileID, &inv.ProfileName, &inv.Purpose, &inv.CreatedAt, &inv.ExpiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return inv, err
}

// CancelInvites withdraws a person's waiting invites.
func (r *AccountRepository) CancelInvites(profileID string) error {
	_, err := database.DB.Exec(`DELETE FROM account_invites WHERE profile_id = ? AND used_at IS NULL`, profileID)
	return err
}

// AcceptInvite spends an invite and sets the sign-in it promised: a new
// login for "invite", a new password for "reset". Atomic: an invite can
// be spent once, however many devices race to use it.
func (r *AccountRepository) AcceptInvite(inv *Invite, username, passwordHash string, now time.Time) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`
		UPDATE account_invites SET used_at = ?
		WHERE id = ? AND used_at IS NULL AND expires_at > ?`, now.Unix(), inv.ID, now.Unix())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrInviteInvalid
	}
	switch inv.Purpose {
	case "invite":
		_, err = tx.Exec(`
			INSERT INTO account_logins (profile_id, username, password_hash, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(profile_id) DO UPDATE SET
				username = excluded.username, password_hash = excluded.password_hash,
				failed_attempts = 0, locked_until = 0, updated_at = excluded.updated_at`,
			inv.ProfileID, username, passwordHash, now.Unix(), now.Unix())
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
			return ErrUsernameTaken
		}
	case "reset":
		res, err = tx.Exec(`
			UPDATE account_logins SET password_hash = ?, updated_at = ?,
				failed_attempts = 0, locked_until = 0
			WHERE profile_id = ?`, passwordHash, now.Unix(), inv.ProfileID)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				err = ErrNoLogin
			}
		}
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UsernameAvailable reports whether username is free, or already this
// person's own.
func (r *AccountRepository) UsernameAvailable(username, profileID string) (bool, error) {
	var owner string
	err := database.DB.QueryRow(`SELECT profile_id FROM account_logins WHERE username = ? COLLATE NOCASE`,
		username).Scan(&owner)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return owner == profileID, nil
}

// ── services ────────────────────────────────────────────────────────

// ServiceCredentials are what a device signs in with. Sealed at rest.
type ServiceCredentials struct {
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	APIKey   string `json:"apiKey,omitempty"`
}

// PlexService is the rest of a "plexuser" service: which of the admin's
// Plex servers, who on it, and every address Plex gave for it (local,
// remote, relay), so a device can reach it the way Plex's own apps do.
// None of it is secret; the person's key is Credentials.Token.
type PlexService struct {
	ServerID    string           `json:"serverId"`
	ServerName  string           `json:"serverName"`
	UserID      int64            `json:"userId"`
	UserTitle   string           `json:"userTitle"`
	Connections []PlexConnection `json:"connections"`
}

// PlexConnection is one address of a Plex server, as plex.tv lists it.
type PlexConnection struct {
	URI   string `json:"uri"`
	Local bool   `json:"local,omitempty"`
	Relay bool   `json:"relay,omitempty"`
}

var plexServerIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// maxPlexConnections: plex.tv lists a handful per server (LAN, plex.direct,
// relay, IPv6); a dozen is room for all of them and no more.
const maxPlexConnections = 12

// clip keeps at most n bytes of s, cut between characters: Plex names
// and the names an admin types are often not ASCII, and a byte cut could
// split one ("Grand-mère") into invalid UTF-8.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// normalize checks a picked Plex person and tidies it: addresses that are
// not http(s) are dropped, duplicates once, and at least one must remain.
func (p *PlexService) normalize() error {
	p.ServerID = strings.TrimSpace(p.ServerID)
	p.ServerName = clip(strings.TrimSpace(p.ServerName), 80)
	p.UserTitle = clip(strings.TrimSpace(p.UserTitle), 80)
	if !plexServerIDRe.MatchString(p.ServerID) || p.UserTitle == "" || p.UserID < 0 {
		return ErrBadPlex
	}
	if p.ServerName == "" {
		p.ServerName = "Plex"
	}
	seen := map[string]bool{}
	conns := []PlexConnection{}
	for _, c := range p.Connections {
		u, err := NormalizeServiceURL(c.URI)
		if err != nil || seen[u] {
			continue
		}
		seen[u] = true
		conns = append(conns, PlexConnection{URI: u, Local: c.Local, Relay: c.Relay})
		if len(conns) == maxPlexConnections {
			break
		}
	}
	if len(conns) == 0 {
		return ErrBadPlex
	}
	p.Connections = conns
	return nil
}

// preferredURI is the address shown for a Plex service and the one an
// older app, which knows nothing of the list, would use: a remote https
// one reaches the server from anywhere, then any direct one, then a relay.
func (p *PlexService) preferredURI() string {
	for _, c := range p.Connections {
		if !c.Local && !c.Relay && strings.HasPrefix(c.URI, "https://") {
			return c.URI
		}
	}
	for _, c := range p.Connections {
		if !c.Relay {
			return c.URI
		}
	}
	return p.Connections[0].URI
}

// AccountService is one service set up for a person. Credentials is only
// filled when a device asks for its own services; the admin's view shows
// the username and whether a secret is set, never the secret.
type AccountService struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	BaseURL   string `json:"baseUrl"`
	LoginKind string `json:"loginKind"`
	Username  string `json:"username,omitempty"`
	HasSecret bool   `json:"hasSecret"`
	// Plex is the picked person's server and name ("plexuser" only).
	Plex *PlexService `json:"plex,omitempty"`
	// Unreadable is true when the stored login no longer opens — the
	// server key changed since it was saved. The admin re-enters it.
	Unreadable  bool                `json:"unreadable,omitempty"`
	Priority    int                 `json:"priority"`
	UpdatedAt   int64               `json:"updatedAt"`
	Credentials *ServiceCredentials `json:"-"`
}

// ServiceInput is an add or an edit. Nil secret fields are left as they
// are on an edit; an empty string clears one. Plex, likewise, is kept on
// an edit that leaves it out, and replaced by one that names it (a Plex
// person picked again).
type ServiceInput struct {
	Type      string       `json:"type"`
	Name      string       `json:"name"`
	BaseURL   string       `json:"baseUrl"`
	LoginKind string       `json:"loginKind"`
	Username  *string      `json:"username"`
	Password  *string      `json:"password"`
	Token     *string      `json:"token"`
	APIKey    *string      `json:"apiKey"`
	Plex      *PlexService `json:"plex"`
}

func serviceAD(profileID, id string) string { return "account-service|" + profileID + "|" + id }

// NormalizeServiceURL checks an address and drops a trailing slash.
func NormalizeServiceURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", ErrBadServiceURL
	}
	return strings.TrimRight(raw, "/"), nil
}

func (r *AccountRepository) sealCredentials(profileID, id string, c ServiceCredentials) (sql.NullString, error) {
	if c == (ServiceCredentials{}) {
		return sql.NullString{}, nil
	}
	if r.vault == nil {
		return sql.NullString{}, ErrVaultOff
	}
	b, err := json.Marshal(c)
	if err != nil {
		return sql.NullString{}, err
	}
	sealed, err := r.vault.Seal(b, serviceAD(profileID, id))
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: sealed, Valid: true}, nil
}

func (r *AccountRepository) openCredentials(profileID, id string, sealed sql.NullString) (*ServiceCredentials, error) {
	if !sealed.Valid || sealed.String == "" {
		return &ServiceCredentials{}, nil
	}
	if r.vault == nil {
		return nil, ErrVaultOff
	}
	b, err := r.vault.Open(sealed.String, serviceAD(profileID, id))
	if err != nil {
		return nil, err
	}
	var c ServiceCredentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// setsSecret is whether the input names any login field at all.
func (in ServiceInput) setsSecret() bool {
	return in.Username != nil || in.Password != nil || in.Token != nil || in.APIKey != nil
}

// clearsSecret is whether it names login fields and every one is empty.
func (in ServiceInput) clearsSecret() bool {
	for _, p := range []*string{in.Username, in.Password, in.Token, in.APIKey} {
		if p != nil && strings.TrimSpace(*p) != "" {
			return false
		}
	}
	return in.setsSecret()
}

// plexColumn is how a service's PlexService is stored: JSON, or NULL for
// every service that is not a picked Plex person.
func plexColumn(p *PlexService) (sql.NullString, error) {
	if p == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(p)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func readPlexColumn(v sql.NullString) *PlexService {
	if !v.Valid || v.String == "" {
		return nil
	}
	var p PlexService
	if json.Unmarshal([]byte(v.String), &p) != nil {
		return nil
	}
	return &p
}

// picksPlexPerson is whether an input hands over a Plex person's key: the
// key is what makes the pick, and an add or a new pick always carries one.
func (in ServiceInput) picksPlexPerson() bool {
	return in.Token != nil && strings.TrimSpace(*in.Token) != ""
}

func applyInput(c *ServiceCredentials, in ServiceInput) {
	if in.Username != nil {
		c.Username = strings.TrimSpace(*in.Username)
	}
	if in.Password != nil {
		c.Password = *in.Password
	}
	if in.Token != nil {
		c.Token = strings.TrimSpace(*in.Token)
	}
	if in.APIKey != nil {
		c.APIKey = strings.TrimSpace(*in.APIKey)
	}
}

// ListServices returns a person's services in priority order. With
// withSecrets the credentials are opened; a row that will not open is
// marked Unreadable rather than failing the whole list.
func (r *AccountRepository) ListServices(profileID string, withSecrets bool) ([]AccountService, error) {
	rows, err := database.DB.Query(`
		SELECT id, service_type, name, base_url, login_kind, credentials, plex, priority, updated_at
		FROM account_services WHERE profile_id = ?
		ORDER BY priority, created_at`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AccountService{}
	for rows.Next() {
		var s AccountService
		var sealed, plex sql.NullString
		if err := rows.Scan(&s.ID, &s.Type, &s.Name, &s.BaseURL, &s.LoginKind,
			&sealed, &plex, &s.Priority, &s.UpdatedAt); err != nil {
			return nil, err
		}
		s.Plex = readPlexColumn(plex)
		s.HasSecret = sealed.Valid && sealed.String != ""
		if s.HasSecret || withSecrets {
			c, err := r.openCredentials(profileID, s.ID, sealed)
			switch {
			case err == nil:
				s.Username = c.Username
				s.HasSecret = c.Password != "" || c.Token != "" || c.APIKey != ""
				if withSecrets {
					s.Credentials = c
				}
			default:
				s.Unreadable = true
			}
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func validateInput(in *ServiceInput) error {
	in.Type = strings.ToLower(strings.TrimSpace(in.Type))
	in.LoginKind = strings.ToLower(strings.TrimSpace(in.LoginKind))
	if _, ok := ServiceTypes[in.Type]; !ok || !LoginKinds[in.LoginKind] {
		return ErrBadService
	}
	if in.LoginKind == "plexuser" {
		if in.Type != "plex" {
			return ErrBadService
		}
		if in.Plex == nil {
			return ErrBadPlex
		}
		if err := in.Plex.normalize(); err != nil {
			return err
		}
		// Plex says where the server is; nobody types it.
		if strings.TrimSpace(in.BaseURL) == "" {
			in.BaseURL = in.Plex.preferredURI()
		}
	} else {
		in.Plex = nil
	}
	u, err := NormalizeServiceURL(in.BaseURL)
	if err != nil {
		return err
	}
	in.BaseURL = u
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		in.Name = ServiceTypes[in.Type]
	}
	if len(in.Name) > 80 {
		in.Name = clip(in.Name, 80)
	}
	return nil
}

// AddService sets a service up for a person, last in priority order.
func (r *AccountRepository) AddService(profileID string, in ServiceInput, now time.Time) (*AccountService, error) {
	if err := validateInput(&in); err != nil {
		return nil, err
	}
	if in.LoginKind == "plexuser" && !in.picksPlexPerson() {
		return nil, ErrBadPlex
	}
	plex, err := plexColumn(in.Plex)
	if err != nil {
		return nil, err
	}
	id := uuid.New().String()
	var c ServiceCredentials
	applyInput(&c, in)
	sealed, err := r.sealCredentials(profileID, id, c)
	if err != nil {
		return nil, err
	}
	var next int
	if err := database.DB.QueryRow(`SELECT COALESCE(MAX(priority) + 1, 0) FROM account_services WHERE profile_id = ?`,
		profileID).Scan(&next); err != nil {
		return nil, err
	}
	if _, err := database.DB.Exec(`
		INSERT INTO account_services (id, profile_id, service_type, name, base_url, login_kind,
			credentials, plex, priority, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, profileID, in.Type, in.Name, in.BaseURL, in.LoginKind, sealed, plex, next,
		now.Unix(), now.Unix()); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "foreign key") {
			return nil, ErrProfileNotFound
		}
		return nil, err
	}
	return r.service(profileID, id)
}

func (r *AccountRepository) service(profileID, id string) (*AccountService, error) {
	all, err := r.ListServices(profileID, false)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, ErrServiceNotFound
}

// UpdateService edits a service. Secret fields left nil keep their value.
func (r *AccountRepository) UpdateService(profileID, id string, in ServiceInput, now time.Time) (*AccountService, error) {
	var sealed, curPlex sql.NullString
	var cur ServiceInput
	err := database.DB.QueryRow(`
		SELECT service_type, name, base_url, login_kind, credentials, plex
		FROM account_services WHERE profile_id = ? AND id = ?`, profileID, id).
		Scan(&cur.Type, &cur.Name, &cur.BaseURL, &cur.LoginKind, &sealed, &curPlex)
	if err == sql.ErrNoRows {
		return nil, ErrServiceNotFound
	}
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Type) == "" {
		in.Type = cur.Type
	}
	if strings.TrimSpace(in.LoginKind) == "" {
		in.LoginKind = cur.LoginKind
	}
	if strings.TrimSpace(in.Name) == "" {
		in.Name = cur.Name
	}
	// A Plex person picked again brings their server, their addresses and
	// their key together; an edit that leaves the pick out (a new name, a
	// new place in the order) keeps the one stored.
	repick := in.Plex != nil
	if strings.ToLower(strings.TrimSpace(in.LoginKind)) == "plexuser" {
		if !repick {
			in.Plex = readPlexColumn(curPlex)
		} else if !in.picksPlexPerson() {
			return nil, ErrBadPlex
		}
	}
	if strings.TrimSpace(in.BaseURL) == "" && !repick {
		in.BaseURL = cur.BaseURL
	}
	if err := validateInput(&in); err != nil {
		return nil, err
	}
	plex, err := plexColumn(in.Plex)
	if err != nil {
		return nil, err
	}
	newSealed := sealed
	c, err := r.openCredentials(profileID, id, sealed)
	switch {
	case err == nil:
		applyInput(c, in)
		if newSealed, err = r.sealCredentials(profileID, id, *c); err != nil {
			return nil, err
		}
	case !in.setsSecret():
		// The login cannot be read here — no secrets key, or a key that
		// changed since it was saved — and this edit does not touch it (a
		// new name, a new address). It stays exactly as stored: there when
		// the key comes back, still flagged for the admin to enter again if
		// the key changed. Writing back what could be read — nothing —
		// erased it.
	case in.clearsSecret():
		// Cleared on purpose (the login kind changed): forgetting it needs
		// no key.
		newSealed = sql.NullString{}
	default:
		// New secrets over a login that will not open replace it whole,
		// which needs the key.
		var fresh ServiceCredentials
		applyInput(&fresh, in)
		if newSealed, err = r.sealCredentials(profileID, id, fresh); err != nil {
			return nil, err
		}
	}
	if _, err := database.DB.Exec(`
		UPDATE account_services SET service_type = ?, name = ?, base_url = ?, login_kind = ?,
			credentials = ?, plex = ?, updated_at = ?
		WHERE profile_id = ? AND id = ?`,
		in.Type, in.Name, in.BaseURL, in.LoginKind, newSealed, plex, now.Unix(), profileID, id); err != nil {
		return nil, err
	}
	return r.service(profileID, id)
}

// DeleteService removes a service from a person. Their devices sign out of
// it at their next check (and keep its downloads).
func (r *AccountRepository) DeleteService(profileID, id string) error {
	res, err := database.DB.Exec(`DELETE FROM account_services WHERE profile_id = ? AND id = ?`, profileID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrServiceNotFound
	}
	return nil
}

// ReorderServices sets the priority order: ids first to last. Every one of
// the person's services must be named exactly once.
func (r *AccountRepository) ReorderServices(profileID string, ids []string, now time.Time) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id FROM account_services WHERE profile_id = ?`, profileID)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		have[id] = true
	}
	rows.Close()
	seen := map[string]bool{}
	for _, id := range ids {
		if !have[id] || seen[id] {
			return ErrServiceNotFound
		}
		seen[id] = true
	}
	if len(seen) != len(have) {
		return ErrServiceNotFound
	}
	for i, id := range ids {
		if _, err := tx.Exec(`UPDATE account_services SET priority = ?, updated_at = ? WHERE id = ?`,
			i, now.Unix(), id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ServicesRevision is a fingerprint of a person's services: it changes
// whenever one is added, edited, removed or reordered, so a device can ask
// "anything new?" without being sent anything.
func (r *AccountRepository) ServicesRevision(profileID string) (string, error) {
	rows, err := database.DB.Query(`
		SELECT id, priority, updated_at FROM account_services WHERE profile_id = ?`, profileID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var id string
		var prio int
		var updated int64
		if err := rows.Scan(&id, &prio, &updated); err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("%s:%d:%d", id, prio, updated))
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:8]), nil
}

// ── device keys ─────────────────────────────────────────────────────

// SetDeviceKey records a device's public key, once. A key cannot be
// replaced through here: a stolen device token must not be able to swap
// in a key of its own and have the services sealed to it.
func (r *AccountRepository) SetDeviceKey(tokenID, publicKey string, now time.Time) error {
	_, err := database.DB.Exec(`
		INSERT INTO account_device_keys (token_id, public_key, created_at) VALUES (?, ?, ?)`,
		tokenID, publicKey, now.Unix())
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique") {
		existing, _ := r.DeviceKey(tokenID)
		if existing == publicKey {
			return nil
		}
		return ErrDeviceKeyExists
	}
	return err
}

// DeviceKey is a device's stored public key.
func (r *AccountRepository) DeviceKey(tokenID string) (string, error) {
	var key string
	err := database.DB.QueryRow(`SELECT public_key FROM account_device_keys WHERE token_id = ?`,
		tokenID).Scan(&key)
	if err == sql.ErrNoRows {
		return "", ErrNoDeviceKey
	}
	return key, err
}
