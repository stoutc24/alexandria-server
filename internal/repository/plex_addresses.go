package repository

// Keeping a picked Plex person's addresses current (owner, 2026-11-14).
//
// A "plexuser" service keeps every address plex.tv listed for the admin's
// Plex server AT PICK TIME. Plex's remote addresses carry the home's public
// IP inside the name ("https://203-0-113-7.<hash>.plex.direct:32400"), so
// when a home's address changes, the saved list rots: the person's devices
// reach Plex at home (the LAN address) and nowhere else. Two things keep it
// current, both through here:
//
//   - The admin's app reads the server's addresses from plex.tv with the
//     admin's own Plex sign-in and hands them over
//     (PUT /admin/plex-servers/:serverId/connections).
//   - This server watches its own public IP and moves a saved plex.direct
//     address to the new one once the new one proves it is that server
//     (internal/plexwatch).
//
// A saved login never follows a service somewhere new (security sweep
// 2026-10, S1): moving an address normally needs the login typed again.
// Here the person's key stays, so an address is taken only when it is
// PROVEN to be the same Plex server:
//
//   - it is already saved for that server, or
//   - it is an https "<anything>.<hash>.plex.direct" address whose <hash>
//     is one already saved for that server. Plex issues the certificate for
//     "*.<hash>.plex.direct" to that one server, and every app checks it, so
//     only that server can answer there — whatever IP the first label names.
//
// "Saved for that server" means saved in THAT PERSON'S OWN PICK, never in
// someone else's pick of the same server id (pre-release review
// 2026-11-15): a pick's server id is only what it claims, so an admin could
// add a decoy pick claiming a real server's id with any address in it, and
// a proof shared across picks would then hand every real pick's key to that
// address. Each pick proves only its own addresses.
//
// Anything else (a custom address set in Plex, a plex.direct name of
// another server) is left out and counted as skipped: it reaches the
// person's devices only with a new pick, which carries their key again.

import (
	"database/sql"
	"errors"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// ErrPlexServerNotFound: no picked Plex person uses that server.
var ErrPlexServerNotFound = errors.New("no one is picked from that Plex server")

// PlexServerAddresses is one Plex server people were picked from, and the
// addresses saved for it. No secret: the same as the admin's list of a
// person's services shows.
type PlexServerAddresses struct {
	ServerID   string `json:"serverId"`
	ServerName string `json:"serverName"`
	// Picks is how many picked people use this server.
	Picks int `json:"picks"`
	// Saved is each distinct address list saved for it, most picks first
	// (usually one: everyone picked at the same time).
	Saved [][]PlexConnection `json:"saved"`
}

// PlexConnectionsUpdate is what ReplacePlexConnections did.
type PlexConnectionsUpdate struct {
	// Updated is how many picks got the new list (0: every one had it).
	Updated int `json:"updated"`
	// Skipped is how many of the given addresses were left out because
	// they are not proven to be this server.
	Skipped int `json:"skipped"`
}

type plexPickRow struct {
	profileID string
	id        string
	baseURL   string
	plex      *PlexService
}

type rowQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// plexPickRows reads every picked Plex person's service, or only those on
// serverID when it is not empty. Rows whose pick will not read are left out.
func plexPickRows(q rowQuerier, serverID string) ([]plexPickRow, error) {
	rows, err := q.Query(`
		SELECT profile_id, id, base_url, plex FROM account_services
		WHERE login_kind = 'plexuser' AND plex IS NOT NULL
		ORDER BY created_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []plexPickRow
	for rows.Next() {
		var r plexPickRow
		var plex sql.NullString
		if err := rows.Scan(&r.profileID, &r.id, &r.baseURL, &plex); err != nil {
			return nil, err
		}
		r.plex = readPlexColumn(plex)
		if r.plex == nil || len(r.plex.Connections) == 0 {
			continue
		}
		if serverID != "" && r.plex.ServerID != serverID {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// HasPlexPicks is whether anyone is picked from a Plex server at all.
func (r *AccountRepository) HasPlexPicks() (bool, error) {
	var n int
	err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM account_services
		WHERE login_kind = 'plexuser' AND plex IS NOT NULL`).Scan(&n)
	return n > 0, err
}

// PlexServers lists every Plex server people were picked from, with the
// addresses saved for each, by server name.
func (r *AccountRepository) PlexServers() ([]PlexServerAddresses, error) {
	rows, err := plexPickRows(database.DB, "")
	if err != nil {
		return nil, err
	}
	type listCount struct {
		list  []PlexConnection
		count int
	}
	byServer := map[string]*PlexServerAddresses{}
	lists := map[string][]*listCount{}
	var order []string
	for _, row := range rows {
		id := row.plex.ServerID
		s := byServer[id]
		if s == nil {
			s = &PlexServerAddresses{ServerID: id, ServerName: row.plex.ServerName}
			byServer[id] = s
			order = append(order, id)
		}
		s.Picks++
		found := false
		for _, lc := range lists[id] {
			if sameConnections(lc.list, row.plex.Connections) {
				lc.count++
				found = true
				break
			}
		}
		if !found {
			lists[id] = append(lists[id], &listCount{list: row.plex.Connections, count: 1})
		}
	}
	out := make([]PlexServerAddresses, 0, len(order))
	for _, id := range order {
		s := byServer[id]
		ls := lists[id]
		sort.SliceStable(ls, func(i, j int) bool { return ls[i].count > ls[j].count })
		for _, lc := range ls {
			s.Saved = append(s.Saved, lc.list)
		}
		out = append(out, *s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].ServerName) < strings.ToLower(out[j].ServerName)
	})
	return out, nil
}

