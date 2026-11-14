// Package plexwatch keeps picked Plex people's remote addresses current
// when the home's public IP changes (owner, 2026-11-14).
//
// Plex's remote addresses carry the home's public IP inside the name
// ("https://203-0-113-7.<hash>.plex.direct:32400"). A picked person's
// addresses are saved at pick time, so a home on a dynamic IP loses them:
// the person's devices reach Plex at home and nowhere else. Every 15
// minutes, and only while someone is picked from a Plex server, this asks
// plex.tv which public IPv4 address this server is reached from
// (PublicIPURL: plain text, no sign-in, nothing sent but the request). When
// it changed, each saved remote plex.direct address is moved to the new IP
// — but only once the new address PROVES it is the same Plex server:
//
//   - its GET /identity (which every Plex server answers without a token)
//     names the saved server's machineIdentifier, over https with the
//     certificate checked; or
//   - when the router cannot reach its own public address from inside
//     ("hairpin"), so that check cannot answer here: one of the pick's LOCAL
//     addresses answers /identity with that machineIdentifier — the Plex
//     server is on this server's network — AND the old address named the
//     public IP this server had before. Then the Plex server and this server
//     share the home's address, and it moved for both. (A Plex server
//     reached through some other address — a VPN, another home — is never
//     moved on this proof: its old address was never this server's.)
//
// Nothing else is changed: local and relay addresses stay, as do addresses
// that are not plex.direct ones. No token or login is sent anywhere or
// read. Turned off with ALEXANDRIA_PLEX_ADDRESS_WATCH=off.
package plexwatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// PublicIPURL answers the caller's public IPv4 address as plain text
// ("203.0.113.7"). It is plex.tv's own, which Plex Media Server uses for
// the same question; it needs no sign-in. Checked 2026-11-14: 200,
// text/plain, the address and a newline.
const PublicIPURL = "https://plex.tv/:/ip"

// Interval is how often the address is asked while someone is picked from
// a Plex server.
const Interval = 15 * time.Minute

// metaKey is where the last public IP seen is kept (database.MetaSet).
const metaKey = "plexwatch.public_ip"

// maxTries is how many checks keep trying an address change whose new
// addresses could not be proven or refuted (Plex off for a moment): two
// hours' worth. The admin's app refreshes them from plex.tv either way.
const maxTries = 8

// Store is what the watcher needs from the accounts repository.
type Store interface {
	HasPlexPicks() (bool, error)
	PlexServers() ([]repository.PlexServerAddresses, error)
	RewritePlexConnection(serverID, oldURI, newURI string, now time.Time) (int, error)
}

// Watcher is the job. Zero values are filled by New; tests set their own.
type Watcher struct {
	Store Store
	// PublicIP asks for this server's public IPv4 address.
	PublicIP func(ctx context.Context) (netip.Addr, error)
	// Identity is the machineIdentifier a Plex server at base answers on
	// /identity (no token sent). An error means it could not be read.
	Identity func(ctx context.Context, base string) (string, error)
	// LastSeen and SetLastSeen keep the last public IP between runs.
	LastSeen    func() (string, error)
	SetLastSeen func(string) error
	Now         func() time.Time
	Logf        func(format string, args ...any)

	mu      sync.Mutex
	pending *change
}

// change is an IP change still being worked through.
type change struct {
	from  netip.Addr // invalid when no earlier address was known
	to    netip.Addr
	tries int
}

// New is a Watcher on the real network and database.
func New(store Store) *Watcher {
	return &Watcher{
		Store:       store,
		PublicIP:    FetchPublicIP,
		Identity:    FetchIdentity,
		LastSeen:    func() (string, error) { return database.MetaGet(metaKey) },
		SetLastSeen: func(v string) error { return database.MetaSet(metaKey, v) },
		Now:         time.Now,
		Logf:        log.Printf,
	}
}

// Start runs w in the background when on is true, and reports whether it
// did. Off, nothing runs at all: no database read, no request.
func Start(ctx context.Context, on bool, w *Watcher) bool {
	if !on || w == nil {
		return false
	}
	go w.Run(ctx)
	return true
}

