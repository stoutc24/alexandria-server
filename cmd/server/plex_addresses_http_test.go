package main

// A picked Plex person's addresses kept current by the admin's app
// (2026-11-14), against the real route table. No t.Parallel: database.DB
// is a package global (see app_test.go).

import (
	"fmt"
	"strings"
	"testing"
)

const (
	plexServerA = "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b"
	plexServerB = "aaaabbbbccccddddeeeeffff0000111122223333"
	oldRemote   = "https://203-0-113-7.abc123.plex.direct:32400"
	newRemote   = "https://198-51-100-4.abc123.plex.direct:32400"
	lanAddress  = "https://192-168-1-9.abc123.plex.direct:32400"
)

// pickPlexPerson adds a picked Plex person on serverID for profileID.
func pickPlexPerson(t *testing.T, s *testServer, profileID, serverID, key string, uris ...map[string]any) string {
	t.Helper()
	r := s.do("POST", "/api/v1/admin/profiles/"+profileID+"/services", map[string]any{
		"type": "plex", "loginKind": "plexuser", "token": key, "plex": map[string]any{
			"serverId": serverID, "serverName": "Home Plex", "userId": 42, "userTitle": "Kate",
			"connections": uris,
		},
	}, bearer(testAPIToken))
	if r.status != 201 {
		t.Fatalf("add plex person: %d %s", r.status, r)
	}
	return r.body["service"].(map[string]any)["id"].(string)
}

func plexPerson(t *testing.T, s *testServer, name string) string {
	t.Helper()
	r := s.do("POST", "/api/v1/admin/profiles", map[string]any{"name": name}, bearer(testAPIToken))
	if r.status != 201 {
		t.Fatalf("create profile: %d %s", r.status, r)
	}
	return r.body["profile"].(map[string]any)["id"].(string)
}

// acceptAs signs a device in as the person through an invite; returns
// its token.
func acceptAs(t *testing.T, s *testServer, profileID, username string, d testDevice) string {
	t.Helper()
	r := s.do("POST", "/api/v1/admin/profiles/"+profileID+"/invite", nil, bearer(testAPIToken))
	if r.status != 201 {
		t.Fatalf("invite: %d %s", r.status, r)
	}
	invite := r.body["invite"].(map[string]any)["token"].(string)
	r = s.do("POST", "/api/v1/accounts/invite/accept", map[string]any{
		"invite": invite, "username": username, "password": "a long enough one", "device": d.body(username + "'s phone"),
	}, nil)
	if r.status != 201 {
		t.Fatalf("accept: %d %s", r.status, r)
	}
	return r.str("deviceToken")
}

func connectionsBody(uris ...string) map[string]any {
	list := []map[string]any{}
	for _, u := range uris {
		c := map[string]any{"uri": u}
		if u == lanAddress {
			c["local"] = true
		}
		list = append(list, c)
	}
	return map[string]any{"connections": list}
}

func TestPlexConnectionsNeedTheAdmin(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	kate := plexPerson(t, s, "Kate")
	pickPlexPerson(t, s, kate, plexServerA, "key-kate", map[string]any{"uri": oldRemote})
	phone := newTestDevice(t)
	person := acceptAs(t, s, kate, "kate", phone)
	path := "/api/v1/admin/plex-servers/" + plexServerA + "/connections"

	for name, hdr := range map[string]map[string]string{
		"no token":         nil,
		"a person's token": bearer(person),
		"a wrong token":    bearer("not-the-token-0123456789abcdef"),
	} {
		if r := s.do("PUT", path, connectionsBody(newRemote), hdr); r.status != 401 {
			t.Errorf("%s: PUT answered %d %s", name, r.status, r)
		}
		if r := s.do("GET", "/api/v1/admin/plex-servers", nil, hdr); r.status != 401 {
			t.Errorf("%s: GET answered %d %s", name, r.status, r)
		}
	}
	// Nothing moved for the refused ones.
	mine := openServices(t, phone, s.do("GET", "/api/v1/accounts/services", nil, bearer(person)))
	if got := mine.Services[0].Plex.Connections; len(got) != 1 || got[0].URI != oldRemote {
		t.Fatalf("a refused request changed the pick: %+v", got)
	}

	// The admin's own phone sign-in may.
	e := enrollDirect(t)
	adminPhone := bearer(s.signIn(e, true, nil).token)
	if r := s.do("PUT", path, connectionsBody(newRemote), adminPhone); r.status != 200 {
		t.Fatalf("admin phone: %d %s", r.status, r)
	}
}

