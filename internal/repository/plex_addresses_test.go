package repository

import (
	"net/netip"
	"testing"
	"time"
)

const (
	plexA = "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b"
	plexB = "aaaabbbbccccddddeeeeffff0000111122223333"
)

// pickOn adds a picked Plex person on server id with conns, with key.
func pickOn(t *testing.T, acc *AccountRepository, profileID, serverID, key string, conns ...PlexConnection) *AccountService {
	t.Helper()
	s, err := acc.AddService(profileID, ServiceInput{
		Type: "plex", LoginKind: "plexuser", Token: str(key),
		Plex: &PlexService{ServerID: serverID, ServerName: "Home Plex", UserID: 7, UserTitle: "Kate",
			Connections: conns},
	}, time.Unix(1000, 0))
	if err != nil {
		t.Fatalf("add pick: %v", err)
	}
	return s
}

func servicesOf(t *testing.T, acc *AccountRepository, profileID string) map[string]AccountService {
	t.Helper()
	list, err := acc.ListServices(profileID, true)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]AccountService{}
	for _, s := range list {
		out[s.ID] = s
	}
	return out
}

var (
	homeLAN    = PlexConnection{URI: "https://192-168-1-9.abc123.plex.direct:32400", Local: true}
	homeOld    = PlexConnection{URI: "https://203-0-113-7.abc123.plex.direct:32400"}
	homeNew    = PlexConnection{URI: "https://198-51-100-4.abc123.plex.direct:32400"}
	homeRelay  = PlexConnection{URI: "https://139-162-1-2.abc123.plex.direct:8443", Relay: true}
	otherOld   = PlexConnection{URI: "https://203-0-113-9.fff999.plex.direct:32400"}
	customURL  = PlexConnection{URI: "https://plex.example.com"}
	attacker   = PlexConnection{URI: "https://203-0-113-50.e7e7e7.plex.direct:32400"}
	plainLocal = PlexConnection{URI: "http://192.168.1.9:32400", Local: true}
)

func TestRewritePlexDirectURI(t *testing.T) {
	ip := netip.MustParseAddr("198.51.100.4")
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"https://203-0-113-7.abc123.plex.direct:32400", "https://198-51-100-4.abc123.plex.direct:32400", true},
		{"https://203-0-113-7.ABC123.Plex.Direct:40000", "https://198-51-100-4.abc123.plex.direct:40000", true},
		{"https://203-0-113-7.abc123.plex.direct", "https://198-51-100-4.abc123.plex.direct", true},
		{"https://198-51-100-4.abc123.plex.direct:32400", "", false}, // already there
		{"https://192-168-1-9.abc123.plex.direct:32400", "", false},  // a private address
		{"https://100-64-1-2.abc123.plex.direct:32400", "", false},   // CGNAT
		{"http://203-0-113-7.abc123.plex.direct:32400", "", false},   // not https
		{"https://203.0.113.7:32400", "", false},                     // not plex.direct
		{"https://plex.example.com", "", false},
		{"https://203-0-113-7.abc123.plex.direct:32400/web", "", false}, // a path
		{"https://2001-db8--1.abc123.plex.direct:32400", "", false},     // IPv6
		{"https://x.203-0-113-7.abc123.plex.direct:32400", "", false},   // extra label
	} {
		got, ok := RewritePlexDirectURI(tc.in, ip)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got %q %v, want %q %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
	if _, ok := RewritePlexDirectURI(homeOld.URI, netip.MustParseAddr("2001:db8::1")); ok {
		t.Error("rewrote to an IPv6 address")
	}
}

