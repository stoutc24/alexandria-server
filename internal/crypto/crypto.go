package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"

	"github.com/stoutsolutions/alexandria-server/internal/models"
)

var (
	ErrInvalidCiphertext = errors.New("invalid ciphertext")
	ErrDecryptionFailed  = errors.New("decryption failed")
)

// Encryptor handles encryption/decryption of sensitive data
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new Encryptor with the given secret
// The secret is hashed to ensure a consistent 32-byte key for AES-256
func NewEncryptor(secret string) *Encryptor {
	hash := sha256.Sum256([]byte(secret))
	return &Encryptor{key: hash[:]}
}

// Encrypt encrypts plaintext using AES-256-GCM
func (e *Encryptor) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts ciphertext that was encrypted with Encrypt
func (e *Encryptor) Decrypt(ciphertextB64 string) ([]byte, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextB64)
	if err != nil {
		return nil, ErrInvalidCiphertext
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(ciphertext) < gcm.NonceSize() {
		return nil, ErrInvalidCiphertext
	}

	nonce, ciphertext := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, ErrDecryptionFailed
	}

	return plaintext, nil
}

// EncryptCredentials encrypts MediaServerCredentials to a string
func (e *Encryptor) EncryptCredentials(creds *models.MediaServerCredentials) (string, error) {
	jsonBytes, err := json.Marshal(creds)
	if err != nil {
		return "", err
	}
	return e.Encrypt(jsonBytes)
}

// DecryptCredentials decrypts an encrypted string back to MediaServerCredentials
func (e *Encryptor) DecryptCredentials(encrypted string) (*models.MediaServerCredentials, error) {
	plaintext, err := e.Decrypt(encrypted)
	if err != nil {
		return nil, err
	}

	var creds models.MediaServerCredentials
	if err := json.Unmarshal(plaintext, &creds); err != nil {
		return nil, err
	}

	return &creds, nil
}