// Result says what one run did (for the tests and the log).
type Result struct {
	// Skipped is why nothing was asked: "no-picks", "no-plex-direct",
	// "ip-unknown", "unchanged". Empty when the addresses were checked.
	Skipped string
	IP      netip.Addr
	// Moved: addresses rewritten (one per server and old address).
	Moved int
	// Refused: new addresses that answered as another server.
	Refused int
	// Unproven: new addresses neither proven nor refused.
	Unproven int
}

// Run checks once now, then every Interval until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	w.RunOnce(ctx)
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.RunOnce(ctx)
		}
	}
}

// candidate is one saved remote plex.direct address of one server.
type candidate struct {
	serverID   string
	serverName string
	uri        string
	ip         netip.Addr
	locals     []string
}

// RunOnce is one check. It never panics on a bad answer and changes
// nothing it cannot prove.
func (w *Watcher) RunOnce(ctx context.Context) Result {
	w.mu.Lock()
	defer w.mu.Unlock()

	has, err := w.Store.HasPlexPicks()
	if err != nil {
		w.Logf("plex address watch: could not read the picks: %v", err)
		return Result{Skipped: "no-picks"}
	}
	if !has {
		return Result{Skipped: "no-picks"}
	}
	servers, err := w.Store.PlexServers()
	if err != nil {
		w.Logf("plex address watch: could not read the picks: %v", err)
		return Result{Skipped: "no-picks"}
	}
	cands := candidates(servers)
	if len(cands) == 0 {
		// Nothing a public IP could fix: no plex.tv request at all.
		return Result{Skipped: "no-plex-direct"}
	}

	ip, err := w.PublicIP(ctx)
	if err != nil {
		w.Logf("plex address watch: could not learn this server's public address: %v", err)
		return Result{Skipped: "ip-unknown"}
	}
	res := Result{IP: ip}
	lastText, err := w.LastSeen()
	if err != nil {
		w.Logf("plex address watch: could not read the last address: %v", err)
		return Result{Skipped: "ip-unknown", IP: ip}
	}
	last, _ := netip.ParseAddr(strings.TrimSpace(lastText))
	if last != ip {
		if err := w.SetLastSeen(ip.String()); err != nil {
			w.Logf("plex address watch: could not save the address: %v", err)
		}
		if last.IsValid() {
			w.Logf("plex address watch: this server's public address changed")
		}
		w.pending = &change{from: last, to: ip}
	}
	if w.pending == nil || w.pending.to != ip {
		res.Skipped = "unchanged"
		return res
	}

	ch := w.pending
	ch.tries++
	for _, c := range cands {
		if c.ip == ip {
			continue // already there
		}
		newURI, ok := repository.RewritePlexDirectURI(c.uri, ip)
		if !ok {
			continue
		}
		switch w.verify(ctx, c, newURI, ch.from) {
		case proven:
			n, err := w.Store.RewritePlexConnection(c.serverID, c.uri, newURI, w.Now())
			if err != nil {
				w.Logf("plex address watch: could not save %q's new address: %v", c.serverName, err)
				res.Unproven++
				continue
			}
			if n > 0 {
				res.Moved++
				w.Logf("plex address watch: %q's remote address moved to this server's new public address (%d pick(s))",
					c.serverName, n)
			}
		case otherServer:
			res.Refused++
			w.Logf("plex address watch: %q's address at the new public address is another Plex server; left as it was",
				c.serverName)
		default:
			res.Unproven++
		}
	}
	if res.Unproven == 0 || ch.tries >= maxTries {
		if res.Unproven > 0 {
			w.Logf("plex address watch: %d address(es) could not be proven after %d tries; left as they were "+
				"(the admin's Alexandria app refreshes them from plex.tv)", res.Unproven, ch.tries)
		}
		w.pending = nil
	}
	return res
}

type verdict int

const (
	unknown verdict = iota
	proven
	otherServer
)

