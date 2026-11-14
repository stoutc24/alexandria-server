package plexwatch

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

const serverA = "5d776825e9c9b9b6e8f1a2b3c4d5e6f708192a3b"

var (
	lan      = repository.PlexConnection{URI: "https://192-168-1-9.abc123.plex.direct:32400", Local: true}
	oldAddr  = repository.PlexConnection{URI: "https://203-0-113-7.abc123.plex.direct:32400"}
	newAddr  = "https://198-51-100-4.abc123.plex.direct:32400"
	relay    = repository.PlexConnection{URI: "https://139-162-1-2.abc123.plex.direct:8443", Relay: true}
	custom   = repository.PlexConnection{URI: "https://plex.example.com"}
	newIP    = netip.MustParseAddr("198.51.100.4")
	oldIP    = netip.MustParseAddr("203.0.113.7")
	errNoNet = errors.New("no route")
)

// ── the network calls, against httptest servers ─────────────────────

func TestFetchIdentityReadsJSONAndXMLAndSendsNoToken(t *testing.T) {
	var mu sync.Mutex
	var seen []http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Clone())
		mu.Unlock()
		switch r.URL.Path {
		case "/json/identity":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"MediaContainer":{"size":0,"claimed":true,"machineIdentifier":%q,"version":"1.41"}}`, serverA)
		case "/xml/identity":
			fmt.Fprintf(w, `<?xml version="1.0"?><MediaContainer size="0" machineIdentifier="%s" version="1.41"/>`, serverA)
		case "/moved/identity":
			http.Redirect(w, r, "/json/identity", http.StatusFound)
		case "/empty/identity":
			fmt.Fprint(w, `{"MediaContainer":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 2 * time.Second
	client.CheckRedirect = identityClient.CheckRedirect
	ctx := context.Background()

	for _, base := range []string{srv.URL + "/json", srv.URL + "/xml/"} {
		if id, err := fetchIdentity(ctx, client, base); err != nil || id != serverA {
			t.Errorf("%s: %q %v", base, id, err)
		}
	}
	for _, base := range []string{srv.URL + "/moved", srv.URL + "/empty", srv.URL + "/gone"} {
		if id, err := fetchIdentity(ctx, client, base); err == nil {
			t.Errorf("%s: believed %q", base, id)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	for _, h := range seen {
		if h.Get("X-Plex-Token") != "" || h.Get("Authorization") != "" {
			t.Fatalf("a credential was sent: %v", h)
		}
	}
	// A certificate the system does not trust is refused (the real client).
	if _, err := FetchIdentity(ctx, srv.URL+"/json"); err == nil {
		t.Fatal("an untrusted certificate was believed")
	}
}

func TestFetchPublicIPBelievesOnlyAPublicIPv4(t *testing.T) {
	answer := ""
	status := 200
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("X-Plex-Token") != "" || r.URL.RawQuery != "" {
			t.Errorf("sent more than a bare request: %v %q", r.Header, r.URL.RawQuery)
		}
		w.WriteHeader(status)
		fmt.Fprint(w, answer)
	}))
	defer srv.Close()
	ctx := context.Background()
	for _, tc := range []struct {
		answer string
		status int
		ok     bool
	}{
		{"198.51.100.4\n", 200, true},
		{"  198.51.100.4  ", 200, true},
		{"192.168.1.1", 200, false},
		{"100.64.0.1", 200, false},
		{"127.0.0.1", 200, false},
		{"2001:db8::1", 200, false},
		{"198.51.100.4", 500, false},
		{"<html>hello</html>", 200, false},
		{strings.Repeat("1", 200), 200, false},
	} {
		answer, status = tc.answer, tc.status
		ip, err := fetchPublicIP(ctx, srv.Client(), srv.URL)
		if (err == nil) != tc.ok || (tc.ok && ip != newIP) {
			t.Errorf("%q %d: %v %v", tc.answer, tc.status, ip, err)
		}
	}
}

