package repository

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
)

func newProfileDB(t *testing.T) (*ProfileRepository, *StatsRepository) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "profiles_test.db")
	if err := database.Connect(path); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.RunMigrations(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return NewProfileRepository(), NewStatsRepository()
}

// mkProfile creates a person with one linked device.
func mkProfile(t *testing.T, r *ProfileRepository, name string) (*Profile, string) {
	t.Helper()
	p, err := r.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	token, err := r.MintToken(p.ID, name+"'s phone")
	if err != nil {
		t.Fatalf("mint for %s: %v", name, err)
	}
	return p, token
}

// The DSN bug that left every cascade inert for months.
func TestForeignKeysAreEnforced(t *testing.T) {
	newProfileDB(t)

	var on int
	if err := database.DB.QueryRow("PRAGMA foreign_keys").Scan(&on); err != nil {
		t.Fatalf("pragma: %v", err)
	}
	if on != 1 {
		t.Fatal("foreign keys are off; the DSN regressed to a spelling " +
			"modernc.org/sqlite ignores")
	}

	if _, err := database.DB.Exec(`
		INSERT INTO stats_events (id, profile_id, event_type, seconds, occurred_at)
		VALUES ('e1', 'no-such-profile', 'progress', 10, ?)
	`, time.Now().UTC()); err == nil {
		t.Fatal("an event was accepted for a profile that does not exist")
	}
}

func TestTokensAreStoredOnlyAsHashes(t *testing.T) {
	profiles, _ := newProfileDB(t)
	_, token := mkProfile(t, profiles, "Sarah")

	if !strings.HasPrefix(token, "alxp_") {
		t.Errorf("token %q lacks the profile prefix", token)
	}

	var stored string
	if err := database.DB.QueryRow(
		`SELECT token_hash FROM profile_tokens`,
	).Scan(&stored); err != nil {
		t.Fatalf("read: %v", err)
	}
	if stored == token || strings.Contains(stored, strings.TrimPrefix(token, "alxp_")) {
		t.Fatal("the raw token is recoverable from the database")
	}
	if stored != HashToken(token) {
		t.Fatal("stored value is not the token hash")
	}
}

func TestTokenResolvesToItsOwnProfileOnly(t *testing.T) {
	profiles, _ := newProfileDB(t)
	sarah, sarahToken := mkProfile(t, profiles, "Sarah")
	chris, chrisToken := mkProfile(t, profiles, "Chris")

	got, err := profiles.FindByToken(sarahToken)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if got.ProfileID != sarah.ID {
		t.Fatalf("sarah's token resolved to %q", got.ProfileName)
	}
	if got.ProfileID == chris.ID {
		t.Fatal("tokens collide across profiles")
	}
	if _, err := profiles.FindByToken(chrisToken); err != nil {
		t.Fatalf("chris's token should resolve: %v", err)
	}
	if _, err := profiles.FindByToken("alxp_nonsense"); err != ErrProfileNotFound {
		t.Fatalf("an invalid token resolved to something: %v", err)
	}
}

// The reason per-device tokens exist: linking the watch must not log
// out the phone.
func TestOneProfileCanHoldSeveralDevices(t *testing.T) {
	profiles, _ := newProfileDB(t)
	sarah, phone := mkProfile(t, profiles, "Sarah")

	watch, err := profiles.MintToken(sarah.ID, "Sarah's watch")
	if err != nil {
		t.Fatalf("mint watch: %v", err)
	}
	if watch == phone {
		t.Fatal("two devices got the same token")
	}

	for name, tok := range map[string]string{"phone": phone, "watch": watch} {
		c, err := profiles.FindByToken(tok)
		if err != nil {
			t.Fatalf("%s stopped working: %v", name, err)
		}
		if c.ProfileID != sarah.ID {
			t.Errorf("%s resolved to the wrong profile", name)
		}
	}

	devices, err := profiles.ListTokens(sarah.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("expected 2 devices, got %d", len(devices))
	}
	for _, d := range devices {
		if strings.Contains(d.Label, "alxp_") {
			t.Error("a device listing leaked a token")
		}
	}
}

