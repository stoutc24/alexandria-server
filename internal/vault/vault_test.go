package vault

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, KeySize)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestParseKeyAcceptsEveryWayOpenSSLWritesOne(t *testing.T) {
	k := testKey(t)
	for name, s := range map[string]string{
		"base64":        base64.StdEncoding.EncodeToString(k),
		"base64 no pad": base64.RawStdEncoding.EncodeToString(k),
		"base64url":     base64.URLEncoding.EncodeToString(k),
		"hex":           hex.EncodeToString(k),
		"padded spaces": "  " + base64.StdEncoding.EncodeToString(k) + "\n",
	} {
		got, err := ParseKey(s)
		if err != nil || !bytes.Equal(got, k) {
			t.Errorf("%s: got %x, %v", name, got, err)
		}
	}
}

func TestParseKeyRefusesWrongSizesAndJunk(t *testing.T) {
	for _, s := range []string{
		"", "change-me", "short",
		base64.StdEncoding.EncodeToString(make([]byte, 16)),
		base64.StdEncoding.EncodeToString(make([]byte, 33)),
		strings.Repeat("zz", 32),
	} {
		if _, err := ParseKey(s); err == nil {
			t.Errorf("%q: accepted", s)
		}
	}
}

func TestSealOpenRoundTripAndTheRowBinding(t *testing.T) {
	v, err := New(testKey(t))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := v.Seal([]byte(`{"password":"hunter2"}`), "profile-a|service-1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, "v1:") || strings.Contains(sealed, "hunter2") {
		t.Fatalf("stored form leaks or is unversioned: %s", sealed)
	}
	plain, err := v.Open(sealed, "profile-a|service-1")
	if err != nil || string(plain) != `{"password":"hunter2"}` {
		t.Fatalf("round trip: %q %v", plain, err)
	}
	// Copied onto another person's row, it does not open.
	if _, err := v.Open(sealed, "profile-b|service-1"); err != ErrOpenFailed {
		t.Fatalf("opened under another row: %v", err)
	}
	// Under another key (a backup restored with a new key), it does not open.
	other, _ := New(testKey(t))
	if _, err := other.Open(sealed, "profile-a|service-1"); err != ErrOpenFailed {
		t.Fatalf("opened under another key: %v", err)
	}
	// Tampered, it does not open.
	raw, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(sealed, "v1:"))
	raw[len(raw)-1] ^= 1
	if _, err := v.Open("v1:"+base64.StdEncoding.EncodeToString(raw), "profile-a|service-1"); err != ErrOpenFailed {
		t.Fatalf("tampered value opened: %v", err)
	}
	if _, err := v.Open("plain text", "x"); err != ErrNotSealed {
		t.Fatalf("unsealed value: %v", err)
	}
}

func TestADeviceBoxOpensOnlyForThatDevice(t *testing.T) {
	device, _ := ecdh.X25519().GenerateKey(rand.Reader)
	stranger, _ := ecdh.X25519().GenerateKey(rand.Reader)

	pub, err := ParseDeviceKey(base64.StdEncoding.EncodeToString(device.PublicKey().Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	box, err := SealToDevice(pub, []byte("the services"))
	if err != nil {
		t.Fatal(err)
	}
	if box.Alg != BoxAlg {
		t.Fatalf("alg %q", box.Alg)
	}
	got, err := OpenBox(device, box)
	if err != nil || string(got) != "the services" {
		t.Fatalf("device could not open its box: %q %v", got, err)
	}
	if _, err := OpenBox(stranger, box); err == nil {
		t.Fatal("another key opened the box")
	}
	tampered := *box
	ct, _ := base64.StdEncoding.DecodeString(box.Ciphertext)
	ct[0] ^= 1
	tampered.Ciphertext = base64.StdEncoding.EncodeToString(ct)
	if _, err := OpenBox(device, &tampered); err == nil {
		t.Fatal("tampered box opened")
	}
}

func TestParseDeviceKeyRefusesWrongSizes(t *testing.T) {
	for _, s := range []string{"", "nope", base64.StdEncoding.EncodeToString(make([]byte, 31))} {
		if _, err := ParseDeviceKey(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

// The vector the app's Dart implementation must reproduce byte for byte
// (test/fixtures/accounts_devicebox_vector.json in the app repo is a copy).
// Regenerate with UPDATE_VECTOR=1 only if the scheme itself changes — and
// then bump BoxAlg.
func TestDeviceBoxVector(t *testing.T) {
	ephPriv := bytes.Repeat([]byte{0x11}, 32)
	devPriv := bytes.Repeat([]byte{0x22}, 32)
	nonce := bytes.Repeat([]byte{0x33}, 12)
	plaintext := []byte(`{"services":[{"type":"abs","baseUrl":"http://abs.local:13378","username":"sam","password":"correct horse"}]}`)

	eph, err := ecdh.X25519().NewPrivateKey(ephPriv)
	if err != nil {
		t.Fatal(err)
	}
	dev, err := ecdh.X25519().NewPrivateKey(devPriv)
	if err != nil {
		t.Fatal(err)
	}
	box, err := sealWith(eph, nonce, dev.PublicKey(), plaintext)
	if err != nil {
		t.Fatal(err)
	}
	got, err := OpenBox(dev, box)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("vector does not round trip: %v", err)
	}

	vector := map[string]string{
		"alg":                 BoxAlg,
		"devicePrivateKey":    base64.StdEncoding.EncodeToString(devPriv),
		"devicePublicKey":     base64.StdEncoding.EncodeToString(dev.PublicKey().Bytes()),
		"ephemeralPrivateKey": base64.StdEncoding.EncodeToString(ephPriv),
		"epk":                 box.EphemeralKey,
		"nonce":               box.Nonce,
		"ct":                  box.Ciphertext,
		"plaintext":           string(plaintext),
	}
	path := filepath.Join("testdata", "devicebox_vector.json")
	if os.Getenv("UPDATE_VECTOR") == "1" {
		b, _ := json.MarshalIndent(vector, "", "  ")
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing %s (run once with UPDATE_VECTOR=1): %v", path, err)
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	for k, v := range vector {
		if want[k] != v {
			t.Errorf("%s: got %q, pinned %q", k, v, want[k])
		}
	}
}
