package config

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// Decision D-B: ALEXANDRIA_PUBLIC_URL is published only as a plain https
// address, and by the same rule the phone applies (canonicalPublicUrl in
// the app), so the server never advertises an address the phone drops.
func TestPublicURLAcceptsOnlyPlainHTTPSAddresses(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "",
		"   ":                                 "",
		"https://alexandria.example.com":      "https://alexandria.example.com",
		"https://alexandria.home.example.net": "https://alexandria.home.example.net",
		// A trailing slash is normalised away; so are the default port
		// and the case of the scheme and host.
		"https://alexandria.example.com/":     "https://alexandria.example.com",
		" https://example.com:8443/alx// ":    "https://example.com:8443/alx",
		"HTTPS://Alexandria.Example.COM:443/": "https://alexandria.example.com",
		"https://192.168.1.50:4443":           "https://192.168.1.50:4443",
		"https://[::1]:8443/":                 "https://[::1]:8443",
		"https://[2001:db8::1]":               "https://[2001:db8::1]",
		// An unbracketed IPv6 literal: url.Parse would read its last
		// group as a port and publish a broken address.
		"https://2001:db8::1":                  "",
		"https://2001:db8::1/":                 "",
		"https://fe80::1:8443":                 "",
		"https://xn--lexandria-2fh.example":    "https://xn--lexandria-2fh.example",
		"https://example.com/a-b/c_d/e.f~g":    "https://example.com/a-b/c_d/e.f~g",
		"https://example.com:8443/Alexandria/": "https://example.com:8443/Alexandria",

		// Not https, or not an absolute address with a host.
		"http://192.168.1.50:4000":      "",
		"http://alexandria.example.com": "",
		"alexandria.example.com":        "",
		"ftp://example.com":             "",
		"https://":                      "",
		"https:///alx":                  "",
		"https:alexandria.example.com":  "",
		"://nonsense":                   "",

		// User-info, a query or a fragment: each makes the address read
		// as one place and connect to another, or carries a secret.
		"https://alexandria.home.example.net@evil.example/?x=1#frag": "",
		"https://alexandria.example.com@evil.example":                "",
		"https://user:secret@alexandria.example.com":                 "",
		"https://alexandria.example.com/?x=1":                        "",
		"https://alexandria.example.com/?":                           "",
		"https://alexandria.example.com?x=1":                         "",
		"https://alexandria.example.com/#frag":                       "",
		"https://alexandria.example.com/#":                           "",
		"https://alexandria.example.com#frag":                        "",

		// Percent-encoding (a host spelled in escapes), a backslash, a
		// space, a control character, a look-alike letter.
		"https://alex%61ndria.example.com":             "",
		"https://alexandria.example.com/a%2Fb":         "",
		`https://alexandria.example.com\@evil.example`: "",
		"https://alexandria.example.com/a b":           "",
		"https://alexandria.example.com/\x00":          "",
		"https://\u0430lexandria.example.com":          "",

		// A port outside 1-65535, a host that is not a plain DNS name or
		// IP literal, a path that is not plain.
		"https://alexandria.example.com:0":     "",
		"https://alexandria.example.com:65536": "",
		"https://alexandria.example.com:http":  "",
		"https://alexandria.example.com.":      "",
		"https://-alexandria.example.com":      "",
		"https://under_score.example.com":      "",
		"https://alexandria.example.com/../x":  "",
		"https://alexandria.example.com/a//b":  "",
		"https://alexandria.example.com/a;b":   "",
		"https://" + strings.Repeat("a", 2048): "",
	} {
		if got := publicURL(raw); got != want {
			t.Errorf("publicURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

// An address that is ignored is warned about, and the warning does not
// repeat it: user-info in it would put a password in the log.
func TestPublicURLWarnsWithoutRepeatingTheValue(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	for raw, why := range map[string]string{
		"https://user:hunter2@alexandria.example.com": "user name",
		"http://192.168.1.50:4000":                    "https://",
		"https://alexandria.example.com/?token=abc":   "query",
		"https://alexandria.example.com/#x":           "fragment",
		"https://alex%61ndria.example.com":            "percent",
	} {
		buf.Reset()
		if got := publicURL(raw); got != "" {
			t.Errorf("publicURL(%q) = %q, want it ignored", raw, got)
		}
		out := buf.String()
		if !strings.Contains(out, "ALEXANDRIA_PUBLIC_URL") || !strings.Contains(out, why) {
			t.Errorf("publicURL(%q) warned %q, want a warning naming %q", raw, out, why)
		}
		for _, secret := range []string{"hunter2", "token=abc", "192.168.1.50", "%61"} {
			if strings.Contains(out, secret) {
				t.Errorf("publicURL(%q) repeated %q in the log: %q", raw, secret, out)
			}
		}
	}

	buf.Reset()
	publicURL("https://alexandria.example.com")
	publicURL("")
	if buf.Len() != 0 {
		t.Errorf("a valid or empty address warned: %q", buf.String())
	}
}

func TestLoadReadsThePublicURL(t *testing.T) {
	t.Setenv("ALEXANDRIA_API_TOKEN", "0123456789abcdef0123")
	t.Setenv("ALEXANDRIA_PUBLIC_URL", "https://alexandria.example.com/")
	if got := Load().PublicURL; got != "https://alexandria.example.com" {
		t.Fatalf("PublicURL = %q", got)
	}
	t.Setenv("ALEXANDRIA_PUBLIC_URL", "")
	if got := Load().PublicURL; got != "" {
		t.Fatalf("PublicURL unset = %q", got)
	}
	// The review's deceptive value boots, and is not published.
	t.Setenv("ALEXANDRIA_PUBLIC_URL",
		"https://alexandria.home.example.net@evil.example/?x=1#frag")
	if got := Load().PublicURL; got != "" {
		t.Fatalf("PublicURL = %q, want the deceptive value ignored", got)
	}
}

// ALEXANDRIA_LISTEN_ADDR is one IP address, or unset: anything else is
// refused at start rather than quietly listening everywhere.
func TestListenAddrIsOneIPAddressOrUnset(t *testing.T) {
	for _, ok := range []string{"", "127.0.0.1", "192.168.1.9", "::1", "fd00::9"} {
		if problem := listenAddrProblem(ok); problem != "" {
			t.Errorf("listenAddrProblem(%q) = %q, want none", ok, problem)
		}
	}
	for _, bad := range []string{"localhost", "127.0.0.1:4000", "0.0.0.0/0", "[::1]", "all"} {
		if problem := listenAddrProblem(bad); problem == "" {
			t.Errorf("listenAddrProblem(%q) found no problem", bad)
		}
	}
}

// The Plex address watch is on unless switched off in words people use.
func TestPlexAddressWatchSwitch(t *testing.T) {
	for raw, want := range map[string]bool{
		"": true, "on": true, "1": true, "yes": true, "true": true, "anything": true,
		"off": false, "OFF": false, " false ": false, "0": false, "no": false, "No": false,
	} {
		if got := switchOn(raw); got != want {
			t.Errorf("%q: %v, want %v", raw, got, want)
		}
	}
}
