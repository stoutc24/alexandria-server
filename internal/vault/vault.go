// Package vault keeps the service logins that Alexandria Accounts hands to
// a person's devices (owner, 2026-10-12: "users would sign into their apps
// with their Alexandria account which then loads all the services they
// have"; decision 1A: a server key kept OUTSIDE the database and its
// backups).
//
// History worth knowing. A 2025 version of this server vaulted per-user
// media-server credentials and was removed: its key defaulted to the login
// secret on the same box, so it protected little. This one
//
//   - takes its key only from ALEXANDRIA_SECRETS_KEY (or the file named by
//     ALEXANDRIA_SECRETS_KEY_FILE), never from the database;
//   - refuses a key that is short, the shipped placeholder, or the admin
//     token (see config.secretsKey);
//   - binds each stored secret to its row, so a ciphertext copied onto
//     another person's row does not open there.
//
// So neither the database file nor a backup of it can open what it holds.
// The honest limit, as decided: whoever controls the RUNNING server can read
// these logins. That is the household trust the listening stats already
// rely on, and it is what lets a person's sign-in be fully automatic.
package vault

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the server key's length: AES-256.
const KeySize = 32

// sealedPrefix versions the stored form, so a later scheme can sit beside
// this one without a migration guessing which rows are which.
const sealedPrefix = "v1:"

var (
	ErrKeySize    = fmt.Errorf("the secrets key must be %d random bytes, base64 or hex encoded", KeySize)
	ErrNotSealed  = errors.New("not a sealed value")
	ErrOpenFailed = errors.New("could not open the sealed value (wrong key, or it belongs to another row)")
)

// Vault seals and opens values with the server key.
type Vault struct {
	aead cipher.AEAD
}

// ParseKey reads a key written as base64 (standard or URL alphabet, padded
// or not) or hex, and insists on exactly KeySize bytes. `openssl rand
// -base64 32` and `openssl rand -hex 32` both produce one.
func ParseKey(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, ErrKeySize
	}
	if len(raw) == KeySize*2 {
		if b, err := hex.DecodeString(raw); err == nil {
			return b, nil
		}
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(raw); err == nil {
			if len(b) != KeySize {
				return nil, ErrKeySize
			}
			return b, nil
		}
	}
	return nil, ErrKeySize
}

// New makes a vault from a KeySize key.
func New(key []byte) (*Vault, error) {
	if len(key) != KeySize {
		return nil, ErrKeySize
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// Seal encrypts plaintext for storage. associated names what the value
// belongs to (a row's id and its owner): the same bytes are needed to open
// it, so a value moved to another row will not.
func (v *Vault) Seal(plaintext []byte, associated string) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	out := v.aead.Seal(nonce, nonce, plaintext, []byte(associated))
	return sealedPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Open reverses Seal.
func (v *Vault) Open(sealed, associated string) ([]byte, error) {
	if !strings.HasPrefix(sealed, sealedPrefix) {
		return nil, ErrNotSealed
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, sealedPrefix))
	if err != nil {
		return nil, ErrNotSealed
	}
	n := v.aead.NonceSize()
	if len(raw) < n+v.aead.Overhead() {
		return nil, ErrNotSealed
	}
	plain, err := v.aead.Open(nil, raw[:n], raw[n:], []byte(associated))
	if err != nil {
		return nil, ErrOpenFailed
	}
	return plain, nil
}
