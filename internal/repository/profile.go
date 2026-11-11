package repository

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

var (
	ErrProfileNotFound = errors.New("profile not found")
	ErrProfileExists   = errors.New("a profile with that name already exists")
	ErrTokenNotFound   = errors.New("device token not found")
)

// tokenPrefix marks a profile token so a person pasting one into the app
// can tell it apart from the admin token at a glance.
const tokenPrefix = "alxp_"

// LooksLikeDeviceToken: shaped like a profile (device) token — what a
// signed-out device still holds — and not an admin credential.
func LooksLikeDeviceToken(token string) bool {
	return strings.HasPrefix(token, tokenPrefix)
}

// Profile is a name tag, not an account.
//
// There is no password and no registration. Its only job is keeping one
// person's stats out of another person's totals.
type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	// Kids: this person's devices show the simple Kids screens (owner,
	// 2026-11-10). A look, not a lock on content: what they may play is
	// still whatever their own media-server users allow.
	Kids bool `json:"kids"`
}

// DeviceToken describes one linked device. The token itself is not here
// and cannot be recovered — only its hash is stored.
type DeviceToken struct {
	ID         string     `json:"id"`
	Label      string     `json:"label,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt *time.Time `json:"lastSeenAt,omitempty"`
}

// ProfileSummary is what an ADMIN is allowed to see.
//
// Deliberately counts, device labels and timestamps only — never a
// title, an author, or an event. The admin's job is managing storage and
// membership, not reading anyone's history. See docs/COMPANION_DESIGN.md
// on the limits of that promise.
type ProfileSummary struct {
	Profile
	// Look is how this person's apps look, as their own devices set it
	// (PUT /devices/me/look). Read-only here: allowlisted words, never
	// free text.
	Look          Look          `json:"look"`
	LookUpdatedAt int64         `json:"lookUpdatedAt"`
	Devices       []DeviceToken `json:"devices"`
	EventCount    int           `json:"eventCount"`
	TotalSeconds  int64         `json:"totalSeconds"`
}

// Caller is a resolved profile token: who is asking, and from which
// device, so activity can be timestamped per device.
type Caller struct {
	ProfileID   string
	ProfileName string
	TokenID     string
	// TokenLabel is the device name chosen at link time ("iPhone",
	// "Living Room TV"). The token IS the device identity: the party
	// line uses it to know which device holds the leader seat, so no
	// client ever has to mint or persist a key of its own.
	TokenLabel string
}

type ProfileRepository struct{}

func NewProfileRepository() *ProfileRepository {
	return &ProfileRepository{}
}

// HashToken reduces a token to what we are willing to store.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return tokenPrefix + hex.EncodeToString(buf), nil
}

// Create adds a profile. It mints no token: devices are linked, and
// each link mints its own.
func (r *ProfileRepository) Create(name string) (*Profile, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("name is required")
	}

	id := uuid.New().String()
	now := time.Now().UTC()

	_, err := database.DB.Exec(`
		INSERT INTO profiles (id, name, created_at) VALUES (?, ?, ?)
	`, id, name, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, ErrProfileExists
		}
		return nil, err
	}
	return &Profile{ID: id, Name: name, CreatedAt: now}, nil
}

// GetByName finds an existing profile, so linking a second device does
// not create a duplicate person.
func (r *ProfileRepository) GetByName(name string) (*Profile, error) {
	p := &Profile{}
	err := database.DB.QueryRow(`
		SELECT id, name, created_at, kids FROM profiles WHERE name = ?
	`, strings.TrimSpace(name)).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Kids)
	if err == sql.ErrNoRows {
		return nil, ErrProfileNotFound
	}
	return p, err
}

func (r *ProfileRepository) GetByID(id string) (*Profile, error) {
	p := &Profile{}
	err := database.DB.QueryRow(`
		SELECT id, name, created_at, kids FROM profiles WHERE id = ?
	`, id).Scan(&p.ID, &p.Name, &p.CreatedAt, &p.Kids)
	if err == sql.ErrNoRows {
		return nil, ErrProfileNotFound
	}
	return p, err
}

// MintToken issues a token for ONE device.
//
// Returned once and never recoverable. Other devices are untouched —
// that is the point of per-device tokens.
func (r *ProfileRepository) MintToken(
	profileID, label string,
) (string, error) {
	token, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = database.DB.Exec(`
		INSERT INTO profile_tokens (id, profile_id, token_hash, label, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, uuid.New().String(), profileID, HashToken(token),
		strings.TrimSpace(label), time.Now().UTC())
	if err != nil {
		return "", err
	}
	return token, nil
}