// verify asks newURI who it is, and when it cannot answer from here, the
// pick's local addresses (see the package comment for when that counts).
func (w *Watcher) verify(ctx context.Context, c candidate, newURI string, from netip.Addr) verdict {
	id, err := w.Identity(ctx, newURI)
	if err == nil && id != "" {
		if id == c.serverID {
			return proven
		}
		return otherServer
	}
	// No answer at the new address from inside. The LAN proof counts only
	// for an address that was this server's own public IP before.
	if !from.IsValid() || c.ip != from {
		return unknown
	}
	for _, local := range c.locals {
		if id, err := w.Identity(ctx, local); err == nil && id == c.serverID {
			return proven
		}
	}
	return unknown
}

// candidates are the saved remote (not local, not relay) plex.direct
// addresses naming a public IPv4, one per server and address, with the
// server's local addresses for the LAN proof.
func candidates(servers []repository.PlexServerAddresses) []candidate {
	var out []candidate
	for _, s := range servers {
		var locals []string
		seenLocal := map[string]bool{}
		for _, list := range s.Saved {
			for _, c := range list {
				if c.Local && !c.Relay && !seenLocal[c.URI] {
					seenLocal[c.URI] = true
					locals = append(locals, c.URI)
				}
			}
		}
		seen := map[string]bool{}
		for _, list := range s.Saved {
			for _, c := range list {
				if c.Local || c.Relay || seen[c.URI] {
					continue
				}
				ip, ok := repository.PlexDirectIPv4(c.URI)
				if !ok || !repository.PublicIPv4(ip) {
					continue
				}
				seen[c.URI] = true
				out = append(out, candidate{serverID: s.ServerID, serverName: s.ServerName,
					uri: c.URI, ip: ip, locals: locals})
			}
		}
	}
	return out
}

// ── the network ─────────────────────────────────────────────────────

// ipClient asks over IPv4 only: plex.direct names carry an IPv4 address,
// and over IPv6 the answer would be this server's IPv6 one.
var ipClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		// Asked directly, never through a proxy: the answer must be the
		// address this server itself is reached at.
		DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
			return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp4", addr)
		},
		TLSHandshakeTimeout: 5 * time.Second,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// FetchPublicIP asks PublicIPURL for this server's public IPv4 address.
// Only a public address is believed.
func FetchPublicIP(ctx context.Context) (netip.Addr, error) {
	return fetchPublicIP(ctx, ipClient, PublicIPURL)
}

func fetchPublicIP(ctx context.Context, client *http.Client, source string) (netip.Addr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return netip.Addr{}, err
	}
	req.Header.Set("Accept", "text/plain")
	resp, err := client.Do(req)
	if err != nil {
		return netip.Addr{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return netip.Addr{}, fmt.Errorf("the address source answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64))
	if err != nil {
		return netip.Addr{}, err
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(string(body)))
	if err != nil || !ip.Is4() || !repository.PublicIPv4(ip) {
		return netip.Addr{}, errors.New("the address source did not answer with a public IPv4 address")
	}
	return ip, nil
}

// identityClient checks certificates as any client does (a plex.direct
// name has a real one) and follows no redirect.
var identityClient = &http.Client{
	Timeout:       5 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// FetchIdentity is the machineIdentifier the Plex server at base answers on
// GET /identity, asked without a token.
func FetchIdentity(ctx context.Context, base string) (string, error) {
	return fetchIdentity(ctx, identityClient, base)
}

var machineIDAttr = regexp.MustCompile(`machineIdentifier="([^"]+)"`)

func fetchIdentity(ctx context.Context, client *http.Client, base string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/identity", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("/identity answered %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return "", err
	}
	var j struct {
		MediaContainer struct {
			MachineIdentifier string `json:"machineIdentifier"`
		} `json:"MediaContainer"`
	}
	if json.Unmarshal(body, &j) == nil && j.MediaContainer.MachineIdentifier != "" {
		return j.MediaContainer.MachineIdentifier, nil
	}
	if m := machineIDAttr.FindSubmatch(body); m != nil {
		return string(m[1]), nil
	}
	return "", errors.New("/identity named no server")
}