// sameConnections is whether two lists are the same addresses, flags and
// order.
func sameConnections(a, b []PlexConnection) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// plexDirectName splits "<first>.<hash>.plex.direct" (any case) into its
// first label and hash; ok is false for any other host.
func plexDirectName(host string) (first, hash string, ok bool) {
	labels := strings.Split(strings.ToLower(strings.TrimSuffix(host, ".")), ".")
	if len(labels) != 4 || labels[2] != "plex" || labels[3] != "direct" ||
		labels[0] == "" || labels[1] == "" {
		return "", "", false
	}
	for _, c := range labels[1] {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return "", "", false
		}
	}
	return labels[0], labels[1], true
}

// plexDirectHash is the hash of an https plex.direct address with no path
// or query, else "".
func plexDirectHash(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil ||
		strings.Trim(u.Path, "/") != "" || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}
	if _, hash, ok := plexDirectName(u.Hostname()); ok {
		return hash
	}
	return ""
}

// plexAnchors is what proves an address belongs to a server: the addresses
// already saved for it, and the plex.direct hashes among them.
type plexAnchors struct {
	saved  []string
	hashes map[string]bool
}

func anchorsOf(rows []plexPickRow) plexAnchors {
	a := plexAnchors{hashes: map[string]bool{}}
	for _, row := range rows {
		for _, c := range row.plex.Connections {
			a.saved = append(a.saved, c.URI)
			if h := plexDirectHash(c.URI); h != "" {
				a.hashes[h] = true
			}
		}
	}
	return a
}

// proves is whether uri (normalized) is proven to be the anchors' server.
func (a plexAnchors) proves(uri string) bool {
	for _, s := range a.saved {
		if sameServiceAddress(uri, s) {
			return true
		}
	}
	h := plexDirectHash(uri)
	return h != "" && a.hashes[h]
}

// ReplacePlexConnections gives every picked person on serverID the
// addresses conns, proven ones only (see the top of this file), checked as
// a pick's are (http(s) only, duplicates once, at most a dozen, at least
// one). The person's key, name, place in the order and everything else
// stay; a pick whose shown address is no longer listed shows the new
// preferred one. A pick that already has the list is not written. Each
// change bumps the service's updated_at, so the person's devices see a new
// revision and fetch the new addresses.
func (r *AccountRepository) ReplacePlexConnections(serverID string, conns []PlexConnection, now time.Time) (PlexConnectionsUpdate, error) {
	var res PlexConnectionsUpdate
	serverID = strings.TrimSpace(serverID)
	if !plexServerIDRe.MatchString(serverID) {
		return res, ErrBadPlex
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return res, err
	}
	defer tx.Rollback()
	rows, err := plexPickRows(tx, serverID)
	if err != nil {
		return res, err
	}
	if len(rows) == 0 {
		return res, ErrPlexServerNotFound
	}

	// The given addresses, normalized, each once, in order.
	seen := map[string]bool{}
	given := []PlexConnection{}
	for _, c := range conns {
		u, err := NormalizeServiceURL(c.URI)
		if err != nil {
			res.Skipped++
			continue
		}
		key := strings.ToLower(u)
		if seen[key] {
			continue
		}
		seen[key] = true
		given = append(given, PlexConnection{URI: u, Local: c.Local, Relay: c.Relay})
	}

	// Each pick takes the addresses ITS OWN saved list proves (top of this
	// file): another pick of the same claimed server id never vouches.
	lists := make([][]PlexConnection, len(rows))
	provenForSome := map[string]bool{}
	someList := false
	for i, row := range rows {
		anchors := anchorsOf([]plexPickRow{row})
		list := []PlexConnection{}
		for _, c := range given {
			if !anchors.proves(c.URI) {
				continue
			}
			provenForSome[strings.ToLower(c.URI)] = true
			if len(list) < maxPlexConnections {
				list = append(list, c)
			}
		}
		lists[i] = list
		if len(list) > 0 {
			someList = true
		}
	}
	for _, c := range given {
		if !provenForSome[strings.ToLower(c.URI)] {
			res.Skipped++
		}
	}
	if !someList {
		return res, ErrBadPlex
	}

	for i, row := range rows {
		list := lists[i]
		if len(list) == 0 || sameConnections(row.plex.Connections, list) {
			continue
		}
		if err := writePlexConnections(tx, row, list, now); err != nil {
			return res, err
		}
		res.Updated++
	}
	if res.Updated == 0 {
		return res, nil
	}
	return res, tx.Commit()
}