// ── the job, with fakes ─────────────────────────────────────────────

type fakeStore struct {
	t        *testing.T
	has      bool
	servers  []repository.PlexServerAddresses
	rewrites [][3]string
	touched  int
}

func (f *fakeStore) HasPlexPicks() (bool, error) { f.touched++; return f.has, nil }
func (f *fakeStore) PlexServers() ([]repository.PlexServerAddresses, error) {
	f.touched++
	return f.servers, nil
}
func (f *fakeStore) RewritePlexConnection(serverID, oldURI, newURI string, _ time.Time) (int, error) {
	f.touched++
	f.rewrites = append(f.rewrites, [3]string{serverID, oldURI, newURI})
	return 1, nil
}

type fakeNet struct {
	ip       netip.Addr
	ipCalls  int
	answers  map[string]string // base → machineIdentifier ("" = error)
	asked    []string
	lastSeen string
}

func newWatcher(store *fakeStore, n *fakeNet) *Watcher {
	return &Watcher{
		Store: store,
		PublicIP: func(context.Context) (netip.Addr, error) {
			n.ipCalls++
			if !n.ip.IsValid() {
				return netip.Addr{}, errNoNet
			}
			return n.ip, nil
		},
		Identity: func(_ context.Context, base string) (string, error) {
			n.asked = append(n.asked, base)
			if id := n.answers[base]; id != "" {
				return id, nil
			}
			return "", errNoNet
		},
		LastSeen:    func() (string, error) { return n.lastSeen, nil },
		SetLastSeen: func(v string) error { n.lastSeen = v; return nil },
		Now:         time.Now,
		Logf:        func(string, ...any) {},
	}
}

func homeServer(lists ...[]repository.PlexConnection) []repository.PlexServerAddresses {
	return []repository.PlexServerAddresses{{ServerID: serverA, ServerName: "Home Plex", Picks: len(lists), Saved: lists}}
}

func TestWatcherAsksNothingWithoutPicks(t *testing.T) {
	store := &fakeStore{t: t, has: false}
	n := &fakeNet{ip: newIP}
	res := newWatcher(store, n).RunOnce(context.Background())
	if res.Skipped != "no-picks" || n.ipCalls != 0 || len(n.asked) != 0 || store.touched != 1 {
		t.Fatalf("%+v ip calls %d asked %v", res, n.ipCalls, n.asked)
	}
	// Picks with no plex.direct remote address: still no request.
	store = &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{lan, custom, relay})}
	res = newWatcher(store, n).RunOnce(context.Background())
	if res.Skipped != "no-plex-direct" || n.ipCalls != 0 {
		t.Fatalf("%+v ip calls %d", res, n.ipCalls)
	}
}

func TestWatcherOffDoesNothing(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{oldAddr})}
	n := &fakeNet{ip: newIP}
	if Start(context.Background(), false, newWatcher(store, n)) {
		t.Fatal("started while switched off")
	}
	time.Sleep(20 * time.Millisecond)
	if store.touched != 0 || n.ipCalls != 0 {
		t.Fatalf("switched off, yet it ran: store %d ip %d", store.touched, n.ipCalls)
	}
}

func TestWatcherDoesNothingWhileTheIPStays(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{lan, oldAddr})}
	n := &fakeNet{ip: newIP, lastSeen: newIP.String(), answers: map[string]string{newAddr: serverA}}
	w := newWatcher(store, n)
	for i := 0; i < 3; i++ {
		res := w.RunOnce(context.Background())
		if res.Skipped != "unchanged" {
			t.Fatalf("run %d: %+v", i, res)
		}
	}
	if len(n.asked) != 0 || len(store.rewrites) != 0 {
		t.Fatalf("asked %v rewrote %v", n.asked, store.rewrites)
	}
}

