// Package adminauth adds a real login to the admin surface: a password
// you know, and a code from an authenticator app you hold.
//
// Why this exists. The admin token is a single static string that lives
// in a .env file and, on any companion reachable from the internet, is
// the only thing between a stranger and GET /api/v1/admin/backup — which
// returns the entire database. One factor, never rotated, typed into a
// page anyone can load. That is the hole this closes.
//
// What it does NOT change: the server still grants no entitlements, and
// logging in as admin still does not open a path into anybody's stats.
// RequireProfile keeps refusing the admin, session or not. See
// docs/COMPANION_DESIGN.md.
package adminauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
)

// RFC 6238 defaults, which are what Google Authenticator, Aegis, 1Password
// and every other TOTP app assume when a QR omits them: SHA-1, 6 digits,
// a 30-second step.
//
// SHA-1 here is not the SHA-1 you are thinking of. TOTP uses it inside
// HMAC, where the collision attacks that killed SHA-1 for signatures do
// not apply, and every authenticator app in circulation speaks it.
// Choosing SHA-256 would be marginally stronger in theory and would
// silently fail to enrol on real phones.
const (
	totpDigits = 6
	totpPeriod = 30
	// One step of tolerance either side, so a phone clock that is a few
	// seconds out still works. Wider windows trade real security for a
	// convenience nobody asked for.
	totpSkewSteps = 1
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret returns a fresh 160-bit TOTP secret, base32 encoded.
//
// 160 bits is the RFC 4226 recommendation and the size every
// authenticator handles without complaint.
func NewSecret() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return b32.EncodeToString(buf), nil
}

// ProvisioningURI builds the otpauth:// string an authenticator scans.
//
// The issuer appears twice on purpose: once in the label prefix for apps
// that only read the label, once as a parameter for apps that read it
// properly. That duplication is what the Key URI spec asks for.
func ProvisioningURI(secret, account, issuer string) string {
	label := url.PathEscape(issuer + ":" + account)
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", fmt.Sprint(totpDigits))
	q.Set("period", fmt.Sprint(totpPeriod))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// codeAt computes the TOTP code for one time step.
func codeAt(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return "", fmt.Errorf("bad totp secret: %w", err)
	}

	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 §5.3.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// CodeAt is the code for one time step. Exported for tests, which need
// a valid code without an authenticator app; nothing in the server
// itself mints codes.
func CodeAt(secret string, step int64) (string, error) {
	return codeAt(secret, step)
}

// StepCeiling is the newest time step Verify can accept at now. A stored
// last-used step beyond it cannot have been spent honestly (see the
// reset in Verify), so a claim of a genuine step may overwrite it.
func StepCeiling(now int64) int64 {
	return now/totpPeriod + totpSkewSteps
}

// Verify checks a submitted code and returns the time step it matched.
//
// The caller MUST persist that step and refuse anything less than or
// equal to it next time. Without that, a code stays valid for its whole
// 30-second window and anyone who observes it — over someone's shoulder,
// in a screen share, in a proxy log — can replay it. TOTP does not
// prevent replay on its own; remembering the last step is what does.
func Verify(secret, code string, now int64, lastUsedStep int64) (int64, bool) {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return 0, false
	}

	current := now / totpPeriod

	// A step in the FUTURE cannot have been spent, so a stored value
	// beyond the acceptance window is corrupt, not history — a clock that
	// ran ahead when a code was accepted and was later corrected, or a bad
	// write. Honouring it would refuse EVERY genuine code from then on,
	// with the password still passing: an admin locked out of their own
	// server, permanently, with nothing changed on their side. Replay
	// protection for the present and the past is untouched.
	if lastUsedStep > current+totpSkewSteps {
		lastUsedStep = 0
	}

	for delta := int64(-totpSkewSteps); delta <= totpSkewSteps; delta++ {
		step := current + delta
		if step <= lastUsedStep {
			// Already spent. Skip rather than accept.
			continue
		}
		want, err := codeAt(secret, step)
		if err != nil {
			return 0, false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}