// RewritePlexConnection moves one saved address of serverID's picks,
// oldURI, to newURI — the same server's plex.direct name with a new IP —
// keeping its place and flags. Every other address stays as it is. Both
// must be https plex.direct addresses of the same hash. Returns how many
// picks changed.
func (r *AccountRepository) RewritePlexConnection(serverID, oldURI, newURI string, now time.Time) (int, error) {
	oldHash, newHash := plexDirectHash(oldURI), plexDirectHash(newURI)
	if oldHash == "" || oldHash != newHash {
		return 0, ErrBadPlex
	}
	newURI, err := NormalizeServiceURL(newURI)
	if err != nil {
		return 0, err
	}
	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := plexPickRows(tx, serverID)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, row := range rows {
		list := make([]PlexConnection, 0, len(row.plex.Connections))
		moved, has := false, false
		for _, c := range row.plex.Connections {
			if sameServiceAddress(c.URI, newURI) {
				has = true
			}
		}
		for _, c := range row.plex.Connections {
			if sameServiceAddress(c.URI, oldURI) {
				moved = true
				if has {
					continue // already listed: the stale one just goes
				}
				has = true
				c.URI = newURI
			}
			list = append(list, c)
		}
		if !moved || len(list) == 0 {
			continue
		}
		if err := writePlexConnections(tx, row, list, now); err != nil {
			return 0, err
		}
		changed++
	}
	if changed == 0 {
		return 0, nil
	}
	return changed, tx.Commit()
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// writePlexConnections stores list as row's addresses. Only the plex
// column, the shown address (when it is no longer listed) and updated_at
// change; the sealed login is not read or written.
func writePlexConnections(tx execer, row plexPickRow, list []PlexConnection, now time.Time) error {
	p := *row.plex
	p.Connections = list
	col, err := plexColumn(&p)
	if err != nil {
		return err
	}
	base := row.baseURL
	listed := false
	for _, c := range list {
		if sameServiceAddress(c.URI, base) {
			listed = true
			break
		}
	}
	if !listed {
		base = p.preferredURI()
	}
	// updated_at always moves on, even within the second the pick was
	// saved: it is what moves the revision the person's devices check.
	_, err = tx.Exec(`
		UPDATE account_services SET plex = ?, base_url = ?, updated_at = MAX(?, updated_at + 1)
		WHERE profile_id = ? AND id = ? AND login_kind = 'plexuser'`,
		col, base, now.Unix(), row.profileID, row.id)
	return err
}

// RewritePlexDirectURI is uri with the IPv4 address in its plex.direct
// name changed to ip: "https://203-0-113-7.<hash>.plex.direct:32400" →
// "https://198-51-100-4.<hash>.plex.direct:32400". ok is false when uri is
// not such an address, names a private or local IP, or already names ip.
func RewritePlexDirectURI(uri string, ip netip.Addr) (string, bool) {
	if !ip.Is4() || plexDirectHash(uri) == "" {
		return "", false
	}
	u, err := url.Parse(uri)
	if err != nil {
		return "", false
	}
	first, hash, _ := plexDirectName(u.Hostname())
	old, err := netip.ParseAddr(strings.ReplaceAll(first, "-", "."))
	if err != nil || !old.Is4() || !publicIPv4(old) || old == ip {
		return "", false
	}
	host := strings.ReplaceAll(ip.String(), ".", "-") + "." + hash + ".plex.direct"
	if p := u.Port(); p != "" {
		host += ":" + p
	}
	return "https://" + host, true
}

// PlexDirectIPv4 is the IPv4 address an https plex.direct address names,
// when it names one.
func PlexDirectIPv4(uri string) (netip.Addr, bool) {
	if plexDirectHash(uri) == "" {
		return netip.Addr{}, false
	}
	u, err := url.Parse(uri)
	if err != nil {
		return netip.Addr{}, false
	}
	first, _, _ := plexDirectName(u.Hostname())
	a, err := netip.ParseAddr(strings.ReplaceAll(first, "-", "."))
	if err != nil || !a.Is4() {
		return netip.Addr{}, false
	}
	return a, true
}

// RewritePlexDirect is conns with every remote address (not local, not a
// relay) whose plex.direct name carries an old public IPv4 moved to ip.
// Local and relay addresses, and every other address, stay as they are.
// moved maps each old address to its new one.
func RewritePlexDirect(conns []PlexConnection, ip netip.Addr) (out []PlexConnection, moved map[string]string) {
	moved = map[string]string{}
	out = make([]PlexConnection, 0, len(conns))
	for _, c := range conns {
		if !c.Local && !c.Relay {
			if n, ok := RewritePlexDirectURI(c.URI, ip); ok {
				moved[c.URI] = n
				c.URI = n
			}
		}
		out = append(out, c)
	}
	return out, moved
}

// publicIPv4 is whether a is an address on the internet: not private,
// loopback, link-local, shared (CGNAT 100.64/10), multicast or unspecified.
func publicIPv4(a netip.Addr) bool {
	if !a.Is4() || !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() {
		return false
	}
	return !netip.MustParsePrefix("100.64.0.0/10").Contains(a)
}

// PublicIPv4 is publicIPv4 for callers outside the package.
func PublicIPv4(a netip.Addr) bool { return publicIPv4(a) }
