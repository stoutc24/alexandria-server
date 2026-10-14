// Command flow is the admin page's full-flow test harness. TEST-ONLY: it
// lives under testdata, which `go build ./...` and `go vet ./...` skip, and
// nothing in the server imports it. run.sh drives it:
//
//	flow proxy      serves /admin from the real server with driver.js put
//	                ahead of the page's own script, and passes every other
//	                request to the real server untouched. The driver stubs
//	                fetch and window.open for plex.tv only.
//	flow devicekey  makes a device key pair, as the app does at sign-in.
//	flow openbox    opens a device's GET /api/v1/accounts/services answer.
//	flow lastseen   dates a linked device's last visit back by -days, in
//	                the throwaway database, as if it had gone unused.
package main

import (
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/stoutsolutions/alexandria-server/internal/vault"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "proxy":
		proxy(os.Args[2:])
	case "devicekey":
		deviceKey(os.Args[2:])
	case "openbox":
		openBox(os.Args[2:])
	case "lastseen":
		lastSeen(os.Args[2:])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: flow proxy|devicekey|openbox|lastseen [flags]")
	os.Exit(2)
}

// lastSeen sets one device's last_seen_at -days in the past, through the
// same SQLite driver the server writes it with (so it reads back as the
// server's own would). The server keeps running: WAL lets a second writer
// in, and busy_timeout waits out its locks.
func lastSeen(args []string) {
	fs := flag.NewFlagSet("lastseen", flag.ExitOnError)
	dbPath := fs.String("db", "", "the test server's database")
	label := fs.String("label", "", "the device's label")
	days := fs.Int("days", 41, "how many days ago")
	_ = fs.Parse(args)
	db, err := sql.Open("sqlite", *dbPath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	at := time.Now().UTC().Add(-time.Duration(*days) * 24 * time.Hour)
	res, err := db.Exec(`UPDATE profile_tokens SET last_seen_at = ? WHERE label = ?`, at, *label)
	if err != nil {
		log.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		log.Fatalf("%d devices labelled %q, want 1", n, *label)
	}
}

var checkpointName = regexp.MustCompile(`^[a-z0-9-]{1,40}$`)

func proxy(args []string) {
	fs := flag.NewFlagSet("proxy", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:4568", "address to serve on")
	upstream := fs.String("upstream", "http://127.0.0.1:4567", "the real server")
	driver := fs.String("driver", "", "driver.js")
	config := fs.String("config", "", "JSON handed to the driver as window.__FLOW__")
	dir := fs.String("dir", "", "where checkpoints and the report are written")
	_ = fs.Parse(args)

	up, err := url.Parse(*upstream)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()

	// The real admin page, its headers (Content-Security-Policy included)
	// as the server sent them, with the driver first in <head>.
	mux.HandleFunc("/admin", func(w http.ResponseWriter, r *http.Request) {
		resp, err := http.Get(*upstream + "/admin")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		page, err := io.ReadAll(resp.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		js, err1 := os.ReadFile(*driver)
		cfg, err2 := os.ReadFile(*config)
		if err1 != nil || err2 != nil {
			http.Error(w, "driver or config unreadable", http.StatusInternalServerError)
			return
		}
		inject := "<head>\n<script>\nwindow.__FLOW__ = " + string(cfg) + ";\n" + string(js) + "\n</script>"
		out := strings.Replace(string(page), "<head>", inject, 1)
		if out == string(page) {
			http.Error(w, "the admin page has no <head>", http.StatusInternalServerError)
			return
		}
		for k, vs := range resp.Header {
			if k != "Content-Length" {
				w.Header()[k] = vs
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.WriteString(w, out)
	})

	// The driver reached a checkpoint: note it, then hold the answer until
	// run.sh has checked the server with curl and says go on.
	mux.HandleFunc("/__flow/checkpoint/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/__flow/checkpoint/")
		if !checkpointName.MatchString(name) {
			http.Error(w, "bad checkpoint", http.StatusBadRequest)
			return
		}
		_ = os.WriteFile(filepath.Join(*dir, "checkpoint-"+name), nil, 0o600)
		for end := time.Now().Add(2 * time.Minute); time.Now().Before(end); {
			if _, err := os.Stat(filepath.Join(*dir, "continue-"+name)); err == nil {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		http.Error(w, "run.sh never said go on", http.StatusGatewayTimeout)
	})

	mux.HandleFunc("/__flow/report", func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := os.WriteFile(filepath.Join(*dir, "report.json"), b, 0o600); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.Handle("/", httputil.NewSingleHostReverseProxy(up))
	log.Fatal(http.ListenAndServe(*listen, mux))
}

// deviceKey writes a new X25519 private key (base64) to -out and prints
// the public half, as the app sends it at sign-in.
func deviceKey(args []string) {
	fs := flag.NewFlagSet("devicekey", flag.ExitOnError)
	out := fs.String("out", "", "file for the private key")
	_ = fs.Parse(args)
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, []byte(base64.StdEncoding.EncodeToString(k.Bytes())), 0o600); err != nil {
		log.Fatal(err)
	}
	fmt.Println(base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()))
}

// openBox reads a GET /accounts/services answer on stdin and prints what
// the box holds, opened with the device's private key.
func openBox(args []string) {
	fs := flag.NewFlagSet("openbox", flag.ExitOnError)
	keyFile := fs.String("key", "", "the device's private key file")
	_ = fs.Parse(args)
	raw, err := os.ReadFile(*keyFile)
	if err != nil {
		log.Fatal(err)
	}
	kb, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		log.Fatal(err)
	}
	priv, err := ecdh.X25519().NewPrivateKey(kb)
	if err != nil {
		log.Fatal(err)
	}
	var answer struct {
		Box *vault.SealedBox `json:"box"`
	}
	if err := json.NewDecoder(os.Stdin).Decode(&answer); err != nil || answer.Box == nil {
		log.Fatalf("no box in the answer (%v)", err)
	}
	plain, err := vault.OpenBox(priv, answer.Box)
	if err != nil {
		log.Fatal(err)
	}
	_, _ = os.Stdout.Write(plain)
}
