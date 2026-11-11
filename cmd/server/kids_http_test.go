package main

// Kids mode (owner, 2026-11-10): a switch on a person that their devices
// read, over the real routes.

import (
	"net/http"
	"testing"
)

func TestKidsModeIsSwitchedPerPersonAndReadByTheirDevices(t *testing.T) {
	s := newTestServer(t)
	env := bearer(testAPIToken)
	kidsTV := s.linkedDevice("Stout Kids", "Playroom TV")
	dadsPhone := s.linkedDevice("Chris", "Chris's iPhone")

	health := s.expectStatus(http.StatusOK, "GET", "/health", nil, nil)
	found := false
	for _, c := range health.body["capabilities"].([]any) {
		found = found || c == "profiles.kids"
	}
	if !found {
		t.Fatal("/health does not list profiles.kids")
	}

	people := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, env)
	ids := map[string]string{}
	for _, p := range people.body["profiles"].([]any) {
		m := p.(map[string]any)
		ids[m["name"].(string)] = m["id"].(string)
		if m["kids"] != false {
			t.Fatalf("%v starts with kids = %v, want false", m["name"], m["kids"])
		}
	}
	// Off until switched on, on each device.
	for _, dev := range []map[string]string{kidsTV, dadsPhone} {
		if me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, dev); me.body["kids"] != false {
			t.Fatalf("a device before any switch: kids = %v", me.body["kids"])
		}
	}

	path := "/api/v1/admin/profiles/" + ids["Stout Kids"] + "/kids"
	r := s.expectStatus(http.StatusOK, "PUT", path, map[string]bool{"kids": true}, env)
	if p, _ := r.body["profile"].(map[string]any); p["kids"] != true || p["name"] != "Stout Kids" {
		t.Fatalf("the switch answered %v", r)
	}
	// That person's device, at once; nobody else's.
	if me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kidsTV); me.body["kids"] != true {
		t.Fatalf("the kids' TV after the switch: kids = %v", me.body["kids"])
	}
	if me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, dadsPhone); me.body["kids"] != false {
		t.Fatalf("someone else's phone after the switch: kids = %v", me.body["kids"])
	}
	people = s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, env)
	for _, p := range people.body["profiles"].([]any) {
		m := p.(map[string]any)
		if want := m["name"] == "Stout Kids"; m["kids"] != want {
			t.Fatalf("the people list has %v with kids = %v", m["name"], m["kids"])
		}
	}

	// And off again, at any time.
	s.expectStatus(http.StatusOK, "PUT", path, map[string]bool{"kids": false}, env)
	if me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kidsTV); me.body["kids"] != false {
		t.Fatalf("the kids' TV after switching it off: kids = %v", me.body["kids"])
	}

	// What it refuses: a body that does not say, a person who is gone, and
	// anyone who is not the admin (a device's own token included).
	s.expectStatus(http.StatusBadRequest, "PUT", path, map[string]string{"kids": "yes please"}, env)
	s.expectStatus(http.StatusBadRequest, "PUT", path, map[string]any{}, env)
	s.expectStatus(http.StatusNotFound, "PUT", "/api/v1/admin/profiles/no-such-person/kids",
		map[string]bool{"kids": true}, env)
	s.expectStatus(http.StatusUnauthorized, "PUT", path, map[string]bool{"kids": true}, nil)
	s.expectStatus(http.StatusUnauthorized, "PUT", path, map[string]bool{"kids": true}, kidsTV)
}

// An account given only "serveradmin" (the media servers' sign-ins) cannot
// switch Kids mode; "manage" can.
func TestKidsModeNeedsManageAccess(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	h := setUpHousehold(t, s)
	e := enrollDirect(t)
	owner := bearer(s.signIn(e, true, nil).token)
	path := "/api/v1/admin/profiles/" + h.lee + "/kids"

	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("serveradmin"), owner)
	r := s.samSignsIn(h, samPassword)
	if r.status != http.StatusCreated {
		t.Fatalf("Sam's sign-in: %v", r)
	}
	s.refused(http.StatusForbidden, "access-denied", "PUT", path, map[string]bool{"kids": true}, bearer(r.str("token")))

	s.expectStatus(http.StatusOK, "PUT", accessPath(h.sam), access("manage"), owner)
	r = s.samSignsIn(h, samPassword)
	if r.status != http.StatusCreated {
		t.Fatalf("Sam's sign-in with manage: %v", r)
	}
	s.expectStatus(http.StatusOK, "PUT", path, map[string]bool{"kids": true}, bearer(r.str("token")))
}