// The new address answers as the same server: moved. Local, relay and
// custom addresses are never asked about or touched.
func TestWatcherMovesAnAddressThatProvesItself(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer(
		[]repository.PlexConnection{lan, oldAddr, relay, custom},
		[]repository.PlexConnection{oldAddr})}
	n := &fakeNet{ip: newIP, lastSeen: oldIP.String(), answers: map[string]string{newAddr: serverA}}
	w := newWatcher(store, n)
	res := w.RunOnce(context.Background())
	if res.Moved != 1 || res.Unproven != 0 || res.Refused != 0 {
		t.Fatalf("%+v", res)
	}
	if len(store.rewrites) != 1 || store.rewrites[0] != [3]string{serverA, oldAddr.URI, newAddr} {
		t.Fatalf("rewrites %v", store.rewrites)
	}
	if len(n.asked) != 1 || n.asked[0] != newAddr {
		t.Fatalf("asked %v", n.asked)
	}
	if n.lastSeen != newIP.String() {
		t.Fatalf("the new address was not kept: %q", n.lastSeen)
	}
	// Settled: the next check asks nothing more.
	if res := w.RunOnce(context.Background()); res.Skipped != "unchanged" || len(n.asked) != 1 {
		t.Fatalf("second run %+v asked %v", res, n.asked)
	}
}

// Another Plex server answers at the new address: left as it was.
func TestWatcherLeavesAnAddressThatIsAnotherServer(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{lan, oldAddr})}
	n := &fakeNet{ip: newIP, lastSeen: oldIP.String(),
		answers: map[string]string{newAddr: "ffffffffffffffffffffffffffffffffffffffff", lan.URI: serverA}}
	res := newWatcher(store, n).RunOnce(context.Background())
	if res.Refused != 1 || res.Moved != 0 || len(store.rewrites) != 0 {
		t.Fatalf("%+v rewrites %v", res, store.rewrites)
	}
	// The LAN is not asked once the new address said who it is.
	if len(n.asked) != 1 {
		t.Fatalf("asked %v", n.asked)
	}
}

// The router cannot reach its own public address (no hairpin): the pick's
// LAN address proves the server is on this network, and the old address
// was this server's own public IP — moved.
func TestWatcherHairpinFallsBackToTheLAN(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{lan, oldAddr})}
	n := &fakeNet{ip: newIP, lastSeen: oldIP.String(), answers: map[string]string{lan.URI: serverA}}
	res := newWatcher(store, n).RunOnce(context.Background())
	if res.Moved != 1 || len(store.rewrites) != 1 || store.rewrites[0][2] != newAddr {
		t.Fatalf("%+v rewrites %v", res, store.rewrites)
	}
	if strings.Join(n.asked, " ") != newAddr+" "+lan.URI {
		t.Fatalf("asked %v", n.asked)
	}
}

// The LAN proof does not count for an address that was never this
// server's own IP (a Plex server behind a VPN, or in another home), nor
// on the first check, when the old IP is not known.
func TestWatcherLANProofNeedsTheOldIPToBeOurs(t *testing.T) {
	vpn := repository.PlexConnection{URI: "https://192-0-2-77.abc123.plex.direct:32400"}
	for name, n := range map[string]*fakeNet{
		"another address": {ip: newIP, lastSeen: oldIP.String(), answers: map[string]string{lan.URI: serverA}},
		"first check":     {ip: newIP, lastSeen: "", answers: map[string]string{lan.URI: serverA}},
	} {
		saved := []repository.PlexConnection{lan, oldAddr}
		if name == "another address" {
			saved = []repository.PlexConnection{lan, vpn}
		}
		store := &fakeStore{t: t, has: true, servers: homeServer(saved)}
		res := newWatcher(store, n).RunOnce(context.Background())
		if res.Moved != 0 || res.Unproven != 1 || len(store.rewrites) != 0 {
			t.Fatalf("%s: %+v rewrites %v", name, res, store.rewrites)
		}
	}
}

