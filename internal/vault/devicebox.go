package vault

// A box only one device can open.
//
// A person's service logins travel from this server to their device. The
// companion often sits on plain http inside the house, so the logins are
// sealed to a key the DEVICE made when it signed in: an X25519 key pair
// whose private half never leaves it. Anyone watching the network sees
// ciphertext, and a stolen device token alone opens nothing either — the
// box is sealed to the key stored at sign-in, not to one the caller names.
//
// The scheme, so the app can mirror it exactly:
//
//	eph          = a fresh X25519 key pair per box
//	shared       = X25519(eph.private, device.public)
//	key          = HKDF-SHA256(secret = shared,
//	                           salt   = eph.public ‖ device.public,
//	                           info   = "alexandria-accounts-v1", 32 bytes)
//	ciphertext   = AES-256-GCM(key, 12-byte random nonce, plaintext,
//	                           associated data = "alexandria-accounts-v1"),
//	               with the 16-byte tag appended
//
// Every field travels as standard, padded base64.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
)

// BoxAlg names the scheme above in every box, so a later one can be told
// apart.
const BoxAlg = "x25519-hkdf-sha256-aes256gcm-v1"

const boxInfo = "alexandria-accounts-v1"

var ErrBadDeviceKey = errors.New("the device key must be a 32-byte X25519 public key, base64 encoded")

// SealedBox is a payload for one device.
type SealedBox struct {
	Alg          string `json:"alg"`
	EphemeralKey string `json:"epk"`
	Nonce        string `json:"nonce"`
	Ciphertext   string `json:"ct"`
}

// ParseDeviceKey reads a device's public key as the app sends it.
func ParseDeviceKey(encoded string) (*ecdh.PublicKey, error) {
	raw, err := decodeAnyBase64(encoded)
	if err != nil || len(raw) != 32 {
		return nil, ErrBadDeviceKey
	}
	pub, err := ecdh.X25519().NewPublicKey(raw)
	if err != nil {
		return nil, ErrBadDeviceKey
	}
	return pub, nil
}

// EncodeDeviceKey is the stored form of a device key: standard base64.
func EncodeDeviceKey(pub *ecdh.PublicKey) string {
	return base64.StdEncoding.EncodeToString(pub.Bytes())
}

// SealToDevice seals plaintext so only the holder of device's private key
// can read it.
func SealToDevice(device *ecdh.PublicKey, plaintext []byte) (*SealedBox, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return sealWith(eph, nonce, device, plaintext)
}

// sealWith is SealToDevice with the randomness passed in, so a test can
// pin a vector the app's implementation must reproduce byte for byte.
func sealWith(eph *ecdh.PrivateKey, nonce []byte, device *ecdh.PublicKey, plaintext []byte) (*SealedBox, error) {
	gcm, err := boxCipher(eph, device, eph.PublicKey().Bytes(), device.Bytes())
	if err != nil {
		return nil, err
	}
	if len(nonce) != gcm.NonceSize() {
		return nil, errors.New("nonce must be 12 bytes")
	}
	ct := gcm.Seal(nil, nonce, plaintext, []byte(boxInfo))
	return &SealedBox{
		Alg:          BoxAlg,
		EphemeralKey: base64.StdEncoding.EncodeToString(eph.PublicKey().Bytes()),
		Nonce:        base64.StdEncoding.EncodeToString(nonce),
		Ciphertext:   base64.StdEncoding.EncodeToString(ct),
	}, nil
}

// OpenBox is the device's half, here so the scheme is tested end to end in
// Go before the app ever runs it.
func OpenBox(device *ecdh.PrivateKey, box *SealedBox) ([]byte, error) {
	if box == nil || box.Alg != BoxAlg {
		return nil, ErrOpenFailed
	}
	ephRaw, err := decodeAnyBase64(box.EphemeralKey)
	if err != nil {
		return nil, ErrOpenFailed
	}
	eph, err := ecdh.X25519().NewPublicKey(ephRaw)
	if err != nil {
		return nil, ErrOpenFailed
	}
	gcm, err := boxCipher(device, eph, eph.Bytes(), device.PublicKey().Bytes())
	if err != nil {
		return nil, ErrOpenFailed
	}
	nonce, err := decodeAnyBase64(box.Nonce)
	if err != nil || len(nonce) != gcm.NonceSize() {
		return nil, ErrOpenFailed
	}
	ct, err := decodeAnyBase64(box.Ciphertext)
	if err != nil {
		return nil, ErrOpenFailed
	}
	plain, err := gcm.Open(nil, nonce, ct, []byte(boxInfo))
	if err != nil {
		return nil, ErrOpenFailed
	}
	return plain, nil
}

// boxCipher derives the box key from one side's private key and the other
// side's public key. The salt is eph.public ‖ device.public whichever side
// is computing it, so both halves are passed in that order.
func boxCipher(own *ecdh.PrivateKey, other *ecdh.PublicKey, ephPub, devicePub []byte) (cipher.AEAD, error) {
	shared, err := own.ECDH(other)
	if err != nil {
		return nil, err
	}
	salt := append(append([]byte{}, ephPub...), devicePub...)
	key, err := hkdf.Key(sha256.New, shared, salt, boxInfo, 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func decodeAnyBase64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("not base64")
}