func TestRewritePlexDirectLeavesLocalRelayAndOthers(t *testing.T) {
	ip := netip.MustParseAddr("198.51.100.4")
	in := []PlexConnection{homeLAN, plainLocal, homeOld, homeRelay, customURL,
		{URI: "https://203-0-113-7.abc123.plex.direct:32400", Local: true}}
	out, moved := RewritePlexDirect(in, ip)
	want := []PlexConnection{homeLAN, plainLocal, homeNew, homeRelay, customURL,
		{URI: "https://203-0-113-7.abc123.plex.direct:32400", Local: true}}
	if !sameConnections(out, want) {
		t.Fatalf("got %+v", out)
	}
	if len(moved) != 1 || moved[homeOld.URI] != homeNew.URI {
		t.Fatalf("moved %v", moved)
	}
	if !sameConnections(in, []PlexConnection{homeLAN, plainLocal, homeOld, homeRelay, customURL,
		{URI: "https://203-0-113-7.abc123.plex.direct:32400", Local: true}}) {
		t.Fatal("the input was changed")
	}
}

// Every person picked from that server gets the list; another server's
// picks, the keys, names and order stay; a list that changes nothing
// writes nothing.
func TestReplacePlexConnectionsUpdatesEveryPickOfThatServerOnly(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	kate, _ := profiles.Create("Kate")
	sam, _ := profiles.Create("Sam")
	abs, err := acc.AddService(kate.ID, ServiceInput{Type: "abs", BaseURL: "http://abs.local", LoginKind: "password",
		Username: str("kate"), Password: str("pw")}, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	kPick := pickOn(t, acc, kate.ID, plexA, "key-kate", homeLAN, homeOld, homeRelay)
	sPick := pickOn(t, acc, sam.ID, plexA, "key-sam", homeOld)
	other := pickOn(t, acc, sam.ID, plexB, "key-sam-b", otherOld)
	revKate, _ := acc.ServicesRevision(kate.ID)

	now := time.Unix(5000, 0)
	res, err := acc.ReplacePlexConnections(plexA, []PlexConnection{homeLAN, homeNew, homeRelay}, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 2 || res.Skipped != 0 {
		t.Fatalf("result %+v", res)
	}
	k := servicesOf(t, acc, kate.ID)
	s := servicesOf(t, acc, sam.ID)
	want := []PlexConnection{homeLAN, homeNew, homeRelay}
	if !sameConnections(k[kPick.ID].Plex.Connections, want) || !sameConnections(s[sPick.ID].Plex.Connections, want) {
		t.Fatalf("kate %+v sam %+v", k[kPick.ID].Plex.Connections, s[sPick.ID].Plex.Connections)
	}
	// The key, the person, the name and the order stay.
	if k[kPick.ID].Credentials.Token != "key-kate" || s[sPick.ID].Credentials.Token != "key-sam" ||
		k[kPick.ID].Plex.UserTitle != "Kate" || k[kPick.ID].Name != kPick.Name || k[kPick.ID].Priority != kPick.Priority {
		t.Fatalf("something else changed: %+v %+v", k[kPick.ID], k[kPick.ID].Plex)
	}
	// The shown address moves off the stale one; the time moves on.
	if k[kPick.ID].BaseURL != homeNew.URI || k[kPick.ID].UpdatedAt != now.Unix() {
		t.Fatalf("base %s updated %d", k[kPick.ID].BaseURL, k[kPick.ID].UpdatedAt)
	}
	// Another server's pick and other services are untouched.
	if !sameConnections(s[other.ID].Plex.Connections, []PlexConnection{otherOld}) ||
		s[other.ID].UpdatedAt != 1000 || s[other.ID].Credentials.Token != "key-sam-b" {
		t.Fatalf("the other server's pick changed: %+v", s[other.ID])
	}
	if k[abs.ID].UpdatedAt != 1000 || k[abs.ID].Credentials.Password != "pw" {
		t.Fatalf("another service changed: %+v", k[abs.ID])
	}
	// The person's devices see a new revision.
	if rev, _ := acc.ServicesRevision(kate.ID); rev == revKate {
		t.Fatal("the revision did not change")
	}

	// The same list again writes nothing.
	res, err = acc.ReplacePlexConnections(plexA, want, time.Unix(9000, 0))
	if err != nil || res.Updated != 0 {
		t.Fatalf("again: %+v %v", res, err)
	}
	if servicesOf(t, acc, kate.ID)[kPick.ID].UpdatedAt != now.Unix() {
		t.Fatal("an unchanged list was written")
	}
}

// An address is kept only when it is proven to be that server: already
// saved for it, or a plex.direct name with its hash. A custom address, or
// another server's plex.direct name, never takes the person's key.
func TestReplacePlexConnectionsKeepsOnlyProvenAddresses(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	kate, _ := profiles.Create("Kate")
	pick := pickOn(t, acc, kate.ID, plexA, "key-kate", plainLocal, homeOld)

	res, err := acc.ReplacePlexConnections(plexA, []PlexConnection{
		plainLocal,
		{URI: "HTTP://192.168.1.9:32400/", Local: true}, // the same, written another way
		homeNew,
		customURL,
		attacker,
		{URI: "https://198-51-100-4.abc123.plex.direct:32400/steal"}, // a path
		{URI: "ftp://198-51-100-4.abc123.plex.direct"},
		{URI: "http://198-51-100-4.abc123.plex.direct:32400"}, // plex.direct without TLS
		{URI: "https://user@198-51-100-4.abc123.plex.direct"},
	}, time.Unix(5000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Skipped != 6 {
		t.Fatalf("result %+v", res)
	}
	got := servicesOf(t, acc, kate.ID)[pick.ID].Plex.Connections
	if !sameConnections(got, []PlexConnection{plainLocal, homeNew}) {
		t.Fatalf("saved %+v", got)
	}

	// Nothing proven: refused, nothing written.
	if _, err := acc.ReplacePlexConnections(plexA, []PlexConnection{customURL, attacker}, time.Unix(6000, 0)); err != ErrBadPlex {
		t.Fatalf("unproven only: %v", err)
	}
	if servicesOf(t, acc, kate.ID)[pick.ID].UpdatedAt != 5000 {
		t.Fatal("a refused list was written")
	}
}

func TestReplacePlexConnectionsChecks(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	kate, _ := profiles.Create("Kate")
	pickOn(t, acc, kate.ID, plexA, "key-kate", homeOld)
	now := time.Unix(5000, 0)
	if _, err := acc.ReplacePlexConnections(plexB, []PlexConnection{homeNew}, now); err != ErrPlexServerNotFound {
		t.Fatalf("unknown server: %v", err)
	}
	for _, bad := range []string{"", "../x", "a b", "x/y"} {
		if _, err := acc.ReplacePlexConnections(bad, []PlexConnection{homeNew}, now); err != ErrBadPlex {
			t.Fatalf("server id %q: %v", bad, err)
		}
	}
	if _, err := acc.ReplacePlexConnections(plexA, nil, now); err != ErrBadPlex {
		t.Fatalf("empty: %v", err)
	}
	// A dozen at most, duplicates once.
	var many []PlexConnection
	for i := 0; i < 20; i++ {
		many = append(many, PlexConnection{URI: "https://203-0-113-" + string(rune('a'+i)) + ".abc123.plex.direct:32400"})
	}
	many = append([]PlexConnection{homeOld, homeOld}, many...)
	res, err := acc.ReplacePlexConnections(plexA, many, now)
	if err != nil || res.Updated != 1 {
		t.Fatalf("many: %+v %v", res, err)
	}
	all, _ := acc.PlexServers()
	if len(all) != 1 || len(all[0].Saved) != 1 || len(all[0].Saved[0]) != maxPlexConnections ||
		all[0].Saved[0][0] != homeOld {
		t.Fatalf("saved %+v", all)
	}
}

func TestPlexServersListsEachServerAndItsLists(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	if has, err := acc.HasPlexPicks(); err != nil || has {
		t.Fatalf("no picks yet: %v %v", has, err)
	}
	kate, _ := profiles.Create("Kate")
	sam, _ := profiles.Create("Sam")
	ann, _ := profiles.Create("Ann")
	pickOn(t, acc, kate.ID, plexA, "k1", homeLAN, homeOld)
	pickOn(t, acc, sam.ID, plexA, "k2", homeLAN, homeOld)
	pickOn(t, acc, ann.ID, plexA, "k3", homeOld)
	pickOn(t, acc, ann.ID, plexB, "k4", otherOld)
	if has, _ := acc.HasPlexPicks(); !has {
		t.Fatal("picks not seen")
	}
	all, err := acc.PlexServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("servers %+v", all)
	}
	var a PlexServerAddresses
	for _, s := range all {
		if s.ServerID == plexA {
			a = s
		}
	}
	if a.Picks != 3 || len(a.Saved) != 2 || len(a.Saved[0]) != 2 || len(a.Saved[1]) != 1 {
		t.Fatalf("server A %+v", a)
	}
}

// The watcher's rewrite: the one stale address moves in place, every
// other address stays, and a pick that already lists the new one just
// loses the stale one.
func TestRewritePlexConnection(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	kate, _ := profiles.Create("Kate")
	sam, _ := profiles.Create("Sam")
	k := pickOn(t, acc, kate.ID, plexA, "key-kate", homeLAN, homeOld, homeRelay)
	s := pickOn(t, acc, sam.ID, plexA, "key-sam", homeOld, homeNew)
	o := pickOn(t, acc, sam.ID, plexB, "key-b", otherOld)
	now := time.Unix(7000, 0)

	if _, err := acc.RewritePlexConnection(plexA, homeOld.URI, "https://198-51-100-4.zzz.plex.direct:32400", now); err != ErrBadPlex {
		t.Fatalf("another hash: %v", err)
	}
	if _, err := acc.RewritePlexConnection(plexA, homeOld.URI, customURL.URI, now); err != ErrBadPlex {
		t.Fatalf("not plex.direct: %v", err)
	}
	n, err := acc.RewritePlexConnection(plexA, homeOld.URI, homeNew.URI, now)
	if err != nil || n != 2 {
		t.Fatalf("rewrite: %d %v", n, err)
	}
	kk := servicesOf(t, acc, kate.ID)[k.ID]
	if !sameConnections(kk.Plex.Connections, []PlexConnection{homeLAN, homeNew, homeRelay}) ||
		kk.Credentials.Token != "key-kate" || kk.BaseURL != homeNew.URI || kk.UpdatedAt != now.Unix() {
		t.Fatalf("kate %+v %+v", kk, kk.Plex.Connections)
	}
	ss := servicesOf(t, acc, sam.ID)
	if !sameConnections(ss[s.ID].Plex.Connections, []PlexConnection{homeNew}) {
		t.Fatalf("sam %+v", ss[s.ID].Plex.Connections)
	}
	if ss[o.ID].UpdatedAt != 1000 {
		t.Fatal("the other server's pick changed")
	}
	// Nothing left to move.
	if n, err := acc.RewritePlexConnection(plexA, homeOld.URI, homeNew.URI, now); err != nil || n != 0 {
		t.Fatalf("again: %d %v", n, err)
	}
}

// The rows are read without the vault: the watcher's repository has none,
// and a pick written by it still opens for the person with the key.
func TestRewritePlexConnectionNeedsNoSecretsKey(t *testing.T) {
	acc, profiles := newAccountsDB(t, true)
	kate, _ := profiles.Create("Kate")
	k := pickOn(t, acc, kate.ID, plexA, "key-kate", homeOld)
	keyless := NewAccountRepository(nil)
	if n, err := keyless.RewritePlexConnection(plexA, homeOld.URI, homeNew.URI, time.Unix(7000, 0)); err != nil || n != 1 {
		t.Fatalf("rewrite: %d %v", n, err)
	}
	got := servicesOf(t, acc, kate.ID)[k.ID]
	if got.Unreadable || got.Credentials.Token != "key-kate" {
		t.Fatalf("the key did not survive: %+v", got)
	}
}
