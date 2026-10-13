package repository

import (
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

func newAccountsDB(t *testing.T, withVault bool) (*AccountRepository, *ProfileRepository) {
	t.Helper()
	profiles, _ := newProfileDB(t)
	var v *vault.Vault
	if withVault {
		key := make([]byte, vault.KeySize)
		if _, err := rand.Read(key); err != nil {
			t.Fatal(err)
		}
		var err error
		if v, err = vault.New(key); err != nil {
			t.Fatal(err)
		}
	}
	return NewAccountRepository(v), profiles
}

func str(s string) *string { return &s }

func TestInviteAcceptThenSignIn(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()

	token, code, inv, err := acc.CreateInvite(sam.ID, "invite", InviteTTL, now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, InviteTokenPrefix) || len(code) != 9 || code[4] != '-' {
		t.Fatalf("token %q code %q", token, code)
	}
	if inv.ProfileName != "Sam" || inv.Purpose != "invite" {
		t.Fatalf("invite %+v", inv)
	}
	// The code works however it is typed.
	for _, typed := range []string{code, strings.ToLower(code), strings.ReplaceAll(code, "-", " ")} {
		if got, err := acc.FindInvite(typed, now); err != nil || got.ID != inv.ID {
			t.Fatalf("code %q: %v", typed, err)
		}
	}
	if err := acc.AcceptInvite(inv, "sam", "hash-1", now); err != nil {
		t.Fatal(err)
	}
	// Spent: neither the token nor the code works again.
	if _, err := acc.FindInvite(token, now); err != ErrInviteInvalid {
		t.Fatalf("token after use: %v", err)
	}
	if err := acc.AcceptInvite(inv, "sam2", "hash-2", now); err != ErrInviteInvalid {
		t.Fatalf("second accept: %v", err)
	}
	login, err := acc.LoginByUsername("SAM")
	if err != nil || login.ProfileID != sam.ID || login.PasswordHash != "hash-1" || login.ProfileName != "Sam" {
		t.Fatalf("login %+v %v", login, err)
	}
}

func TestInvitesExpireAndANewOneReplacesTheOld(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	first, _, _, _ := acc.CreateInvite(sam.ID, "invite", InviteTTL, now)
	second, _, _, _ := acc.CreateInvite(sam.ID, "invite", InviteTTL, now)
	if _, err := acc.FindInvite(first, now); err != ErrInviteInvalid {
		t.Fatalf("the replaced invite still works: %v", err)
	}
	if _, err := acc.FindInvite(second, now.Add(InviteTTL+time.Second)); err != ErrInviteInvalid {
		t.Fatalf("an expired invite works: %v", err)
	}
	if p, _ := acc.PendingInvite(sam.ID, now); p == nil {
		t.Fatal("no pending invite shown")
	}
	if err := acc.CancelInvites(sam.ID); err != nil {
		t.Fatal(err)
	}
	if p, _ := acc.PendingInvite(sam.ID, now); p != nil {
		t.Fatal("cancelled invite still pending")
	}
}

func TestUsernamesAreUniqueRegardlessOfCase(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	a, _ := profiles.Create("A")
	b, _ := profiles.Create("B")
	now := time.Now()
	_, _, ia, _ := acc.CreateInvite(a.ID, "invite", InviteTTL, now)
	_, _, ib, _ := acc.CreateInvite(b.ID, "invite", InviteTTL, now)
	if err := acc.AcceptInvite(ia, "river", "h", now); err != nil {
		t.Fatal(err)
	}
	if err := acc.AcceptInvite(ib, "RIVER", "h", now); err != ErrUsernameTaken {
		t.Fatalf("duplicate username: %v", err)
	}
	if ok, _ := acc.UsernameAvailable("river", b.ID); ok {
		t.Fatal("taken name reported free")
	}
	if ok, _ := acc.UsernameAvailable("river", a.ID); !ok {
		t.Fatal("a person's own name reported taken")
	}
	for _, bad := range []string{"", "ab", "-lead", "has space", strings.Repeat("x", 33)} {
		if _, err := NormalizeUsername(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if got, _ := NormalizeUsername("  Sam.Smith_1 "); got != "sam.smith_1" {
		t.Fatalf("normalized %q", got)
	}
}

func TestTenWrongAnswersLockTheSignIn(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	_, _, inv, _ := acc.CreateInvite(sam.ID, "invite", InviteTTL, now)
	_ = acc.AcceptInvite(inv, "sam", "h", now)
	var locked int64
	for i := 0; i < LoginFailuresBeforeLock; i++ {
		locked, _ = acc.RecordFailure(sam.ID, now)
	}
	if locked != now.Add(LoginLockDuration).Unix() {
		t.Fatalf("locked until %d", locked)
	}
	l, _ := acc.LoginByProfile(sam.ID)
	if l.LockedUntil != locked {
		t.Fatalf("lock not stored: %+v", l)
	}
	_ = acc.RecordSuccess(sam.ID, now)
	l, _ = acc.LoginByProfile(sam.ID)
	if l.LockedUntil != 0 || l.FailedAttempts != 0 || l.LastLoginAt == 0 {
		t.Fatalf("success did not clear: %+v", l)
	}
}

func TestServicesAreSealedListedInOrderAndNeverLeakToTheAdmin(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()

	abs, err := acc.AddService(sam.ID, ServiceInput{
		Type: "abs", BaseURL: "http://abs.local:13378/", LoginKind: "password",
		Username: str("sam"), Password: str("correct horse"),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if abs.Name != "Audiobookshelf" || abs.BaseURL != "http://abs.local:13378" || !abs.HasSecret || abs.Username != "sam" {
		t.Fatalf("abs %+v", abs)
	}
	plex, _ := acc.AddService(sam.ID, ServiceInput{Type: "plex", BaseURL: "https://plex.example", LoginKind: "none"}, now)
	if plex.HasSecret || plex.Priority != 1 {
		t.Fatalf("plex %+v", plex)
	}

	// At rest, the password is nowhere in the row.
	var stored string
	_ = database.DB.QueryRow(`SELECT credentials FROM account_services WHERE id = ?`, abs.ID).Scan(&stored)
	if strings.Contains(stored, "correct horse") || !strings.HasPrefix(stored, "v1:") {
		t.Fatalf("stored credentials: %s", stored)
	}
	// The admin's list never carries credentials.
	list, _ := acc.ListServices(sam.ID, false)
	if len(list) != 2 || list[0].ID != abs.ID || list[0].Credentials != nil {
		t.Fatalf("admin list %+v", list)
	}
	// The device's list does.
	mine, _ := acc.ListServices(sam.ID, true)
	if mine[0].Credentials == nil || mine[0].Credentials.Password != "correct horse" {
		t.Fatalf("device list %+v", mine[0].Credentials)
	}

	rev1, _ := acc.ServicesRevision(sam.ID)
	if err := acc.ReorderServices(sam.ID, []string{plex.ID, abs.ID}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	list, _ = acc.ListServices(sam.ID, false)
	if list[0].ID != plex.ID {
		t.Fatalf("order %+v", list)
	}
	rev2, _ := acc.ServicesRevision(sam.ID)
	if rev1 == rev2 {
		t.Fatal("reordering did not change the revision")
	}
	if err := acc.ReorderServices(sam.ID, []string{plex.ID}, now); err != ErrServiceNotFound {
		t.Fatalf("partial reorder: %v", err)
	}

	// An edit that leaves the password out keeps it; the username changes.
	edited, err := acc.UpdateService(sam.ID, abs.ID, ServiceInput{Username: str("samuel")}, now.Add(2*time.Second))
	if err != nil || edited.Username != "samuel" || !edited.HasSecret {
		t.Fatalf("edit %+v %v", edited, err)
	}
	mine, _ = acc.ListServices(sam.ID, true)
	for _, s := range mine {
		if s.ID == abs.ID && s.Credentials.Password != "correct horse" {
			t.Fatalf("password lost on edit: %+v", s.Credentials)
		}
	}
	if err := acc.DeleteService(sam.ID, abs.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.DeleteService(sam.ID, abs.ID); err != ErrServiceNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func TestWithoutASecretsKeyNoLoginIsStoredButAddressesAre(t *testing.T) {
	acc, profiles := newAccountsDB(t, false)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	if _, err := acc.AddService(sam.ID, ServiceInput{
		Type: "abs", BaseURL: "http://abs.local", LoginKind: "password",
		Username: str("sam"), Password: str("pw"),
	}, now); err != ErrVaultOff {
		t.Fatalf("stored a login with no key: %v", err)
	}
	if _, err := acc.AddService(sam.ID, ServiceInput{Type: "plex", BaseURL: "http://plex.local", LoginKind: "none"}, now); err != nil {
		t.Fatalf("an address-only service: %v", err)
	}
}

func TestServiceValidation(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	for _, in := range []ServiceInput{
		{Type: "netflix", BaseURL: "http://x", LoginKind: "password"},
		{Type: "abs", BaseURL: "http://x", LoginKind: "magic"},
		{Type: "abs", BaseURL: "ftp://x", LoginKind: "password"},
		{Type: "abs", BaseURL: "http://user:pw@x", LoginKind: "password"},
		{Type: "abs", BaseURL: "not a url", LoginKind: "password"},
	} {
		if _, err := acc.AddService(sam.ID, in, now); err == nil {
			t.Errorf("%+v accepted", in)
		}
	}
}

func TestADeviceKeyIsSetOnceAndGoesWithItsDevice(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	_, _ = profiles.MintToken(sam.ID, "phone")
	devices, _ := profiles.ListTokens(sam.ID)
	tokenID := devices[0].ID

	if err := acc.SetDeviceKey(tokenID, "key-a", now); err != nil {
		t.Fatal(err)
	}
	if err := acc.SetDeviceKey(tokenID, "key-a", now); err != nil {
		t.Fatalf("same key again: %v", err)
	}
	if err := acc.SetDeviceKey(tokenID, "key-b", now); err != ErrDeviceKeyExists {
		t.Fatalf("swapped key: %v", err)
	}
	if err := profiles.RevokeToken(tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.DeviceKey(tokenID); err != ErrNoDeviceKey {
		t.Fatalf("key outlived its device: %v", err)
	}
}

func TestDeletingAPersonRemovesTheirAccount(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	_, _ = profiles.MintToken(sam.ID, "phone")
	devices, _ := profiles.ListTokens(sam.ID)
	_ = acc.SetDeviceKey(devices[0].ID, "key", now)
	_, _, inv, _ := acc.CreateInvite(sam.ID, "invite", InviteTTL, now)
	_ = acc.AcceptInvite(inv, "sam", "h", now)
	_, _, _, _ = acc.CreateInvite(sam.ID, "reset", InviteTTL, now)
	_, _ = acc.AddService(sam.ID, ServiceInput{Type: "abs", BaseURL: "http://abs", LoginKind: "password", Password: str("x")}, now)

	if err := profiles.Delete(sam.ID); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"account_logins", "account_invites", "account_services", "account_device_keys"} {
		var n int
		_ = database.DB.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n)
		if n != 0 {
			t.Errorf("%s still holds %d rows", table, n)
		}
	}
}

// An edit that does not touch the login must never erase one it cannot
// read: with no secrets key (the server restarted without it), and with a
// key that changed since the login was saved. Found by the admin page's
// browser tests: renaming a service with the key missing wiped its login.
func TestAnEditNeverErasesALoginItCannotRead(t *testing.T) {
	keyed, profiles := newAccountsDB(t, true)
	sam, _ := profiles.Create("Sam")
	now := time.Now()
	add := func() *AccountService {
		t.Helper()
		s, err := keyed.AddService(sam.ID, ServiceInput{
			Type: "jellyfin", BaseURL: "https://jf.example", LoginKind: "password",
			Username: str("sam"), Password: str("pw"),
		}, now)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	secret := func(acc *AccountRepository, id string) (*ServiceCredentials, bool) {
		t.Helper()
		list, err := acc.ListServices(sam.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.ID == id {
				return s.Credentials, s.HasSecret
			}
		}
		t.Fatalf("service %s gone", id)
		return nil, false
	}

	// No key: a rename keeps the login, still openable once the key is back.
	noKey := NewAccountRepository(nil)
	s := add()
	if _, err := noKey.UpdateService(sam.ID, s.ID, ServiceInput{Name: "Movies"}, now); err != nil {
		t.Fatalf("rename without a key: %v", err)
	}
	if c, has := secret(keyed, s.ID); !has || c == nil || c.Password != "pw" {
		t.Fatalf("rename without a key erased the login: %+v %v", c, has)
	}
	// No key: a new secret needs the key.
	if _, err := noKey.UpdateService(sam.ID, s.ID, ServiceInput{Password: str("new")}, now); err != ErrVaultOff {
		t.Fatalf("new secret without a key: %v", err)
	}
	// No key: clearing on purpose (the login kind changed) needs no key.
	if _, err := noKey.UpdateService(sam.ID, s.ID, ServiceInput{
		LoginKind: "none", Username: str(""), Password: str(""),
	}, now); err != nil {
		t.Fatalf("clear without a key: %v", err)
	}
	if _, has := secret(keyed, s.ID); has {
		t.Fatal("the cleared login is still stored")
	}

	// A key that changed: a rename keeps the unreadable login, still flagged.
	otherKey := make([]byte, vault.KeySize)
	_, _ = rand.Read(otherKey)
	v2, _ := vault.New(otherKey)
	changed := NewAccountRepository(v2)
	s = add()
	if _, err := changed.UpdateService(sam.ID, s.ID, ServiceInput{Name: "Films"}, now); err != nil {
		t.Fatalf("rename with a changed key: %v", err)
	}
	if c, has := secret(keyed, s.ID); !has || c == nil || c.Password != "pw" {
		t.Fatalf("rename with a changed key erased the login: %+v %v", c, has)
	}
	list, _ := changed.ListServices(sam.ID, false)
	for _, ls := range list {
		if ls.ID == s.ID && !ls.Unreadable {
			t.Fatal("the unreadable login lost its flag")
		}
	}
	// ... and new secrets replace it, sealed with the new key.
	if _, err := changed.UpdateService(sam.ID, s.ID, ServiceInput{Username: str("sam"), Password: str("fresh")}, now); err != nil {
		t.Fatal(err)
	}
	if c, _ := secret(changed, s.ID); c == nil || c.Password != "fresh" {
		t.Fatalf("replaced login: %+v", c)
	}
}
