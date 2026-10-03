package repository

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// newAdminAuthDB is a migrated temporary database with one enrolled
// credential (dummy hash and secret: these tests never verify them).
func newAdminAuthDB(t *testing.T) *AdminAuthRepository {
	t.Helper()
	newProfileDB(t)
	auth := NewAdminAuthRepository()
	if err := auth.Enroll("hash", "SECRET", []string{"r1", "r2"}); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	return auth
}

func storedStep(t *testing.T) int64 {
	t.Helper()
	var step int64
	if err := database.DB.QueryRow(
		`SELECT COALESCE(last_totp_step, 0) FROM admin_credentials WHERE id = 1`).
		Scan(&step); err != nil {
		t.Fatal(err)
	}
	return step
}

// The race the old unconditional write lost: one step, many claimants,
// exactly one winner.
func TestClaimTOTPStepAdmitsExactlyOneOfManyConcurrentClaims(t *testing.T) {
	auth := newAdminAuthDB(t)
	const step, ceiling = 1000, 1001

	const n = 8
	wins := make([]bool, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			wins[i], errs[i] = auth.ClaimTOTPStep(step, ceiling)
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for i := range wins {
		if errs[i] != nil {
			t.Fatalf("claim %d: %v", i, errs[i])
		}
		if wins[i] {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent claims of one step won; want exactly 1", won, n)
	}
	if got := storedStep(t); got != step {
		t.Fatalf("stored step %d, want %d", got, step)
	}
}

func TestClaimTOTPStepRefusesSpentAndOlderSteps(t *testing.T) {
	auth := newAdminAuthDB(t)

	if ok, err := auth.ClaimTOTPStep(1000, 1001); err != nil || !ok {
		t.Fatalf("first claim: %v %v", ok, err)
	}
	if ok, _ := auth.ClaimTOTPStep(1000, 1001); ok {
		t.Fatal("the same step was claimed twice")
	}
	if ok, _ := auth.ClaimTOTPStep(999, 1001); ok {
		t.Fatal("an older step was claimed after a newer one was spent")
	}
	if ok, err := auth.ClaimTOTPStep(1001, 1001); err != nil || !ok {
		t.Fatalf("the next step was refused: %v %v", ok, err)
	}
	if got := storedStep(t); got != 1001 {
		t.Fatalf("stored step %d, want 1001", got)
	}
}

// A stored step beyond the ceiling is corrupt (a clock that ran ahead),
// not spent; claiming a genuine step must still overwrite it, or the
// owner is locked out for good.
func TestClaimTOTPStepOverwritesACorruptFutureStep(t *testing.T) {
	auth := newAdminAuthDB(t)
	if _, err := database.DB.Exec(
		`UPDATE admin_credentials SET last_totp_step = 5000 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if ok, err := auth.ClaimTOTPStep(1000, 1001); err != nil || !ok {
		t.Fatalf("a genuine step was refused over a corrupt future one: %v %v", ok, err)
	}
	if got := storedStep(t); got != 1000 {
		t.Fatalf("stored step %d, want 1000", got)
	}
	// …and once overwritten, replay protection is back.
	if ok, _ := auth.ClaimTOTPStep(1000, 1001); ok {
		t.Fatal("the step was claimed twice after the reset")
	}
}

// With no credential row there is nothing to claim against.
func TestClaimTOTPStepWithoutACredential(t *testing.T) {
	newProfileDB(t)
	if ok, err := NewAdminAuthRepository().ClaimTOTPStep(1000, 1001); err != nil || ok {
		t.Fatalf("claim with no credential: %v %v", ok, err)
	}
}

// --- admin device tokens -------------------------------------------

const day = 24 * time.Hour

// t0 is a fixed instant, so expiry arithmetic never depends on when the
// test runs.
var t0 = time.Date(2026, 10, 2, 18, 40, 0, 0, time.UTC)

// mkDevice issues a sign-in and returns its token and row.
func mkDevice(t *testing.T, auth *AdminAuthRepository, label string, persistent bool, now time.Time) (string, *AdminDevice) {
	t.Helper()
	token, err := adminauth.NewToken()
	if err != nil {
		t.Fatal(err)
	}
	token = "alxa_" + token
	d, err := auth.CreateDeviceToken(adminauth.HashToken(token), NewAdminDevice{
		Label: label, Persistent: persistent,
		CreatedVia: AdminDeviceViaTOTP, SchemeBasis: "proxy",
	}, now)
	if err != nil {
		t.Fatalf("create %s: %v", label, err)
	}
	return token, d
}

func lookup(t *testing.T, auth *AdminAuthRepository, token string, now time.Time) (*AdminDevice, DeviceTokenState) {
	t.Helper()
	d, state, err := auth.LookupDeviceToken(adminauth.HashToken(token), now)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	return d, state
}

// use is what RequireAdmin does on every request: look up, then touch.
func use(t *testing.T, auth *AdminAuthRepository, token string, now time.Time) DeviceTokenState {
	t.Helper()
	d, state := lookup(t, auth, token, now)
	if state == DeviceTokenValid {
		auth.TouchDeviceToken(d, now)
	}
	return state
}

func rowCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM admin_device_tokens`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func storedTimes(t *testing.T, id string) (lastUsed, expires int64) {
	t.Helper()
	if err := database.DB.QueryRow(`
		SELECT last_used_at, expires_at FROM admin_device_tokens WHERE id = ?`, id).
		Scan(&lastUsed, &expires); err != nil {
		t.Fatal(err)
	}
	return lastUsed, expires
}

// A stolen database must yield nothing replayable: no column may hold
// the token, and the hash must be adminauth's (the variant that trims).
func TestAdminDeviceTokenIsStoredOnlyAsItsHash(t *testing.T) {
	auth := newAdminAuthDB(t)
	token, d := mkDevice(t, auth, "Chris · iPhone", true, t0)

	rows, err := database.DB.Query(`SELECT * FROM admin_device_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i, v := range vals {
			s := fmt.Sprint(v)
			if b, ok := v.([]byte); ok {
				s = string(b)
			}
			if strings.Contains(s, token) || strings.Contains(s, strings.TrimPrefix(token, "alxa_")) {
				t.Fatalf("column %s holds the plaintext token", cols[i])
			}
		}
	}
	var hash string
	if err := database.DB.QueryRow(
		`SELECT token_hash FROM admin_device_tokens WHERE id = ?`, d.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if hash != adminauth.HashToken(token) {
		t.Fatal("token_hash is not adminauth.HashToken(token)")
	}
	// The trimming variant: a token pasted with a trailing newline still
	// resolves.
	if _, state := lookup(t, auth, token+"\n", t0); state != DeviceTokenValid {
		t.Fatalf("token with trailing whitespace: state %v", state)
	}
}

// The repository's device type has nowhere to put a secret.
func TestAdminDeviceCarriesNoTokenOrHash(t *testing.T) {
	typ := reflect.TypeOf(AdminDevice{})
	for i := 0; i < typ.NumField(); i++ {
		name := strings.ToLower(typ.Field(i).Name)
		if strings.Contains(name, "hash") || strings.Contains(name, "token") {
			t.Fatalf("AdminDevice has a %s field", typ.Field(i).Name)
		}
	}
}

func TestAdminDeviceTokenLastsUntilRevoked(t *testing.T) {
	auth := newAdminAuthDB(t)
	a, da := mkDevice(t, auth, "phone A", true, t0)
	b, db := mkDevice(t, auth, "phone B", true, t0)

	if _, s := lookup(t, auth, a, t0.Add(30*day)); s != DeviceTokenValid {
		t.Fatalf("A: %v", s)
	}
	gone, err := auth.RevokeDeviceToken(da.ID)
	if err != nil || gone.ID != da.ID || gone.Label != "phone A" {
		t.Fatalf("revoke A: %+v %v", gone, err)
	}
	if _, s := lookup(t, auth, a, t0); s != DeviceTokenUnknown {
		t.Fatalf("A after revoke: %v", s)
	}
	if _, s := lookup(t, auth, b, t0); s != DeviceTokenValid {
		t.Fatalf("revoking A ended B: %v", s)
	}
	if _, err := auth.RevokeDeviceToken(da.ID); !errors.Is(err, ErrAdminDeviceNotFound) {
		t.Fatalf("revoking A twice: %v", err)
	}

	all, err := auth.RevokeAllDeviceTokens()
	if err != nil || len(all) != 1 || all[0].ID != db.ID {
		t.Fatalf("revoke all: %+v %v", all, err)
	}
	if _, s := lookup(t, auth, b, t0); s != DeviceTokenUnknown {
		t.Fatalf("B after revoke-all: %v", s)
	}
	if none, err := auth.RevokeAllDeviceTokens(); err != nil || len(none) != 0 {
		t.Fatalf("revoke all with nothing left: %+v %v", none, err)
	}
}

// Re-enrolment is the break-glass reset for a lost device, which may be
// the phone: every admin sign-in must die with the old credential.
func TestEnrollWipesAdminDeviceTokens(t *testing.T) {
	auth := newAdminAuthDB(t)
	a, _ := mkDevice(t, auth, "phone A", true, t0)
	mkDevice(t, auth, "phone B", false, t0)
	if err := auth.Enroll("hash2", "SECRET2", []string{"r3"}); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t); n != 0 {
		t.Fatalf("%d admin device tokens survived re-enrolment", n)
	}
	if _, s := lookup(t, auth, a, t0); s != DeviceTokenUnknown {
		t.Fatalf("A after re-enrolment: %v", s)
	}
}

// A1: a persistent sign-in unused for 180 days expires — and the row is
// deleted on that lookup.
func TestPersistentSignInExpiresAfter180DaysUnused(t *testing.T) {
	auth := newAdminAuthDB(t)
	tok, d := mkDevice(t, auth, "phone", true, t0)
	if !d.ExpiresAt.Equal(t0.Add(180 * day)) {
		t.Fatalf("expires %v, want issue + 180 days", d.ExpiresAt)
	}

	if _, s := lookup(t, auth, tok, t0.Add(180*day-time.Second)); s != DeviceTokenValid {
		t.Fatalf("one second before the idle limit: %v", s)
	}
	got, s := lookup(t, auth, tok, t0.Add(180*day+time.Second))
	if s != DeviceTokenExpired || got == nil || got.ID != d.ID {
		t.Fatalf("one second past the idle limit: %v %+v", s, got)
	}
	if n := rowCount(t); n != 0 {
		t.Fatal("the expired row was not deleted on lookup")
	}
	if _, s := lookup(t, auth, tok, t0); s != DeviceTokenUnknown {
		t.Fatalf("an expired sign-in came back when the clock did: %v", s)
	}
}

// A1: every use renews a persistent sign-in, and nothing caps it.
func TestPersistentSignInSlidesWithUseAndHasNoCap(t *testing.T) {
	auth := newAdminAuthDB(t)
	tok, d := mkDevice(t, auth, "phone", true, t0)

	use(t, auth, tok, t0.Add(100*day))
	last, exp := storedTimes(t, d.ID)
	if last != t0.Add(100*day).Unix() || exp != t0.Add(280*day).Unix() {
		t.Fatalf("after a use on day 100: last %d exp %d, want day 100 / day 280", last, exp)
	}
	if _, s := lookup(t, auth, tok, t0.Add(279*day)); s != DeviceTokenValid {
		t.Fatalf("day 279, 179 days after the last use: %v", s)
	}

	// Used daily for well over a year: still signed in.
	for i := 101; i <= 500; i++ {
		if s := use(t, auth, tok, t0.Add(time.Duration(i)*day)); s != DeviceTokenValid {
			t.Fatalf("day %d of daily use: %v", i, s)
		}
	}
	if _, s := lookup(t, auth, tok, t0.Add(500*day+179*day)); s != DeviceTokenValid {
		t.Fatalf("179 days after the last of 500 days of use: %v", s)
	}
	if _, s := lookup(t, auth, tok, t0.Add(500*day+180*day)); s != DeviceTokenExpired {
		t.Fatalf("180 days after the last use: %v", s)
	}
}

// A sign-in that did not ask to stay lasts 12 hours from issue, however
// often it is used.
func TestSessionOnlySignInLastsTwelveHoursEvenWhenUsed(t *testing.T) {
	auth := newAdminAuthDB(t)
	tok, d := mkDevice(t, auth, "phone", false, t0)
	if !d.ExpiresAt.Equal(t0.Add(12 * time.Hour)) {
		t.Fatalf("expires %v, want issue + 12 h", d.ExpiresAt)
	}
	for m := 5; m < 12*60; m += 5 {
		if s := use(t, auth, tok, t0.Add(time.Duration(m)*time.Minute)); s != DeviceTokenValid {
			t.Fatalf("minute %d: %v", m, s)
		}
	}
	last, exp := storedTimes(t, d.ID)
	if exp != t0.Add(12*time.Hour).Unix() {
		t.Fatalf("use moved a session-only expiry to %d", exp)
	}
	if last <= t0.Unix() {
		t.Fatal("last_used_at was never recorded")
	}
	if _, s := lookup(t, auth, tok, t0.Add(12*time.Hour)); s != DeviceTokenExpired {
		t.Fatalf("at 12 h: %v", s)
	}
}

func TestLastUsedIsWrittenAtMostOncePerMinute(t *testing.T) {
	auth := newAdminAuthDB(t)
	tok, d := mkDevice(t, auth, "phone", true, t0)

	use(t, auth, tok, t0.Add(30*time.Second))
	if last, _ := storedTimes(t, d.ID); last != t0.Unix() {
		t.Fatalf("written 30 s after the last write: %d", last)
	}
	use(t, auth, tok, t0.Add(61*time.Second))
	if last, _ := storedTimes(t, d.ID); last != t0.Add(61*time.Second).Unix() {
		t.Fatalf("not written 61 s after the last write: %d", last)
	}
	use(t, auth, tok, t0.Add(90*time.Second))
	if last, _ := storedTimes(t, d.ID); last != t0.Add(61*time.Second).Unix() {
		t.Fatalf("written 29 s after the last write: %d", last)
	}

	// Two requests holding the same stale reading: only one writes.
	stale, _ := lookup(t, auth, tok, t0.Add(125*time.Second))
	auth.TouchDeviceToken(stale, t0.Add(125*time.Second))
	auth.TouchDeviceToken(stale, t0.Add(126*time.Second))
	if last, _ := storedTimes(t, d.ID); last != t0.Add(125*time.Second).Unix() {
		t.Fatalf("a second concurrent touch wrote: %d", last)
	}
}

func TestListDeviceTokensSkipsExpiredMostRecentlyUsedFirst(t *testing.T) {
	auth := newAdminAuthDB(t)
	_, a := mkDevice(t, auth, "A", true, t0)
	mkDevice(t, auth, "B session", false, t0)
	c, dc := mkDevice(t, auth, "C", true, t0.Add(time.Minute))
	use(t, auth, c, t0.Add(2*day))

	list, err := auth.ListDeviceTokens(t0.Add(3 * day))
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != dc.ID || list[1].ID != a.ID {
		t.Fatalf("list %+v, want C then A (B's 12 h is over)", list)
	}
	if !list[0].LastUsedAt.Equal(t0.Add(2*day)) || !list[0].Persistent ||
		list[0].Label != "C" || list[0].CreatedVia != AdminDeviceViaTOTP ||
		list[0].SchemeBasis != "proxy" {
		t.Fatalf("C listed as %+v", list[0])
	}
	if empty, err := auth.ListDeviceTokens(t0.Add(400 * day)); err != nil || len(empty) != 0 {
		t.Fatalf("list after everything expired: %+v %v", empty, err)
	}
}

func TestPurgeExpiredDeviceTokensRemovesOnlyTheExpired(t *testing.T) {
	auth := newAdminAuthDB(t)
	a, da := mkDevice(t, auth, "A", true, t0)
	_, db := mkDevice(t, auth, "B session", false, t0)
	_, di := mkDevice(t, auth, "idle", true, t0)
	use(t, auth, a, t0.Add(100*day))

	gone, err := auth.PurgeExpiredDeviceTokens(t0.Add(181 * day))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, g := range gone {
		ids[g.ID] = true
	}
	if len(gone) != 2 || !ids[db.ID] || !ids[di.ID] {
		t.Fatalf("purged %+v, want B (12 h over) and idle (181 days unused)", gone)
	}
	if _, s := lookup(t, auth, a, t0.Add(181*day)); s != DeviceTokenValid {
		t.Fatalf("A, used on day 100, after the purge: %v", s)
	}
	if n := rowCount(t); n != 1 {
		t.Fatalf("%d rows left, want 1 (%s)", n, da.ID)
	}
}

// Every migration runs on every boot; the new table must tolerate that
// and keep its rows.
func TestRunMigrationsTwiceKeepsAdminDeviceTokens(t *testing.T) {
	auth := newAdminAuthDB(t)
	tok, _ := mkDevice(t, auth, "phone", true, t0)
	if err := database.RunMigrations(); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, s := lookup(t, auth, tok, t0); s != DeviceTokenValid {
		t.Fatalf("after a second migration run: %v", s)
	}
}