// FindByToken resolves a token to its profile and device.
func (r *ProfileRepository) FindByToken(token string) (*Caller, error) {
	c := &Caller{}
	err := database.DB.QueryRow(`
		SELECT t.id, p.id, p.name, COALESCE(t.label, '')
		FROM profile_tokens t
		JOIN profiles p ON p.id = t.profile_id
		WHERE t.token_hash = ?
	`, HashToken(token)).Scan(&c.TokenID, &c.ProfileID, &c.ProfileName, &c.TokenLabel)

	if err == sql.ErrNoRows {
		return nil, ErrProfileNotFound
	}
	if err != nil {
		return nil, err
	}
	return c, nil
}

// lastSeenWritten throttles TouchLastSeen: a following TV holds a
// long-poll open every ~15s and the leader heartbeats every 5s, so an
// unconditional UPDATE per authed call became a steady stream of
// SQLite writes that told nobody anything new. Minute resolution is
// all "last seen" has ever meant to the devices page.
var lastSeenWritten sync.Map // tokenID -> time.Time of the last write

const lastSeenResolution = time.Minute

// TouchLastSeen records device activity. Best-effort: a failure here
// must never fail the request the caller actually made.
func (r *ProfileRepository) TouchLastSeen(tokenID string) {
	now := time.Now()
	if prev, ok := lastSeenWritten.Load(tokenID); ok {
		if now.Sub(prev.(time.Time)) < lastSeenResolution {
			return
		}
	}
	lastSeenWritten.Store(tokenID, now)
	_, _ = database.DB.Exec(
		`UPDATE profile_tokens SET last_seen_at = ? WHERE id = ?`,
		time.Now().UTC(), tokenID,
	)
}

