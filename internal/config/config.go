package config

import (
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Config is the whole configuration surface. It is deliberately small:
// this server is a single-household appliance, not a service with
// tenants. One deployment, one library, one shared token.
type Config struct {
	Port     string
	DBPath   string
	APIToken string

	// ServerName labels this server in LAN discovery pick-lists. It
	// defaults to the machine's hostname, which is WRONG in Docker:
	// there the hostname is the container id, so the server appears to
	// be a different machine after every recreate (reported from a live
	// deployment). Set ALEXANDRIA_SERVER_NAME to pin it.
	ServerName string

	// PublicURL is the address this server is reached at from outside,
	// from ALEXANDRIA_PUBLIC_URL ("https://alexandria.example.com").
	// /health publishes it, so a phone linked over a LAN http:// address
	// can offer to switch to it. It also declares the server is reached
	// only over HTTPS, which an admin sign-in that stays signed in
	// records as its scheme basis ("config") when no header names a
	// scheme; a header saying http overrules it. Only a plain https
	// address is kept, in canonical form (see publicURL); empty when
	// unset or anything else.
	PublicURL string
}

// defaultToken is the placeholder shipped in .env.example and the
// Dockerfile. Booting with it would leave an open server, so it counts
// as unset.
const defaultToken = "change-me"

func Load() *Config {
	cfg := &Config{
		Port:       getEnv("PORT", "4000"),
		DBPath:     getEnv("DB_PATH", "./alexandria.db"),
		APIToken:   os.Getenv("ALEXANDRIA_API_TOKEN"),
		ServerName: os.Getenv("ALEXANDRIA_SERVER_NAME"),
		PublicURL:  publicURL(os.Getenv("ALEXANDRIA_PUBLIC_URL")),
	}

	// Refuse to start rather than come up unauthenticated. This server
	// holds a map of everything the household owns; an accidentally
	// exposed one with no token is worse than one that failed to boot.
	if cfg.APIToken == "" || cfg.APIToken == defaultToken {
		log.Fatal("ALEXANDRIA_API_TOKEN is not set.\n" +
			"Generate one with:  openssl rand -hex 32\n" +
			"then set it in .env (or pass it to the container).")
	}
	if len(cfg.APIToken) < 16 {
		log.Fatal("ALEXANDRIA_API_TOKEN is too short; use at least 16 " +
			"characters (openssl rand -hex 32).")
	}

	return cfg
}

// publicURL is ALEXANDRIA_PUBLIC_URL as /health publishes it: a plain
// https address, rebuilt from its parts as https://host[:port][/path]
// with the host lowercased and the default port and trailing slashes
// dropped. Anything else is ignored with a warning rather than refusing
// to boot: the setting is optional, and a typo in it must not take the
// household's server down.
func publicURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	canonical, problem := plainHTTPSAddress(raw)
	if problem != "" {
		// The value itself is not repeated: a user name and password
		// written into it would land in the log.
		log.Printf("ALEXANDRIA_PUBLIC_URL is ignored: %s. Set a plain "+
			"https address like https://alexandria.example.com, or leave "+
			"it empty", problem)
		return ""
	}
	return canonical
}

// The parts of a plain address, as the phone checks them.
var (
	dnsHostRe   = regexp.MustCompile(`^` + dnsLabel + `(?:\.` + dnsLabel + `)*$`)
	ipv6HostRe  = regexp.MustCompile(`^[0-9a-f:.]+$`)
	plainPathRe = regexp.MustCompile(`^(?:/[A-Za-z0-9._~-]+)*$`)
)

// dnsLabel is one DNS label: letters, digits and inner hyphens.
const dnsLabel = `[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?`

// plainHTTPSAddress returns raw in canonical form, or why it is not a
// plain https address. The rule is the phone's own (canonicalPublicUrl
// in the app), so the server never publishes an address the phone would
// silently drop:
//
//   - https only, with a host: the phone stays signed in only over
//     https, so an http:// address is no place to move a link to;
//   - no user-info, query or fragment, and none of @ ? # anywhere:
//     https://alexandria.example.com@evil.example reads as the first and
//     connects to the second;
//   - no percent-encoding, backslash, space or character outside
//     printable ASCII: they spell a host in disguise, or with letters
//     from another alphabet that look like ours (write an international
//     name in its xn-- form);
//   - a port from 1 to 65535, a host that is a plain DNS name or IP
//     literal, and a path of plain segments.
func plainHTTPSAddress(raw string) (canonical, problem string) {
	if len(raw) > 2048 {
		return "", "it is longer than 2048 characters"
	}
	for _, r := range raw {
		switch {
		case r == '@':
			return "", "it has a user name in it (@)"
		case r == '?':
			return "", "it has a query (?)"
		case r == '#':
			return "", "it has a fragment (#)"
		case r == '%':
			return "", "it has percent-encoding (%)"
		case r == '\\':
			return "", "it has a backslash"
		case r < 0x21 || r > 0x7e:
			return "", "it has a space, a control character or a " +
				"character outside plain ASCII (write an international " +
				"name in its xn-- form)"
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "it is not a URL"
	}
	if u.Scheme != "https" {
		return "", "it is not an https:// address"
	}
	if u.Opaque != "" || u.Host == "" {
		return "", "it has no host"
	}
	if u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		// Unreachable past the character check; kept as the rule itself.
		return "", "it has a user name, query or fragment in it"
	}

	// An IPv6 literal must be bracketed: unbracketed, url.Parse reads its
	// last group as a port ("https://2001:db8::1" → host "2001:db8:",
	// port "1"), which would publish a broken address.
	if strings.Count(u.Host, ":") > 1 && !strings.HasPrefix(u.Host, "[") {
		return "", "an IPv6 address must be written in [brackets]"
	}
	host := strings.ToLower(u.Hostname())
	ipv6 := strings.Contains(host, ":")
	plainHost := dnsHostRe.MatchString(host)
	if ipv6 {
		plainHost = ipv6HostRe.MatchString(host)
	}
	if host == "" || len(host) > 253 || !plainHost {
		return "", "its host is not a plain DNS name or IP address"
	}

	port := ""
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", "its port is not a number from 1 to 65535"
		}
		if n != 443 {
			port = ":" + strconv.Itoa(n)
		}
	}

	path := strings.TrimRight(u.Path, "/")
	if !plainPathRe.MatchString(path) {
		return "", "its path is not plain: letters, digits and . _ ~ - " +
			"between single slashes"
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return "", "its path has a . or .. segment"
		}
	}

	if ipv6 {
		host = "[" + host + "]"
	}
	return "https://" + host + port + path, ""
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