// Losing a phone must not mean resetting everything the person owns.
func TestRevokingOneDeviceLeavesTheOthers(t *testing.T) {
	profiles, _ := newProfileDB(t)
	sarah, phone := mkProfile(t, profiles, "Sarah")
	watch, _ := profiles.MintToken(sarah.ID, "Sarah's watch")

	devices, _ := profiles.ListTokens(sarah.ID)
	var phoneID string
	for _, d := range devices {
		if strings.Contains(d.Label, "phone") {
			phoneID = d.ID
		}
	}
	if phoneID == "" {
		t.Fatal("could not find the phone")
	}

	if err := profiles.RevokeToken(phoneID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := profiles.FindByToken(phone); err != ErrProfileNotFound {
		t.Error("the revoked phone still works")
	}
	if _, err := profiles.FindByToken(watch); err != nil {
		t.Errorf("revoking the phone broke the watch: %v", err)
	}
}

func TestDuplicateNamesAreRejected(t *testing.T) {
	profiles, _ := newProfileDB(t)
	if _, err := profiles.Create("Sarah"); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := profiles.Create("Sarah"); err != ErrProfileExists {
		t.Fatalf("expected ErrProfileExists, got %v", err)
	}
}

func TestDeletingAProfileRemovesItsEventsAndDevices(t *testing.T) {
	profiles, stats := newProfileDB(t)
	sarah, sarahToken := mkProfile(t, profiles, "Sarah")
	chris, _ := mkProfile(t, profiles, "Chris")

	mustAppend(t, stats, sarah.ID, 3)
	mustAppend(t, stats, chris.ID, 2)

	if err := profiles.Delete(sarah.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	var remaining int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM stats_events WHERE profile_id = ?`, sarah.ID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d of Sarah's events survived deletion", remaining)
	}
	if _, err := profiles.FindByToken(sarahToken); err != ErrProfileNotFound {
		t.Error("a deleted profile's device token still works")
	}

	s, err := stats.Summarize(chris.ID)
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if s.TotalEvents != 2 {
		t.Errorf("Chris lost events when Sarah was deleted: %d", s.TotalEvents)
	}
}

// The admin's management view must carry no content — only counts.
func TestAdminListExposesCountsNotContent(t *testing.T) {
	profiles, stats := newProfileDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	if _, err := stats.AppendEvents(sarah.ID, []Event{{
		Title: "A Very Private Book", Author: "Secret Author",
		MediaType: "book", EventType: "finished", Seconds: 600,
		OccurredAt: time.Now().UTC(),
	}}); err != nil {
		t.Fatalf("append: %v", err)
	}

	list, err := profiles.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(list))
	}
	if list[0].EventCount != 1 || list[0].TotalSeconds != 600 {
		t.Errorf("counts wrong: %+v", list[0])
	}
	if len(list[0].Devices) != 1 {
		t.Errorf("expected 1 device, got %d", len(list[0].Devices))
	}

	rendered := renderSummary(list[0])
	for _, secret := range []string{"A Very Private Book", "Secret Author"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("admin summary leaked %q", secret)
		}
	}
}

func renderSummary(s ProfileSummary) string {
	parts := []string{s.ID, s.Name}
	for _, d := range s.Devices {
		parts = append(parts, d.ID, d.Label)
	}
	return strings.Join(parts, "|")
}

func mustAppend(t *testing.T, stats *StatsRepository, profileID string, n int) {
	t.Helper()
	events := make([]Event, 0, n)
	for i := 0; i < n; i++ {
		events = append(events, Event{
			Title: "Title", Author: "Author", MediaType: "music",
			EventType: "progress", Seconds: 60,
			OccurredAt: time.Now().UTC(),
		})
	}
	if _, err := stats.AppendEvents(profileID, events); err != nil {
		t.Fatalf("append: %v", err)
	}
}

// A device may change the name the party shows for it; the next caller
// lookup carries the new label under the same token id.
func TestRenameTokenChangesTheLabelACallerSees(t *testing.T) {
	r, _ := newProfileDB(t)
	_, token := mkProfile(t, r, "Ann")

	before, err := r.FindByToken(token)
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if before.TokenLabel != "Ann's phone" {
		t.Fatalf("label before = %q", before.TokenLabel)
	}

	if err := r.RenameToken(before.TokenID, "  Bedroom  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	after, err := r.FindByToken(token)
	if err != nil {
		t.Fatalf("find after: %v", err)
	}
	if after.TokenLabel != "Bedroom" {
		t.Errorf("label after = %q, want Bedroom (trimmed)", after.TokenLabel)
	}
	if after.TokenID != before.TokenID {
		t.Errorf("token id changed on rename: %q → %q", before.TokenID, after.TokenID)
	}

	if err := r.RenameToken("no-such-token", "x"); err != ErrTokenNotFound {
		t.Errorf("unknown token: err = %v, want ErrTokenNotFound", err)
	}
}