// ListTokens returns a profile's devices, without their tokens.
func (r *ProfileRepository) ListTokens(profileID string) ([]DeviceToken, error) {
	rows, err := database.DB.Query(`
		SELECT id, COALESCE(label, ''), created_at, last_seen_at
		FROM profile_tokens WHERE profile_id = ?
		ORDER BY created_at
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []DeviceToken{}
	for rows.Next() {
		var d DeviceToken
		var lastSeen sql.NullTime
		if err := rows.Scan(&d.ID, &d.Label, &d.CreatedAt, &lastSeen); err != nil {
			return nil, err
		}
		if lastSeen.Valid {
			d.LastSeenAt = &lastSeen.Time
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeMinted takes back a token minted moments ago whose row could not
// be read back — a link claim that could not finish. It never reached
// anyone, and no key was kept for it.
func (r *ProfileRepository) RevokeMinted(token string) error {
	_, err := database.DB.Exec(
		`DELETE FROM profile_tokens WHERE token_hash = ?`, HashToken(token))
	return err
}

// RevokeToken unlinks ONE device — a lost phone, without disturbing the
// watch or the TV.
func (r *ProfileRepository) RevokeToken(tokenID string) error {
	// Its Alexandria Accounts key goes with it, explicitly, as everything
	// a deletion removes here does.
	if _, err := database.DB.Exec(
		`DELETE FROM account_device_keys WHERE token_id = ?`, tokenID); err != nil {
		return err
	}
	// And the Manage server sign-in this device made with its person's
	// account, if it made one: a phone signed out here must not go on
	// managing the server (security review, 2026-11-10).
	if _, err := database.DB.Exec(
		`DELETE FROM admin_device_tokens WHERE via_token = ? AND via_token != ''`, tokenID); err != nil {
		return err
	}
	res, err := database.DB.Exec(
		`DELETE FROM profile_tokens WHERE id = ?`, tokenID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// RevokeAllTokens signs out EVERY device of one person (owner,
// 2026-10-30: sign users out "remotely, from the server manager"): their
// link tokens, the Alexandria Accounts keys sealed to them, and their
// phones' Manage-server sign-ins. Their history, their account login and
// their services stay; each device has to sign in again. Returns how many
// devices it signed out (Manage-server sign-ins not counted).
func (r *ProfileRepository) RevokeAllTokens(profileID string) (int, error) {
	if _, err := r.GetByID(profileID); err != nil {
		return 0, err
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM account_device_keys WHERE token_id IN
		(SELECT id FROM profile_tokens WHERE profile_id = ?)`, profileID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`DELETE FROM admin_device_tokens
		WHERE profile_id = ? AND profile_id != ''`, profileID); err != nil {
		return 0, err
	}
	// A link approved for this person but not yet collected by its device
	// is a sign-in about to happen: it goes too, or that device walks in a
	// moment after "sign out all devices" (security review, 2026-11-10).
	if _, err := tx.Exec(`DELETE FROM link_requests WHERE profile_id = ?`, profileID); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM profile_tokens WHERE profile_id = ?`, profileID)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// RenameToken changes the label a device was linked with — the name the
// party shows for it. The label was fixed at link time and nothing could
// change it, so "Apple TV" stayed "Apple TV" on every other screen no
// matter what the TV's own Settings said. The token id remains the
// identity; only the label moves, and the next request carries it.
func (r *ProfileRepository) RenameToken(tokenID, label string) error {
	res, err := database.DB.Exec(
		`UPDATE profile_tokens SET label = ? WHERE id = ?`,
		strings.TrimSpace(label), tokenID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrTokenNotFound
	}
	return nil
}

// ListIDs returns every profile's id, ordered by name as List orders
// them: for a route that needs the people but none of their stats or
// devices (handlers.AccountsHandler.Summary).
func (r *ProfileRepository) ListIDs() ([]string, error) {
	rows, err := database.DB.Query(`SELECT id FROM profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetKids turns the Kids screens on or off for a person. Their devices read
// it on GET /devices/me and switch within a minute.
func (r *ProfileRepository) SetKids(id string, kids bool) error {
	res, err := database.DB.Exec(`UPDATE profiles SET kids = ? WHERE id = ?`, kids, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProfileNotFound
	}
	return nil
}

// List returns every profile with its devices and storage footprint —
// the admin's management view. No event content crosses this boundary.
func (r *ProfileRepository) List() ([]ProfileSummary, error) {
	rows, err := database.DB.Query(`
		SELECT p.id, p.name, p.created_at, p.kids, p.look, p.look_updated_at,
		       COUNT(e.id), COALESCE(SUM(e.seconds), 0)
		FROM profiles p
		LEFT JOIN stats_events e ON e.profile_id = p.id
		GROUP BY p.id, p.name, p.created_at, p.kids, p.look, p.look_updated_at
		ORDER BY p.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ProfileSummary{}
	for rows.Next() {
		var s ProfileSummary
		var look string
		if err := rows.Scan(&s.ID, &s.Name, &s.CreatedAt, &s.Kids,
			&look, &s.LookUpdatedAt, &s.EventCount, &s.TotalSeconds); err != nil {
			return nil, err
		}
		s.Look = decodeLook(look)
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range out {
		devices, err := r.ListTokens(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Devices = devices
	}
	return out, nil
}

// Delete removes a profile and everything it owns.
//
// Events and device tokens are deleted EXPLICITLY rather than left to
// ON DELETE CASCADE. The cascades are declared and foreign keys are now
// enabled, but this server spent months with them silently off, and
// "someone's data was supposed to be deleted" is the wrong thing to be
// wrong about.
func (r *ProfileRepository) Delete(id string) error {
	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, q := range []string{
		`DELETE FROM stats_events WHERE profile_id = ?`,
		// Alexandria Accounts: the device keys first, while the tokens
		// they belong to can still be found.
		`DELETE FROM account_device_keys WHERE token_id IN
			(SELECT id FROM profile_tokens WHERE profile_id = ?)`,
		`DELETE FROM account_services WHERE profile_id = ?`,
		`DELETE FROM account_invites WHERE profile_id = ?`,
		`DELETE FROM account_logins WHERE profile_id = ?`,
		// Manage server sign-ins made with their account's admin access.
		// Never the admin login's own: those have no profile_id.
		`DELETE FROM admin_device_tokens WHERE profile_id = ? AND profile_id != ''`,
		`DELETE FROM profile_tokens WHERE profile_id = ?`,
		`DELETE FROM link_requests WHERE profile_id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}

	res, err := tx.Exec(`DELETE FROM profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrProfileNotFound
	}
	return tx.Commit()
}
