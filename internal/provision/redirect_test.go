package provision

// Redirects follow only on the server the admin saved (security review,
// 2026-11-02): Go re-sends a 307's body to wherever it points, and the
// review's probe saw an Audiobookshelf sign-in body — the admin's password
// — arrive at a second server.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func TestARedirectToAnotherServerNeverCarriesTheAdminsPassword(t *testing.T) {
	var mu sync.Mutex
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		elsewhere = append(elsewhere, r.Method+" "+r.URL.Path+" "+string(body))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer other.Close()
	saved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer saved.Close()

	_, err := Ensure(context.Background(), NewHTTPClient(), "abs",
		Admin{BaseURL: saved.URL, Username: "root", Password: "S3cret-admin-pass"},
		Person{Username: "emily", Password: "her-new-pass"}, false)
	if err == nil {
		t.Fatal("a sign-in sent on to another server succeeded")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(elsewhere) != 0 {
		t.Fatalf("the other server was sent %q", elsewhere)
	}
}

func TestARedirectOnTheSameServerIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "here")
	}))
	defer srv.Close()
	resp, err := NewHTTPClient().Get(srv.URL + "/old")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "here" {
		t.Fatalf("got %q", body)
	}
}

func TestNeverFromHTTPSDownToPlainHTTP(t *testing.T) {
	req := func(raw string) *http.Request {
		u, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: u}
	}
	if err := sameServerRedirect(req("http://media.example/b"),
		[]*http.Request{req("https://media.example/a")}); err == nil ||
		!strings.Contains(err.Error(), "plain http") {
		t.Fatalf("https → http: %v", err)
	}
	// Plain http up to https on the same server is fine.
	if err := sameServerRedirect(req("https://media.example/b"),
		[]*http.Request{req("http://media.example/a")}); err != nil {
		t.Fatalf("http → https: %v", err)
	}
	// Up, then back down, is still down.
	if err := sameServerRedirect(req("http://media.example/c"),
		[]*http.Request{req("http://media.example/a"), req("https://media.example/b")}); err == nil {
		t.Fatal("http → https → http followed")
	}
	if err := sameServerRedirect(req("https://elsewhere.example/b"),
		[]*http.Request{req("https://media.example/a")}); err == nil {
		t.Fatal("another host followed")
	}
}
