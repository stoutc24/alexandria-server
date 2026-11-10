package adminauth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters.
//
// These are OWASP's minimum recommended configuration (19 MiB, t=2,
// p=1), chosen deliberately over something heavier: the compose file
// caps this container at 256 MB, and a KDF that allocates 64 MB per call
// is one concurrent login away from being OOM-killed on the box it is
// supposed to protect. Login is rare and rate limited, so the cost of a
// slightly cheaper hash is small; the cost of an admin page that dies
// under load is not.
const (
	argonTime    = 2
	argonMemory  = 19 * 1024 // KiB
	argonThreads = 1
	argonKeyLen  = 32
	argonSaltLen = 16
)

// argonGate lets two password hashes run at a time; the rest wait their
// turn (security review, 2026-11-10). Each hash holds 19 MiB while it
// runs, and nothing bounded how many ran at once: twenty sign-ins sent
// together took the process past 400 MB, on a container capped at 256.
// The limits in front of the sign-ins count requests per minute, not
// requests in flight. Waiting is short (a hash takes a fraction of a
// second) and bounded by those same limits.
const argonConcurrency = 2

var argonGate = make(chan struct{}, argonConcurrency)

// argonKey is argon2.IDKey behind argonGate.
func argonKey(password, salt []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	argonGate <- struct{}{}
	defer func() { <-argonGate }()
	if TestHookArgonRunning != nil {
		TestHookArgonRunning()
	}
	return argon2.IDKey(password, salt, time, memory, threads, keyLen)
}

// TestHookArgonRunning, when set, is called by each hash once it holds its
// place in argonGate. Tests only.
var TestHookArgonRunning func()

// MinPasswordLength is deliberately the only password rule.
//
// Composition rules (a digit, a symbol, a capital) push people toward
// Passw0rd! and buy nothing. Length is the property that actually
// matters, and this is the one thing standing in front of a backup of
// the whole database, so twelve is the floor.
const MinPasswordLength = 12

var ErrPasswordTooShort = fmt.Errorf(
	"password must be at least %d characters", MinPasswordLength)

// HashPassword returns an encoded argon2id hash, self-describing so a
// future parameter change can still verify old hashes.
func HashPassword(password string) (string, error) {
	if len([]rune(password)) < MinPasswordLength {
		return "", ErrPasswordTooShort
	}
	return hashPassword(password)
}

// HashPasswordAtLeast is HashPassword with another floor: a household
// member's Alexandria Accounts sign-in takes eight
// (repository.MinAccountPasswordLength) behind its rate limit and
// lockout, while the admin's own login keeps its twelve.
func HashPasswordAtLeast(password string, minLength int) (string, error) {
	if len([]rune(password)) < minLength {
		return "", fmt.Errorf("password must be at least %d characters", minLength)
	}
	return hashPassword(password)
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argonKey([]byte(password), salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether the password matches the encoded hash.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil ||
		version != argon2.Version {
		return false
	}

	var memory uint32
	var time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d",
		&memory, &time, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argonKey([]byte(password), salt,
		time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// --- opaque tokens -------------------------------------------------
//
// Session tokens and recovery codes are 32 bytes from crypto/rand, so
// they carry full entropy and need no stretching. Plain SHA-256 is the
// right store for them: the slow hashes above exist for low-entropy
// human passwords, and running argon2 on every request would make each
// authenticated page load cost 19 MB.

// NewToken returns a fresh opaque token, hex encoded.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// DeviceTokenPrefix marks an admin device token — a phone's admin
// sign-in — as "alxp_" marks a profile token, so RequireAdmin can tell
// it from every other bearer without a lookup, and a person can tell
// what they are looking at.
const DeviceTokenPrefix = "alxa_"

// NewDeviceToken returns a fresh admin device token: the prefix and 32
// bytes from crypto/rand, hex encoded. Store only HashToken of it.
func NewDeviceToken() (string, error) {
	t, err := NewToken()
	if err != nil {
		return "", err
	}
	return DeviceTokenPrefix + t, nil
}

// HashToken is the storage form of a session token or recovery code.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return hex.EncodeToString(sum[:])
}

// RecoveryCodeCount is how many single-use codes an enrolment mints.
const RecoveryCodeCount = 10

// NewRecoveryCodes mints single-use codes for the case this feature
// otherwise creates: a lost or wiped phone, with the authenticator on
// it, and an admin page that will not open without a code from it.
//
// Grouped into two blocks for transcription; the dash is cosmetic and
// stripped before hashing.
func NewRecoveryCodes() ([]string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789" // no 0 O 1 I L
	codes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		var b strings.Builder
		for j := 0; j < 10; j++ {
			if j == 5 {
				b.WriteByte('-')
			}
			c, err := pick(alphabet)
			if err != nil {
				return nil, err
			}
			b.WriteByte(c)
		}
		codes = append(codes, b.String())
	}
	return codes, nil
}

// pick chooses one character uniformly.
//
// Rejection sampling rather than v%len(alphabet): 256 is not a multiple
// of 31, so the modulo would quietly favour the first few letters. The
// bias is small enough not to matter here and cheap enough not to keep.
func pick(alphabet string) (byte, error) {
	n := len(alphabet)
	// Computed in int: a 256-character alphabet would make this 256,
	// which is 0 in a byte and would loop forever.
	limit := 256 - (256 % n)
	buf := make([]byte, 1)
	for {
		if _, err := rand.Read(buf); err != nil {
			return 0, err
		}
		if int(buf[0]) < limit {
			return alphabet[int(buf[0])%n], nil
		}
	}
}

// NormalizeRecoveryCode makes entry forgiving: case and dashes are
// presentation, not secret.
func NormalizeRecoveryCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(
		strings.TrimSpace(code), "-", ""))
}

var ErrNoCredentials = errors.New("no admin credentials enrolled")
