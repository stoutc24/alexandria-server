package main

// The 2026-11-02 security review's findings, against the real route table.
// The admin-access gate's is in adminaccess_http_test.go
// (TestTheAccessGateIgnoresHowAPathIsSpelt).

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"testing"
)

// A compressed body is refused before anything reads it: Fiber would
// unpack it with no limit — 65 KB of gzip became 64 MiB in the review's
// probe — and the sign-in reads its body before any token.
func TestCompressedRequestBodiesAreRefused(t *testing.T) {
	s := newTestServer(t)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	// 16 MiB of zeros once unpacked (twice the body limit), a few KB sent.
	chunk := make([]byte, 1<<20)
	for i := 0; i < 16; i++ {
		if _, err := zw.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.Len() > 64<<10 {
		t.Fatalf("the bomb is %d bytes: not a small request", buf.Len())
	}
	for _, enc := range []string{"gzip", "deflate", "br", "GZIP", "gzip, gzip", "identity, gzip"} {
		r, err := s.sendRaw("POST", "/api/v1/accounts/login", "application/json", buf.String(),
			map[string]string{"Content-Encoding": enc})
		if err != nil {
			t.Fatal(err)
		}
		if r.status != http.StatusUnsupportedMediaType || r.str("reason") != "encoded-body" {
			t.Errorf("Content-Encoding %q: %d %v — want 415 encoded-body", enc, r.status, r.body)
		}
	}

	// A plain body, and one that says identity, still reach the sign-in.
	for _, enc := range []string{"", "identity"} {
		hdr := map[string]string{}
		if enc != "" {
			hdr["Content-Encoding"] = enc
		}
		r, err := s.sendRaw("POST", "/api/v1/accounts/login", "application/json",
			`{"username":"nobody","password":"not it"}`, hdr)
		if err != nil {
			t.Fatal(err)
		}
		if r.status == http.StatusUnsupportedMediaType || r.status == http.StatusOK {
			t.Errorf("Content-Encoding %q: %d %v — want the sign-in's own refusal", enc, r.status, r.body)
		}
	}
}
