package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

var (
	ErrLinkNotFound = errors.New("link request not found")
	ErrLinkExpired  = errors.New("link request expired")
	ErrLinkUsed     = errors.New("link request already used")
)

// LinkTTL is how long a code stays valid. Short on purpose: the whole
// flow is someone reading six characters aloud to the person sitting
// next to them.
const LinkTTL = 10 * time.Minute

// codeAlphabet omits 0/O/1/I/L — the characters people misread when a
// code is spoken or squinted at across a room.
const codeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

const codeLength = 6

// LinkRequest is a device asking to be let in.
//
// The short code is NOT a credential. Approving it needs the admin
// token, and collecting the result needs the claim secret, which only
// the requesting app ever held. So the code can be shouted across a
// room: on its own it does nothing.
type LinkRequest struct {
	ID          string     `json:"id"`
	Code        string     `json:"code"`
	DeviceLabel string     `json:"deviceLabel,omitempty"`
	ProfileID   string     `json:"profileId,omitempty"`
	ProfileName string     `json:"profileName,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   time.Time  `json:"expiresAt"`
	ApprovedAt  *time.Time `json:"approvedAt,omitempty"`
}

type LinkRepository struct{}

func NewLinkRepository() *LinkRepository {
	return &LinkRepository{}
}

func randomCode() (string, error) {
	max := big.NewInt(int64(len(codeAlphabet)))
	var b strings.Builder
	for i := 0; i < codeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	return b.String(), nil
}

func randomClaimSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// CreateRequest starts a link. It returns the human-readable code and
// the claim secret, which the app keeps private and uses to poll.
// publicKey is the device's key (stored form), or "" for none: the claim
// gives it to the device it mints.
func (r *LinkRepository) CreateRequest(
	deviceLabel, publicKey string,
) (code, claimSecret string, expiresAt time.Time, err error) {
	claimSecret, err = randomClaimSecret()
	if err != nil {
		return "", "", time.Time{}, err
	}

	now := time.Now().UTC()
	expiresAt = now.Add(LinkTTL)

	// Retry on the rare collision with another pending code rather than
	// handing back a duplicate.
	for attempt := 0; attempt < 8; attempt++ {
		code, err = randomCode()
		if err != nil {
			return "", "", time.Time{}, err
		}

		var key any
		if publicKey != "" {
			key = publicKey
		}
		_, err = database.DB.Exec(`
			INSERT INTO link_requests
				(id, code, claim_hash, device_label, created_at, expires_at, public_key)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, uuid.New().String(), code, HashToken(claimSecret),
			strings.TrimSpace(deviceLabel), now, expiresAt, key)

		if err == nil {
			return code, claimSecret, expiresAt, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", "", time.Time{}, err
		}
	}
	return "", "", time.Time{}, errors.New("could not allocate a link code")
}

// ListPending shows the admin what is waiting, newest first. Expired and
// already-approved requests are left out — the admin should see a short
// list of live requests, not a log.
func (r *LinkRepository) ListPending() ([]LinkRequest, error) {
	rows, err := database.DB.Query(`
		SELECT id, code, COALESCE(device_label, ''), created_at, expires_at
		FROM link_requests
		WHERE profile_id IS NULL AND expires_at > ?
		ORDER BY created_at DESC
	`, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LinkRequest{}
	for rows.Next() {
		var l LinkRequest
		if err := rows.Scan(&l.ID, &l.Code, &l.DeviceLabel,
			&l.CreatedAt, &l.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CheckPending reports whether a code could be approved right now,
// without changing anything.
//
// It exists so the handler can validate BEFORE creating a profile:
// otherwise a mistyped or stale code leaves an orphan person behind,
// every time.
func (r *LinkRepository) CheckPending(code string) error {
	code = strings.ToUpper(strings.TrimSpace(code))

	var expires time.Time
	var existing sql.NullString
	err := database.DB.QueryRow(`
		SELECT expires_at, profile_id FROM link_requests WHERE code = ?
	`, code).Scan(&expires, &existing)
	if err == sql.ErrNoRows {
		return ErrLinkNotFound
	}
	if err != nil {
		return err
	}
	if existing.Valid && existing.String != "" {
		return ErrLinkUsed
	}
	if time.Now().UTC().After(expires) {
		return ErrLinkExpired
	}
	return nil
}

// Approve binds a pending code to a profile. Only the admin can reach
// this, which is what makes the code safe to say out loud.
func (r *LinkRepository) Approve(code, profileID string) error {
	code = strings.ToUpper(strings.TrimSpace(code))

	var id string
	var expires time.Time
	var existing sql.NullString
	err := database.DB.QueryRow(`
		SELECT id, expires_at, profile_id FROM link_requests WHERE code = ?
	`, code).Scan(&id, &expires, &existing)
	if err == sql.ErrNoRows {
		return ErrLinkNotFound
	}
	if err != nil {
		return err
	}
	if existing.Valid && existing.String != "" {
		return ErrLinkUsed
	}
	if time.Now().UTC().After(expires) {
		return ErrLinkExpired
	}

	// The guard in the UPDATE, not just the SELECT above: two concurrent
	// approvals both pass the read, and without it the second silently
	// re-binds the code. Zero rows affected means somebody else won.
	now := time.Now().UTC()
	res, err := database.DB.Exec(`
		UPDATE link_requests SET profile_id = ?, approved_at = ?
		WHERE id = ? AND profile_id IS NULL
	`, profileID, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLinkUsed
	}
	return nil
}

// Claim is the app collecting its token.
//
// Returns the bound profile once approved, and the key the request was
// made with ("" for none). Single use: a claimed request is consumed, so
// a replayed claim secret gets nothing.
func (r *LinkRepository) Claim(claimSecret string) (profileID, deviceLabel, publicKey string, err error) {
	var id string
	var expires time.Time
	var profile sql.NullString
	var claimed sql.NullTime
	var label sql.NullString
	var key sql.NullString

	err = database.DB.QueryRow(`
		SELECT id, expires_at, profile_id, claimed_at, device_label, public_key
		FROM link_requests WHERE claim_hash = ?
	`, HashToken(claimSecret)).Scan(&id, &expires, &profile, &claimed, &label, &key)

	if err == sql.ErrNoRows {
		return "", "", "", ErrLinkNotFound
	}
	if err != nil {
		return "", "", "", err
	}
	if claimed.Valid {
		return "", "", "", ErrLinkUsed
	}
	if time.Now().UTC().After(expires) {
		return "", "", "", ErrLinkExpired
	}
	if !profile.Valid || profile.String == "" {
		// Approved yet? Not an error — the app is still polling.
		return "", "", "", nil
	}

	// Single use has to hold under CONCURRENCY, which a SELECT-then-
	// UPDATE does not: two racing claims with the same secret both pass
	// the read and both mint a token. The claimed_at guard makes the
	// second UPDATE touch zero rows, and zero rows means already used.
	res, err := database.DB.Exec(
		`UPDATE link_requests SET claimed_at = ? WHERE id = ? AND claimed_at IS NULL`,
		time.Now().UTC(), id,
	)
	if err != nil {
		return "", "", "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", "", "", ErrLinkUsed
	}
	return profile.String, label.String, key.String, nil
}

// PurgeExpired clears out stale requests so the table does not grow
// without bound. Cheap enough to call on a timer.
func (r *LinkRepository) PurgeExpired() (int64, error) {
	res, err := database.DB.Exec(
		`DELETE FROM link_requests WHERE expires_at < ?`,
		time.Now().UTC().Add(-time.Hour),
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
