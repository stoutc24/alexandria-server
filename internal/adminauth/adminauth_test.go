package adminauth

import (
	"strings"
	"testing"
)

func TestAGeneratedCodeVerifies(t *testing.T) {
	secret, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := int64(1_700_000_000)
	code, err := codeAt(secret, now/totpPeriod)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := Verify(secret, code, now, 0); !ok {
		t.Fatal("a freshly generated code did not verify")
	}
}

// A phone clock a few seconds out must still work; a clock five minutes
// out must not.
func TestClockSkewIsToleratedButNotUnbounded(t *testing.T) {
	secret, _ := NewSecret()
	now := int64(1_700_000_000)

	prev, _ := codeAt(secret, now/totpPeriod-1)
	if _, ok := Verify(secret, prev, now, 0); !ok {
		t.Fatal("one step of skew must be accepted")
	}

	far, _ := codeAt(secret, now/totpPeriod-10)
	if _, ok := Verify(secret, far, now, 0); ok {
		t.Fatal("a code from five minutes ago was accepted")
	}
}

// The property TOTP does not give you for free. Without the last-used
// step, a code observed over a shoulder or in a proxy log stays good for
// the rest of its 30-second window.
func TestACodeCannotBeReplayed(t *testing.T) {
	secret, _ := NewSecret()
	now := int64(1_700_000_000)
	code, _ := codeAt(secret, now/totpPeriod)

	step, ok := Verify(secret, code, now, 0)
	if !ok {
		t.Fatal("first use should succeed")
	}
	if _, ok := Verify(secret, code, now, step); ok {
		t.Fatal("the same code verified twice — replay is possible")
	}
}

func TestAWrongCodeIsRejected(t *testing.T) {
	secret, _ := NewSecret()
	if _, ok := Verify(secret, "000000", 1_700_000_000, 0); ok {
		t.Fatal("000000 verified")
	}
	if _, ok := Verify(secret, "12345", 1_700_000_000, 0); ok {
		t.Fatal("a five-digit code verified")
	}
}

// Every authenticator app reads these parameters off the URI; getting
// one wrong produces codes that never match and no useful error.
func TestProvisioningURICarriesWhatAuthenticatorsNeed(t *testing.T) {
	uri := ProvisioningURI("JBSWY3DPEHPK3PXP", "admin", "Alexandria")
	for _, want := range []string{
		"otpauth://totp/", "secret=JBSWY3DPEHPK3PXP",
		"issuer=Alexandria", "algorithm=SHA1", "digits=6", "period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Fatalf("provisioning URI missing %q: %s", want, uri)
		}
	}
}

func TestPasswordRoundTrips(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "correct horse battery staple") {
		t.Fatal("the right password did not verify")
	}
	if VerifyPassword(hash, "correct horse battery stapl") {
		t.Fatal("a wrong password verified")
	}
}

// Two hashes of the same password must differ, or the store leaks which
// accounts share one.
func TestPasswordHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("correct horse battery staple")
	b, _ := HashPassword("correct horse battery staple")
	if a == b {
		t.Fatal("two hashes of one password were identical — unsalted")
	}
}

func TestShortPasswordsAreRefused(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("a five-character password was accepted")
	}
}

func TestGarbageHashDoesNotPanicOrPass(t *testing.T) {
	for _, bad := range []string{
		"", "notahash", "$argon2id$v=19$bad$bad$bad",
		"$bcrypt$v=19$m=19456,t=2,p=1$c2FsdA$aGFzaA",
	} {
		if VerifyPassword(bad, "anything") {
			t.Fatalf("garbage hash %q verified", bad)
		}
	}
}

func TestRecoveryCodesAreDistinctAndNormalize(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("got %d codes, want %d", len(codes), RecoveryCodeCount)
	}
	seen := map[string]bool{}
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true
		if strings.ContainsAny(c, "01OIL") {
			t.Fatalf("code %q uses a character people misread", c)
		}
	}
	// Someone reading these off paper should not have to match case or
	// remember the dash.
	if NormalizeRecoveryCode("abcde-fghij") != NormalizeRecoveryCode("ABCDEFGHIJ") {
		t.Fatal("recovery code entry is not forgiving of case or dashes")
	}
}

func TestTokenHashIsStableAndOpaque(t *testing.T) {
	tok, _ := NewToken()
	if HashToken(tok) != HashToken(tok) {
		t.Fatal("token hash is not stable")
	}
	if strings.Contains(HashToken(tok), tok) {
		t.Fatal("the hash contains the token")
	}
}