// Nothing answers anywhere (Plex off for a moment): kept trying for a
// while, then left to the admin's app.
func TestWatcherRetriesAnUnprovenChangeThenStops(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{lan, oldAddr})}
	n := &fakeNet{ip: newIP, lastSeen: oldIP.String(), answers: map[string]string{}}
	w := newWatcher(store, n)
	for i := 1; i <= maxTries; i++ {
		if res := w.RunOnce(context.Background()); res.Unproven != 1 {
			t.Fatalf("try %d: %+v", i, res)
		}
	}
	asked := len(n.asked)
	if res := w.RunOnce(context.Background()); res.Skipped != "unchanged" || len(n.asked) != asked {
		t.Fatalf("kept trying: %+v", res)
	}
	// It comes back when Plex does, within the tries.
	n2 := &fakeNet{ip: newIP, lastSeen: oldIP.String(), answers: map[string]string{}}
	w2 := newWatcher(store, n2)
	w2.RunOnce(context.Background())
	n2.answers[newAddr] = serverA
	if res := w2.RunOnce(context.Background()); res.Moved != 1 {
		t.Fatalf("after Plex came back: %+v", res)
	}
}

func TestWatcherWithoutAnIPChangesNothing(t *testing.T) {
	store := &fakeStore{t: t, has: true, servers: homeServer([]repository.PlexConnection{oldAddr})}
	n := &fakeNet{lastSeen: oldIP.String()}
	res := newWatcher(store, n).RunOnce(context.Background())
	if res.Skipped != "ip-unknown" || len(n.asked) != 0 || len(store.rewrites) != 0 || n.lastSeen != oldIP.String() {
		t.Fatalf("%+v", res)
	}
}

// ── the job against a real database ─────────────────────────────────

func TestWatcherRewritesTheSavedPick(t *testing.T) {
	if err := database.Connect(filepath.Join(t.TempDir(), "watch.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	key := make([]byte, vault.KeySize)
	_, _ = rand.Read(key)
	v, err := vault.New(key)
	if err != nil {
		t.Fatal(err)
	}
	acc := repository.NewAccountRepository(v)
	kate, err := repository.NewProfileRepository().Create("Kate")
	if err != nil {
		t.Fatal(err)
	}
	token := "key-kate"
	svc, err := acc.AddService(kate.ID, repository.ServiceInput{
		Type: "plex", LoginKind: "plexuser", Token: &token,
		Plex: &repository.PlexService{ServerID: serverA, ServerName: "Home Plex", UserID: 7, UserTitle: "Kate",
			Connections: []repository.PlexConnection{lan, oldAddr, relay}},
	}, time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.MetaSet(metaKey, oldIP.String()); err != nil {
		t.Fatal(err)
	}

	// The real store (no secrets key, as main gives it) and the real meta.
	w := New(repository.NewAccountRepository(nil))
	w.Logf = func(string, ...any) {}
	w.PublicIP = func(context.Context) (netip.Addr, error) { return newIP, nil }
	w.Identity = func(_ context.Context, base string) (string, error) {
		if base == newAddr {
			return serverA, nil
		}
		return "", errNoNet
	}
	if res := w.RunOnce(context.Background()); res.Moved != 1 {
		t.Fatalf("%+v", res)
	}
	if seen, _ := database.MetaGet(metaKey); seen != newIP.String() {
		t.Fatalf("kept %q", seen)
	}
	list, err := acc.ListServices(kate.ID, true)
	if err != nil || len(list) != 1 || list[0].ID != svc.ID {
		t.Fatalf("%+v %v", list, err)
	}
	got := list[0]
	want := []repository.PlexConnection{lan, {URI: newAddr}, relay}
	if len(got.Plex.Connections) != 3 {
		t.Fatalf("%+v", got.Plex.Connections)
	}
	for i := range want {
		if got.Plex.Connections[i] != want[i] {
			t.Fatalf("%+v", got.Plex.Connections)
		}
	}
	if got.Credentials == nil || got.Credentials.Token != token || got.BaseURL != newAddr {
		t.Fatalf("%+v", got)
	}
}
