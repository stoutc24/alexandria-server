package repository

import (
	"strings"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
)

func newLinkDB(t *testing.T) (*LinkRepository, *ProfileRepository) {
	t.Helper()
	profiles, _ := newProfileDB(t)
	return NewLinkRepository(), profiles
}

func TestCodesAreReadableAloud(t *testing.T) {
	links, _ := newLinkDB(t)

	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		code, _, _, err := links.CreateRequest("phone", "")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if len(code) != codeLength {
			t.Fatalf("code %q is not %d characters", code, codeLength)
		}
		// 0/O/1/I/L are the characters people misread across a room.
		if strings.ContainsAny(code, "01OIL") {
			t.Fatalf("code %q contains an ambiguous character", code)
		}
		if seen[code] {
			t.Fatalf("duplicate code %q issued while still pending", code)
		}
		seen[code] = true
	}
}

// The whole security argument: the spoken code is not a credential.
func TestTheCodeAloneGrantsNothing(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	code, claim, _, err := links.CreateRequest("Sarah's phone", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// Someone who overhears the code cannot claim with it.
	if _, _, _, err := links.Claim(code); err != ErrLinkNotFound {
		t.Fatal("the spoken code worked as a claim secret")
	}

	// And before approval, even the real claim secret yields nothing.
	profileID, _, _, err := links.Claim(claim)
	if err != nil {
		t.Fatalf("claim while pending: %v", err)
	}
	if profileID != "" {
		t.Fatal("an unapproved request handed out a profile")
	}

	// Only the admin path turns it into access.
	if err := links.Approve(code, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	profileID, _, _, err = links.Claim(claim)
	if err != nil {
		t.Fatalf("claim after approve: %v", err)
	}
	if profileID != sarah.ID {
		t.Fatalf("claim returned %q, want %q", profileID, sarah.ID)
	}
}

// Alexandria Link: the key a request was made with comes back at its
// claim, and only there; a request without one gives back "".
func TestTheRequestsKeyComesBackAtTheClaim(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	code, claim, _, err := links.CreateRequest("Sarah's TV", "a2V5LWJ5dGVz")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, key, _ := links.Claim(claim); key != "" {
		t.Fatalf("a pending claim handed out the key %q", key)
	}
	if err := links.Approve(code, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, key, err := links.Claim(claim); err != nil || key != "a2V5LWJ5dGVz" {
		t.Fatalf("claim: key %q, err %v", key, err)
	}

	code, claim, _, _ = links.CreateRequest("old app", "")
	_ = links.Approve(code, sarah.ID)
	if _, _, key, err := links.Claim(claim); err != nil || key != "" {
		t.Fatalf("keyless claim: key %q, err %v", key, err)
	}
}

func TestClaimIsSingleUse(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	code, claim, _, _ := links.CreateRequest("phone", "")
	if err := links.Approve(code, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, _, _, err := links.Claim(claim); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if _, _, _, err := links.Claim(claim); err != ErrLinkUsed {
		t.Fatalf("a replayed claim secret was accepted: %v", err)
	}
}

func TestApprovingTwiceIsRejected(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")
	chris, _ := mkProfile(t, profiles, "Chris")

	code, _, _, _ := links.CreateRequest("phone", "")
	if err := links.Approve(code, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	// A second approval must not silently move the device to Chris.
	if err := links.Approve(code, chris.ID); err != ErrLinkUsed {
		t.Fatalf("expected ErrLinkUsed, got %v", err)
	}
}

func TestExpiredCodesAreRefused(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	code, claim, _, _ := links.CreateRequest("phone", "")

	// Age the request past its TTL.
	if _, err := database.DB.Exec(
		`UPDATE link_requests SET expires_at = ? WHERE code = ?`,
		time.Now().UTC().Add(-time.Minute), code,
	); err != nil {
		t.Fatalf("age: %v", err)
	}

	if err := links.Approve(code, sarah.ID); err != ErrLinkExpired {
		t.Errorf("expired approve: got %v", err)
	}
	if _, _, _, err := links.Claim(claim); err != ErrLinkExpired {
		t.Errorf("expired claim: got %v", err)
	}
}

func TestPendingListShowsOnlyLiveRequests(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	liveCode, _, _, _ := links.CreateRequest("living room TV", "")
	approvedCode, _, _, _ := links.CreateRequest("already done", "")
	expiredCode, _, _, _ := links.CreateRequest("too slow", "")

	if err := links.Approve(approvedCode, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := database.DB.Exec(
		`UPDATE link_requests SET expires_at = ? WHERE code = ?`,
		time.Now().UTC().Add(-time.Minute), expiredCode,
	); err != nil {
		t.Fatalf("age: %v", err)
	}

	pending, err := links.ListPending()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(pending) != 1 || pending[0].Code != liveCode {
		t.Fatalf("expected only the live request, got %+v", pending)
	}
	if pending[0].DeviceLabel != "living room TV" {
		t.Errorf("device label lost: %q", pending[0].DeviceLabel)
	}
}

func TestUnknownCodeIsNotFound(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")
	if err := links.Approve("ZZZZZZ", sarah.ID); err != ErrLinkNotFound {
		t.Fatalf("expected ErrLinkNotFound, got %v", err)
	}
}

// A failed approval must not leave a person behind. The handler creates
// the profile only after CheckPending passes; this guards the check it
// depends on.
func TestCheckPendingRefusesWhatApproveWouldRefuse(t *testing.T) {
	links, profiles := newLinkDB(t)
	sarah, _ := mkProfile(t, profiles, "Sarah")

	if err := links.CheckPending("ZZZZZZ"); err != ErrLinkNotFound {
		t.Errorf("unknown code: got %v", err)
	}

	code, _, _, _ := links.CreateRequest("phone", "")
	if err := links.CheckPending(code); err != nil {
		t.Errorf("a live code should pass: %v", err)
	}

	if err := links.Approve(code, sarah.ID); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if err := links.CheckPending(code); err != ErrLinkUsed {
		t.Errorf("used code: got %v", err)
	}

	expired, _, _, _ := links.CreateRequest("slow device", "")
	if _, err := database.DB.Exec(
		`UPDATE link_requests SET expires_at = ? WHERE code = ?`,
		time.Now().UTC().Add(-time.Minute), expired,
	); err != nil {
		t.Fatalf("age: %v", err)
	}
	if err := links.CheckPending(expired); err != ErrLinkExpired {
		t.Errorf("expired code: got %v", err)
	}

	// And it must not have created anything along the way.
	list, err := profiles.List()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("CheckPending created profiles: %d exist", len(list))
	}
}