// Every person picked from the server gets the new addresses, with their
// own key, and their devices see the change at their next check; a person
// picked from another server is untouched.
func TestPlexConnectionsReachEveryPickOfThatServer(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	kate := plexPerson(t, s, "Kate")
	sam := plexPerson(t, s, "Sam")
	pickPlexPerson(t, s, kate, plexServerA, "key-kate",
		map[string]any{"uri": lanAddress, "local": true}, map[string]any{"uri": oldRemote})
	pickPlexPerson(t, s, sam, plexServerA, "key-sam", map[string]any{"uri": oldRemote})
	pickPlexPerson(t, s, sam, plexServerB, "key-sam-b", map[string]any{"uri": "https://203-0-113-9.fff999.plex.direct:32400"})
	kPhone, sPhone := newTestDevice(t), newTestDevice(t)
	kTok := bearer(acceptAs(t, s, kate, "kate", kPhone))
	sTok := bearer(acceptAs(t, s, sam, "sam", sPhone))
	before := openServices(t, kPhone, s.do("GET", "/api/v1/accounts/services", nil, kTok))

	// The admin's app reads what is saved.
	r := s.do("GET", "/api/v1/admin/plex-servers", nil, admin)
	if r.status != 200 || strings.Contains(string(r.raw), "key-") {
		t.Fatalf("list: %d %s", r.status, r)
	}
	servers := r.body["servers"].([]any)
	if len(servers) != 2 {
		t.Fatalf("servers %s", r)
	}

	r = s.do("PUT", "/api/v1/admin/plex-servers/"+plexServerA+"/connections",
		connectionsBody(lanAddress, newRemote, "https://plex.example.com"), admin)
	if r.status != 200 || fmt.Sprint(r.body["updated"]) != "2" || fmt.Sprint(r.body["skipped"]) != "1" {
		t.Fatalf("put: %d %s", r.status, r)
	}

	// Kate's device: a new revision, the new list, her own key.
	r = s.do("GET", "/api/v1/accounts/services?since="+before.Revision, nil, kTok)
	if r.body["unchanged"] == true {
		t.Fatal("Kate's device was told nothing changed")
	}
	k := openServices(t, kPhone, r).Services[0]
	if k.Token != "key-kate" || k.Plex == nil || len(k.Plex.Connections) != 2 ||
		k.Plex.Connections[0].URI != lanAddress || !k.Plex.Connections[0].Local ||
		k.Plex.Connections[1].URI != newRemote || k.BaseURL != newRemote {
		t.Fatalf("kate %+v %+v", k, k.Plex)
	}
	// Sam: both of his picks, only server A's changed.
	for _, svc := range openServices(t, sPhone, s.do("GET", "/api/v1/accounts/services", nil, sTok)).Services {
		switch svc.Plex.ServerID {
		case plexServerA:
			if svc.Token != "key-sam" || len(svc.Plex.Connections) != 2 || svc.Plex.Connections[1].URI != newRemote {
				t.Fatalf("sam A %+v %+v", svc, svc.Plex)
			}
		case plexServerB:
			if svc.Token != "key-sam-b" || len(svc.Plex.Connections) != 1 ||
				svc.Plex.Connections[0].URI != "https://203-0-113-9.fff999.plex.direct:32400" {
				t.Fatalf("sam B changed: %+v %+v", svc, svc.Plex)
			}
		}
	}

	// The same list again: nothing written, devices told nothing changed.
	after := openServices(t, kPhone, s.do("GET", "/api/v1/accounts/services", nil, kTok))
	r = s.do("PUT", "/api/v1/admin/plex-servers/"+plexServerA+"/connections",
		connectionsBody(lanAddress, newRemote), admin)
	if r.status != 200 || fmt.Sprint(r.body["updated"]) != "0" {
		t.Fatalf("again: %d %s", r.status, r)
	}
	if r := s.do("GET", "/api/v1/accounts/services?since="+after.Revision, nil, kTok); r.body["unchanged"] != true {
		t.Fatalf("an unchanged list moved the revision: %s", r)
	}
}

func TestPlexConnectionsChecksWhatItIsSent(t *testing.T) {
	s := newTestServer(t, withSecretsKey)
	admin := bearer(testAPIToken)
	kate := plexPerson(t, s, "Kate")
	pickPlexPerson(t, s, kate, plexServerA, "key-kate", map[string]any{"uri": oldRemote})
	path := "/api/v1/admin/plex-servers/" + plexServerA + "/connections"

	many := []string{}
	for i := 0; i < 65; i++ {
		many = append(many, newRemote)
	}
	for name, tc := range map[string]struct {
		path   string
		body   any
		status int
		reason string
	}{
		"no addresses":   {path, map[string]any{"connections": []any{}}, 400, "bad-service"},
		"too many":       {path, connectionsBody(many...), 400, "bad-service"},
		"not proven":     {path, connectionsBody("https://plex.example.com", "https://203-0-113-50.e7e7e7.plex.direct:32400"), 400, "bad-service"},
		"not http":       {path, connectionsBody("ftp://198-51-100-4.abc123.plex.direct"), 400, "bad-service"},
		"another server": {"/api/v1/admin/plex-servers/" + plexServerB + "/connections", connectionsBody(newRemote), 404, "plex-server-not-found"},
		"a bad id":       {"/api/v1/admin/plex-servers/a%20b/connections", connectionsBody(newRemote), 400, "bad-service"},
		"not json":       {path, "connections", 400, "bad-request"},
	} {
		r := s.do("PUT", tc.path, tc.body, admin)
		if r.status != tc.status || r.str("reason") != tc.reason {
			t.Errorf("%s: %d %s", name, r.status, r)
		}
	}
	// None of those changed anything.
	r := s.do("GET", "/api/v1/admin/plex-servers", nil, admin)
	saved := r.body["servers"].([]any)[0].(map[string]any)["saved"].([]any)[0].([]any)
	if len(saved) != 1 || saved[0].(map[string]any)["uri"] != oldRemote {
		t.Fatalf("a refused request changed the pick: %s", r)
	}
}
