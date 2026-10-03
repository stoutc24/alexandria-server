package handlers

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeDeviceLabel(t *testing.T) {
	long := strings.Repeat("é", 100) // 2 bytes each: a byte cap would split one
	for in, want := range map[string]string{
		"Chris · iPhone":               "Chris · iPhone",
		"  padded  ":                   "padded",
		"":                             "phone",
		" \t\n ":                       "phone",
		"bell\a and\x00 nul":           "bell and nul",
		"line\nbreak\r\n":              "linebreak",
		"\x1b[31mred\x1b[0m":           "[31mred[0m",
		long:                           strings.Repeat("é", 80),
		strings.Repeat("a", 79) + " b": strings.Repeat("a", 79),
		"bad \xff utf8":                "bad � utf8",
	} {
		if got := sanitizeDeviceLabel(in); got != want {
			t.Errorf("sanitizeDeviceLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

// Every basis is reachable — direct TLS included, which no in-memory
// request carries — and a declaration never overrules a header. proxy
// is proxyScheme's answer: "" when no X-Forwarded-* header was sent.
func TestSchemeBasisRules(t *testing.T) {
	cfHTTPS, cfHTTP := `{"scheme":"https"}`, `{"scheme":"http"}`
	fwdHTTPS := "for=192.0.2.60;proto=https;by=203.0.113.43"
	fwdHTTP := "for=192.0.2.60;proto=http"
	cases := []struct {
		name                    string
		tls                     bool
		proxy, forwarded, cfVis string
		declared, stay          bool
		want                    string
	}{
		// 1. Cloudflare's edge saw the visitor, and it said http.
		{"Cloudflare http outranks direct TLS", true, "https", fwdHTTPS, cfHTTP, true, true, ""},
		{"Cloudflare http outranks an inner https", false, "https", "", cfHTTP, true, true, ""},
		// 2. Evidence of HTTPS, most direct first.
		{"direct TLS beats the headers", true, "https", fwdHTTPS, cfHTTPS, true, true, SchemeBasisTLS},
		{"proxy header", false, "https", fwdHTTPS, cfHTTPS, true, true, SchemeBasisProxy},
		{"proxy header, upper case", false, "HTTPS", "", "", false, false, SchemeBasisProxy},
		{"RFC 7239", false, "http", fwdHTTPS, cfHTTPS, true, true, SchemeBasisForwarded},
		{"Cloudflare", false, "", "for=1.2.3.4", cfHTTPS, true, true, SchemeBasisCloudflare},
		{"Cloudflare Tunnel into a proxy saying http", false, "http", "", cfHTTPS, true, true, SchemeBasisCloudflare},
		{"an https beside an inner http", false, "https", fwdHTTP, "", true, true, SchemeBasisProxy},
		{"evidence counts without asking to stay", false, "https", "", "", false, false, SchemeBasisProxy},
		// 3. A header saying http, and none https: no declaration overrules it.
		{"proxy http over both declarations", false, "http", "", "", true, true, ""},
		{"RFC 7239 http over both declarations", false, "", fwdHTTP, "", true, true, ""},
		{"proxy http over the phone's word", false, "http", "", "", false, true, ""},
		// 4. No header names a scheme.
		{"operator's declaration", false, "", "", "", true, true, SchemeBasisConfig},
		{"the phone's own word", false, "", "", "", false, true, SchemeBasisClient},
		{"operator's declaration, sign-in did not stay", false, "", "", "", true, false, ""},
		{"nothing at all", false, "", "", "", false, false, ""},
		{"garbage headers name nothing", false, "", ";;=,", "{not json", true, true, SchemeBasisConfig},
		{"garbage headers, did not stay", false, "", ";;=,", "{not json", true, false, ""},
	}
	for _, c := range cases {
		got := decideSchemeBasis(c.tls, c.proxy, c.forwarded, c.cfVis, c.declared, c.stay)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestForwardedProto(t *testing.T) {
	for in, want := range map[string]string{
		"proto=https":                         "https",
		`Proto="HTTPS"`:                       "HTTPS",
		"for=1.2.3.4; proto=https":            "https",
		"for=1.2.3.4, for=5.6.7.8;proto=http": "http",
		"for=1.2.3.4;proto=https, proto=http": "https",
		"for=1.2.3.4":                         "",
		"":                                    "",
	} {
		if got := forwardedProto(in); got != want {
			t.Errorf("forwardedProto(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCFVisitorScheme(t *testing.T) {
	for in, want := range map[string]string{
		`{"scheme":"https"}`: "https",
		`{"scheme":"http"}`:  "http",
		`{}`:                 "",
		`not json`:           "",
		"":                   "",
	} {
		if got := cfVisitorScheme(in); got != want {
			t.Errorf("cfVisitorScheme(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRFC3339IsUTCToTheSecond(t *testing.T) {
	at := time.Date(2026, 10, 2, 13, 40, 0, 999_999_999, time.FixedZone("CDT", -5*3600))
	if got := rfc3339(at); got != "2026-10-02T18:40:00Z" {
		t.Fatalf("rfc3339 = %q", got)
	}
}
