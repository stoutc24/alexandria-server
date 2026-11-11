package middleware

import (
	"log"
	"net/netip"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

// ClientKey keys the rate limiters on whoever is asking.
//
// That is the TCP peer, unless the peer is a reverse proxy named in
// ALEXANDRIA_TRUSTED_PROXIES. Then it is the address that proxy was asked
// from: the right-most X-Forwarded-For entry that is not itself a trusted
// proxy. Right-most, because each proxy appends the address it was asked
// from to whatever the client sent. Everything to its left is the client's
// own say-so, and keying on it would let a client pick a fresh limit for
// every request, or spend someone else's.
//
// Without the setting nothing changes from before: behind a proxy every
// visitor is the proxy, and all of them share one limit. That fails safe
// (no header buys anyone a fresh limit), but it lets one visitor use up
// everyone's, which is why a request that looks proxied says so in the
// log. The proxy must SET or APPEND X-Forwarded-For: one that passes on
// whatever the client sent lets a client be anyone here. Whatever a
// client manages, the admin login caps wrong guesses from every address
// together (adminauth.GuessLimit), and linking caps new requests the
// same way.
//
// An IPv6 client is counted by its /56, not its full address (security
// review, 2026-11-10): a home or a rented server is handed a /64 at the
// least, and commonly a /56 (256 of them), which is one visitor all the
// same. Counted address by address, or even /64 by /64, that visitor had a
// fresh limit for every request, and alone could spend the allowance of
// wrong guesses every sign-in shares — locking the owner out of their own
// admin page. An IPv4 address is counted as itself.
func ClientKey(trusted []netip.Prefix) func(*fiber.Ctx) string {
	isTrusted := func(a netip.Addr) bool {
		for _, p := range trusted {
			if p.Contains(a) {
				return true
			}
		}
		return false
	}
	return func(c *fiber.Ctx) string {
		peer, _ := netip.AddrFromSlice(c.Context().RemoteIP())
		peer = peer.Unmap()
		forwarded := c.Request().Header.PeekAll(fiber.HeaderXForwardedFor)
		if !isTrusted(peer) {
			if len(forwarded) > 0 {
				noteUntrustedProxy(peer)
			}
			return limitKey(peer)
		}
		// Every X-Forwarded-For line, in the order received, read as one
		// list: a proxy that adds a line of its own instead of appending
		// to the client's still has the last word.
		var hops []string
		for _, line := range forwarded {
			hops = append(hops, strings.Split(string(line), ",")...)
		}
		for i := len(hops) - 1; i >= 0; i-- {
			addr, ok := forwardedAddr(hops[i])
			if !ok {
				// Not an address: nothing to its left can be believed.
				break
			}
			if !isTrusted(addr) {
				return limitKey(addr)
			}
		}
		// Only trusted proxies, or nothing usable: the proxy itself.
		return limitKey(peer)
	}
}

// limitKey is what a limit counts an address as: an IPv4 address itself,
// an IPv6 address's /56 (see ClientKey).
func limitKey(a netip.Addr) string {
	if a.Is6() && !a.Is4In6() {
		if p, err := a.Prefix(56); err == nil {
			return p.String()
		}
	}
	return a.String()
}

// forwardedAddr reads one X-Forwarded-For entry: an address, with or
// without a port.
func forwardedAddr(hop string) (netip.Addr, bool) {
	hop = strings.TrimSpace(hop)
	if a, err := netip.ParseAddr(hop); err == nil {
		return a.Unmap(), true
	}
	if ap, err := netip.ParseAddrPort(hop); err == nil {
		return ap.Addr().Unmap(), true
	}
	return netip.Addr{}, false
}

// The hint below is logged once per address, for the first few addresses:
// the proxy is named even when some other client got there first, and a
// flood of addresses cannot flood the log.
var (
	proxyNotedMu sync.Mutex
	proxyNoted   = make(map[netip.Addr]bool)
)

const maxNotedProxies = 8

// noteUntrustedProxy tells whoever runs the server that requests are
// arriving through a proxy it has not been told to believe. It names the
// address so the owner can check it is theirs; it trusts nothing itself.
func noteUntrustedProxy(peer netip.Addr) {
	proxyNotedMu.Lock()
	if proxyNoted[peer] || len(proxyNoted) >= maxNotedProxies {
		proxyNotedMu.Unlock()
		return
	}
	proxyNoted[peer] = true
	proxyNotedMu.Unlock()
	log.Printf("rate limits: requests from %s carry X-Forwarded-For, but "+
		"%s is not in ALEXANDRIA_TRUSTED_PROXIES, so everyone it forwards "+
		"shares one limit. If %s is your reverse proxy (and not an address "+
		"every device on your network shares), set "+
		"ALEXANDRIA_TRUSTED_PROXIES=%s and restart", peer, peer, peer, peer)
}
