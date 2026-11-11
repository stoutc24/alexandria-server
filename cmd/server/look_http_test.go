package main

// A person's look (owner, 2026-11-11): their profile color, accent, page
// background, Now Playing background, font and text size, set by their own
// devices and read by every device linked to them, over the real routes.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

const lookPath = "/api/v1/devices/me/look"

// lookOf is the look and its time in a reply (GET /devices/me, a PUT's
// answer, or one person of the people list).
func lookOf(t *testing.T, body map[string]any) (map[string]any, float64) {
	t.Helper()
	look, ok := body["look"].(map[string]any)
	if !ok {
		t.Fatalf("no look object in %v", body)
	}
	at, ok := body["lookUpdatedAt"].(float64)
	if !ok {
		t.Fatalf("no lookUpdatedAt number in %v", body)
	}
	return look, at
}

// meLook is the look a device reads on GET /devices/me.
func (s *testServer) meLook(dev map[string]string) (map[string]any, float64) {
	s.t.Helper()
	return lookOf(s.t, s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, dev).body)
}

// putLookRaw sends a body exactly as given.
func (s *testServer) putLookRaw(raw string, dev map[string]string) response {
	s.t.Helper()
	r, err := s.sendRaw("PUT", lookPath, "application/json", raw, dev)
	if err != nil {
		s.t.Fatal(err)
	}
	return r
}

// personLook is one person's look on the admin's people list.
func (s *testServer) personLook(name string) (map[string]any, float64) {
	s.t.Helper()
	people := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, bearer(testAPIToken))
	for _, p := range people.body["profiles"].([]any) {
		m := p.(map[string]any)
		if m["name"] == name {
			return lookOf(s.t, m)
		}
	}
	s.t.Fatalf("%s is not on the people list: %v", name, people)
	return nil, 0
}

func wantLook(t *testing.T, where string, got map[string]any, want map[string]string) {
	t.Helper()
	w := map[string]any{}
	for k, v := range want {
		w[k] = v
	}
	if !reflect.DeepEqual(got, w) {
		t.Fatalf("%s: look %v, want %v", where, got, w)
	}
}

func TestALookIsKeptPerPersonAndReadByEveryDeviceOfTheirs(t *testing.T) {
	s := newTestServer(t)
	if !hasCapability(t, s, "profiles.look") {
		t.Fatal("/health does not list profiles.look")
	}
	katePhone := s.linkedDevice("Kate", "Kate's iPhone")
	kateTV := bearer(s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Kate's TV"}, katePhone).str("token"))
	chrisPhone := s.linkedDevice("Chris", "Chris's iPhone")

	// Nothing set: an empty object, never null or missing, and time 0.
	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, katePhone)
	if !strings.Contains(string(me.raw), `"look":{}`) || !strings.Contains(string(me.raw), `"lookUpdatedAt":0`) {
		t.Fatalf("before any look: %s", me.raw)
	}

	// Set from the phone: the answer is the whole look, and only that.
	r := s.expectStatus(http.StatusOK, "PUT", lookPath,
		map[string]string{"color": "teal", "ambiance": "rain"}, katePhone)
	if got := keysOf(r.body); !reflect.DeepEqual(got, []string{"look", "lookUpdatedAt"}) {
		t.Fatalf("the PUT answered keys %v: %v", got, r)
	}
	look, first := lookOf(t, r.body)
	wantLook(t, "after the first PUT", look, map[string]string{"color": "teal", "ambiance": "rain"})
	if first <= 0 {
		t.Fatalf("lookUpdatedAt %v after a change", first)
	}

	// Her TV reads the same; Chris's phone reads nothing of hers.
	look, at := s.meLook(kateTV)
	wantLook(t, "Kate's TV", look, map[string]string{"color": "teal", "ambiance": "rain"})
	if at != first {
		t.Fatalf("Kate's TV reads lookUpdatedAt %v, the PUT said %v", at, first)
	}
	look, at = s.meLook(chrisPhone)
	wantLook(t, "Chris's phone", look, nil)
	if at != 0 {
		t.Fatalf("Chris's phone reads lookUpdatedAt %v", at)
	}

	// Merged: a field from the TV joins the two from the phone.
	r = s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"font": "serif"}, kateTV)
	look, second := lookOf(t, r.body)
	wantLook(t, "after the TV's PUT", look,
		map[string]string{"color": "teal", "ambiance": "rain", "font": "serif"})
	if second <= first {
		t.Fatalf("lookUpdatedAt went %v → %v on a change", first, second)
	}

	// "" clears one field back to the default and keeps the rest.
	r = s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"ambiance": ""}, katePhone)
	look, third := lookOf(t, r.body)
	wantLook(t, "after clearing ambiance", look, map[string]string{"color": "teal", "font": "serif"})
	if third <= second {
		t.Fatalf("lookUpdatedAt went %v → %v on a clear", second, third)
	}
	if !strings.Contains(string(r.raw), `"look":{"color":"teal","font":"serif"}`) {
		t.Fatalf("a cleared field is still in the JSON: %s", r.raw)
	}

	// A repeat, or nothing at all, changes nothing, the time included.
	for _, body := range []map[string]string{{"font": "serif"}, {}} {
		r = s.expectStatus(http.StatusOK, "PUT", lookPath, body, kateTV)
		look, at = lookOf(t, r.body)
		wantLook(t, fmt.Sprintf("after the repeat %v", body), look, map[string]string{"color": "teal", "font": "serif"})
		if at != third {
			t.Fatalf("a PUT of %v moved lookUpdatedAt %v → %v", body, third, at)
		}
	}

	// Every value on every list is taken, and read back as sent.
	for _, field := range repository.LookFieldNames() {
		for _, v := range repository.LookValues(field) {
			r = s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{field: v}, katePhone)
			if look, _ := lookOf(t, r.body); look[field] != v {
				t.Fatalf("%s %q read back as %v", field, v, look[field])
			}
		}
	}
	all := map[string]string{"color": "slate", "accent": "default", "background": "ocean",
		"ambiance": "starfield", "font": "rounded", "textSize": "extraLarge"}
	r = s.expectStatus(http.StatusOK, "PUT", lookPath, all, katePhone)
	look, _ = lookOf(t, r.body)
	wantLook(t, "all six", look, all)
	look, _ = s.meLook(kateTV)
	wantLook(t, "all six on the TV", look, all)

	// And all six cleared at once: back to {}.
	none := map[string]string{}
	for k := range all {
		none[k] = ""
	}
	r = s.expectStatus(http.StatusOK, "PUT", lookPath, none, kateTV)
	if !strings.Contains(string(r.raw), `"look":{}`) {
		t.Fatalf("all cleared: %s", r.raw)
	}
	look, _ = s.meLook(katePhone)
	wantLook(t, "all cleared, on the phone", look, nil)
}

// Anything off the lists is refused with a 400 that names the field, and
// nothing of the body is written — not even its good fields.
func TestALookRefusesAnythingOffTheLists(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"color": "teal"}, kate)
	before, beforeAt := s.meLook(kate)

	for _, c := range []struct {
		name, body string
		says       string // the refusal must contain this
	}{
		{"an unknown key", `{"colour":"red"}`, `"colour" is not a look field`},
		{"an unknown key beside a good one", `{"color":"red","layout":"grid"}`, `"layout" is not a look field`},
		{"a key in the wrong case", `{"Color":"red"}`, `"Color" is not a look field`},
		{"a person named in the body", `{"profileId":"someone-else","color":"red"}`, `"profileId" is not a look field`},
		{"an unknown color", `{"color":"chartreuse"}`, `"color" must be one of`},
		{"default as a profile color", `{"color":"default"}`, `"color" must be one of`},
		{"a color in the wrong case", `{"accent":"RED"}`, `"accent" must be one of`},
		{"an unknown background", `{"background":"white"}`, `"background" must be one of`},
		{"an unknown ambiance", `{"ambiance":"lava"}`, `"ambiance" must be one of`},
		{"an unknown font", `{"font":"comic"}`, `"font" must be one of`},
		{"an unknown text size", `{"textSize":"huge"}`, `"textSize" must be one of`},
		{"a value with spaces", `{"textSize":" large "}`, `"textSize" must be one of`},
		{"a number", `{"color":1}`, `"color" must be a string`},
		{"a boolean", `{"font":true}`, `"font" must be a string`},
		{"null", `{"color":null}`, `"color" must be a string`},
		{"an array", `{"color":["red"]}`, `"color" must be a string`},
		{"an object", `{"color":{"value":"red"}}`, `"color" must be a string`},
		{"a good field, then a bad one", `{"accent":"blue","font":7}`, `"font" must be a string`},
		{"an array body", `[{"color":"red"}]`, "send a JSON object"},
		{"a string body", `"red"`, "send a JSON object"},
		{"a null body", `null`, "send a JSON object"},
		{"a number body", `42`, "send a JSON object"},
		{"no body", ``, "send a JSON object"},
		{"half an object", `{"color":"red"`, "send a JSON object"},
		{"an object and more", `{"color":"red"} {"color":"blue"}`, "send a JSON object"},
		{"not JSON", `color=red`, "send a JSON object"},
	} {
		r := s.putLookRaw(c.body, kate)
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", c.name, r.status, r)
			continue
		}
		if got := keysOf(r.body); !reflect.DeepEqual(got, []string{"error"}) {
			t.Errorf("%s: the refusal has keys %v, want only error: %v", c.name, got, r)
		}
		if !strings.Contains(r.str("error"), c.says) {
			t.Errorf("%s: refused with %q, want it to say %q", c.name, r.str("error"), c.says)
		}
		look, at := s.meLook(kate)
		if !reflect.DeepEqual(look, before) || at != beforeAt {
			t.Fatalf("%s: the look became %v (%v), want %v (%v) — a refusal wrote", c.name, look, at, before, beforeAt)
		}
	}

	// Nothing hostile is repeated back: not a value, and not a key that is
	// more than a plain short word.
	for name, body := range map[string]string{
		"a script value": `{"color":"<script>alert(1)</script>"}`,
		"a script key":   `{"<script>alert(1)</script>":"red"}`,
		"a long key":     `{"` + strings.Repeat("a", 200) + `":"red"}`,
		"a long value":   `{"font":"` + strings.Repeat("b", 500) + `"}`,
	} {
		r := s.putLookRaw(body, kate)
		if r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %v, want 400", name, r.status, r)
			continue
		}
		raw := string(r.raw)
		if strings.Contains(raw, "script") || strings.Contains(raw, "aaaa") || strings.Contains(raw, "bbbb") ||
			len(raw) > 300 {
			t.Errorf("%s: the refusal repeats the input: %s", name, raw)
		}
	}

	// Too big: over 2 KiB is 413, as every size limit here answers; 2 KiB
	// exactly is taken.
	padded := `{"color":"red"}`
	padded += strings.Repeat(" ", 2048-len(padded))
	r := s.putLookRaw(padded+" ", kate)
	if r.status != http.StatusRequestEntityTooLarge || !strings.Contains(r.str("error"), "2 KB") {
		t.Fatalf("2049 bytes: %d %v, want 413", r.status, r)
	}
	if look, at := s.meLook(kate); !reflect.DeepEqual(look, before) || at != beforeAt {
		t.Fatalf("a body too big wrote: %v", look)
	}
	r = s.putLookRaw(`{"color":"`+strings.Repeat("x", 100*1024)+`"}`, kate)
	if r.status != http.StatusRequestEntityTooLarge || strings.Contains(string(r.raw), "xxxx") {
		t.Fatalf("100 KiB: %d %v, want 413", r.status, r)
	}
	r = s.putLookRaw(padded, kate)
	if r.status != http.StatusOK {
		t.Fatalf("2048 bytes: %d %v, want 200", r.status, r)
	}
	if look, _ := lookOf(t, r.body); look["color"] != "red" {
		t.Fatalf("2048 bytes: %v", r)
	}
}

// A device changes its own person's look and no one else's: there is no
// person in the path, a person named in the body is refused, and one in
// the query is ignored.
func TestADeviceCannotChangeAnotherPersonsLook(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	chris := s.linkedDevice("Chris", "Chris's iPhone")
	chrisMe := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, chris)
	chrisID, chrisDevice := chrisMe.str("profileId"), chrisMe.str("deviceKey")

	// Chris's id in the query: the change is Kate's own.
	r := s.expectStatus(http.StatusOK, "PUT", lookPath+"?profileId="+chrisID,
		map[string]string{"color": "red"}, kate)
	if look, _ := lookOf(t, r.body); look["color"] != "red" {
		t.Fatalf("Kate's own change: %v", r)
	}
	// Chris's id in the body: refused.
	s.expectStatus(http.StatusBadRequest, "PUT", lookPath,
		map[string]string{"profileId": chrisID, "color": "blue"}, kate)
	// No route takes a person or another device: these never answer 200.
	for _, path := range []string{
		"/api/v1/devices/" + chrisDevice + "/look",
		"/api/v1/devices/" + chrisID + "/look",
		"/api/v1/profiles/" + chrisID + "/look",
		"/api/v1/admin/profiles/" + chrisID + "/look",
	} {
		if r := s.do("PUT", path, map[string]string{"color": "blue"}, kate); r.status == http.StatusOK {
			t.Errorf("PUT %s answered 200: %v", path, r)
		}
	}

	look, at := s.meLook(chris)
	wantLook(t, "Chris's phone", look, nil)
	if at != 0 {
		t.Fatalf("Chris's lookUpdatedAt moved to %v", at)
	}
	look, _ = s.personLook("Chris")
	wantLook(t, "Chris on the people list", look, nil)
	look, _ = s.meLook(kate)
	wantLook(t, "Kate's phone", look, map[string]string{"color": "red"})
}

// A device signed out, or anyone without a device token, gets what every
// device route gives: 401, token-revoked for a token that was ours.
func TestALookNeedsALiveDeviceToken(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	kateTV := bearer(s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Kate's TV"}, kate).str("token"))
	s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"color": "teal"}, kate)
	key := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kate).str("deviceKey")

	e := enrollDirect(t)
	phone := s.signIn(e, true, nil)
	login := s.expectStatus(http.StatusOK, "POST", loginPath, map[string]string{
		"password": testPassword, "code": freshCode(t, e.secret)}, nil)
	for name, hdr := range map[string]map[string]string{
		"environment token": bearer(testAPIToken),
		"phone admin token": bearer(phone.token),
		"browser session":   {"Cookie": "alx_admin=" + findCookie(login.resp, "alx_admin").Value},
		"a made-up token":   bearer("alxp_" + strings.Repeat("0", 64)),
		"nothing":           nil,
	} {
		if r := s.do("PUT", lookPath, map[string]string{"color": "red"}, hdr); r.status != http.StatusUnauthorized {
			t.Errorf("%s: %d %v, want 401", name, r.status, r)
		}
	}

	s.expectStatus(http.StatusOK, "DELETE", "/api/v1/admin/devices/"+key, nil, bearer(phone.token))
	r := s.do("PUT", lookPath, map[string]string{"color": "red"}, kate)
	if r.status != http.StatusUnauthorized || r.str("code") != "token-revoked" {
		t.Fatalf("a revoked device's PUT: %d %v, want 401 token-revoked", r.status, r)
	}
	// Exactly what the other device routes answer it.
	other := s.do("GET", "/api/v1/devices/me", nil, kate)
	if other.status != r.status || other.str("code") != r.str("code") || other.str("error") != r.str("error") {
		t.Fatalf("PUT look answered %v, GET devices/me %v", r, other)
	}
	look, _ := s.meLook(kateTV)
	wantLook(t, "after the revoked PUT", look, map[string]string{"color": "teal"})
}

// A read of the look that fails is not an answer: 503, as for Kids mode,
// never an empty look standing in for one.
func TestDevicesMeDoesNotMakeUpALookWhenItCouldNotRead(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"color": "teal"}, kate)

	if _, err := database.DB.Exec(`ALTER TABLE profiles RENAME COLUMN look TO look_gone`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = database.DB.Exec(`ALTER TABLE profiles RENAME COLUMN look_gone TO look`)
	})
	r := s.do("GET", "/api/v1/devices/me", nil, kate)
	if r.status != http.StatusServiceUnavailable {
		t.Fatalf("a failed read answered %d %v, want 503", r.status, r)
	}
	for _, k := range []string{"look", "lookUpdatedAt", "kids"} {
		if _, said := r.body[k]; said {
			t.Fatalf("a failed read still said %s = %v", k, r.body[k])
		}
	}
	if r := s.do("PUT", lookPath, map[string]string{"color": "red"}, kate); r.status != http.StatusServiceUnavailable {
		t.Fatalf("a PUT that could not read answered %d %v, want 503", r.status, r)
	}
}

// What is stored is only ever read back through the lists: a value a newer
// server knew is left out, and text that is not a look reads as none —
// it fails no read, and the next change replaces it.
func TestAStoredLookIsReadBackOnlyThroughTheLists(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	kateID := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kate).str("profileId")
	set := func(raw string) {
		t.Helper()
		if _, err := database.DB.Exec(`UPDATE profiles SET look = ?, look_updated_at = 5 WHERE id = ?`, raw, kateID); err != nil {
			t.Fatal(err)
		}
	}

	set(`{"color":"mint","font":"serif","layout":"grid","accent":"<b>x</b>"}`)
	look, at := s.meLook(kate)
	wantLook(t, "values off the lists", look, map[string]string{"font": "serif"})
	if at != 5 {
		t.Fatalf("lookUpdatedAt %v, want 5", at)
	}
	look, _ = s.personLook("Kate")
	wantLook(t, "values off the lists, on the people list", look, map[string]string{"font": "serif"})

	set(`not a look at all`)
	look, _ = s.meLook(kate)
	wantLook(t, "text that is not a look", look, nil)
	s.personLook("Kate")
	r := s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"color": "red"}, kate)
	look, at = lookOf(t, r.body)
	wantLook(t, "the next change", look, map[string]string{"color": "red"})
	if at <= 5 {
		t.Fatalf("lookUpdatedAt %v after the next change", at)
	}
	var stored string
	if err := database.DB.QueryRow(`SELECT look FROM profiles WHERE id = ?`, kateID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `{"color":"red"}` {
		t.Fatalf("stored %q", stored)
	}
}

// Two devices of one person changing different fields at the same moment:
// both changes land.
func TestLookChangesAtTheSameMomentAllLand(t *testing.T) {
	s := newTestServer(t)
	phone := s.linkedDevice("Kate", "Kate's iPhone")
	tv := bearer(s.expectStatus(http.StatusCreated, "POST", "/api/v1/devices",
		map[string]string{"label": "Kate's TV"}, phone).str("token"))
	devices := []map[string]string{phone, tv}

	for round, values := range []map[string]string{
		{"color": "red", "accent": "blue", "background": "plum", "ambiance": "rain", "font": "serif", "textSize": "large"},
		{"color": "gold", "accent": "default", "background": "forest", "ambiance": "fireplace", "font": "rounded", "textSize": "normal"},
	} {
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make(chan error, len(values))
		i := 0
		for field, v := range values {
			wg.Add(1)
			go func(dev map[string]string, field, v string) {
				defer wg.Done()
				<-start
				r, err := s.send("PUT", lookPath, map[string]string{field: v}, dev)
				if err == nil && r.status != http.StatusOK {
					err = fmt.Errorf("%s: %d %v", field, r.status, r)
				}
				if err != nil {
					errs <- err
				}
			}(devices[i%2], field, v)
			i++
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		look, _ := s.meLook(phone)
		wantLook(t, fmt.Sprintf("round %d", round+1), look, values)
	}
}

// An existing database gains the look columns with their defaults, and
// keeps every person and device it had; a second start changes nothing.
func TestAnExistingDatabaseGainsTheLookAndKeepsItsPeople(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	s.linkedDevice("Chris", "Chris's iPhone")
	kateID := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kate).str("profileId")
	s.expectStatus(http.StatusOK, "PUT", "/api/v1/admin/profiles/"+kateID+"/kids",
		map[string]bool{"kids": true}, bearer(testAPIToken))

	type row struct {
		ID, Name, CreatedAt string
		Kids                bool
	}
	rows := func() []row {
		t.Helper()
		q, err := database.DB.Query(`SELECT id, name, CAST(created_at AS TEXT), kids FROM profiles ORDER BY name`)
		if err != nil {
			t.Fatal(err)
		}
		defer q.Close()
		var out []row
		for q.Next() {
			var r row
			if err := q.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.Kids); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
		return out
	}
	type column struct {
		Type, Default string
		NotNull       bool
	}
	columns := func() map[string]column {
		t.Helper()
		q, err := database.DB.Query(`SELECT name, type, "notnull", COALESCE(dflt_value, '') FROM pragma_table_info('profiles')`)
		if err != nil {
			t.Fatal(err)
		}
		defer q.Close()
		out := map[string]column{}
		for q.Next() {
			var name string
			var c column
			if err := q.Scan(&name, &c.Type, &c.NotNull, &c.Default); err != nil {
				t.Fatal(err)
			}
			out[name] = c
		}
		return out
	}

	// A database from before the look: take the columns away, as the old
	// code never had them.
	for _, col := range []string{"look", "look_updated_at"} {
		if _, err := database.DB.Exec(`ALTER TABLE profiles DROP COLUMN ` + col); err != nil {
			t.Fatal(err)
		}
	}
	before := rows()
	if len(before) != 2 {
		t.Fatalf("people before the update: %v", before)
	}
	if _, has := columns()["look"]; has {
		t.Fatal("the look column is still there")
	}

	if err := database.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	cols := columns()
	if c := cols["look"]; c.Type != "TEXT" || !c.NotNull || c.Default != "'{}'" {
		t.Fatalf("look column: %+v", c)
	}
	if c := cols["look_updated_at"]; c.Type != "INTEGER" || !c.NotNull || c.Default != "0" {
		t.Fatalf("look_updated_at column: %+v", c)
	}
	if after := rows(); !reflect.DeepEqual(after, before) {
		t.Fatalf("people after the update %v, before %v", after, before)
	}
	var n int
	if err := database.DB.QueryRow(`SELECT COUNT(*) FROM profiles WHERE look = '{}' AND look_updated_at = 0`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d of 2 people have the default look", n)
	}

	// The device still works, still in Kids mode, with no look yet.
	me := s.expectStatus(http.StatusOK, "GET", "/api/v1/devices/me", nil, kate)
	if me.body["kids"] != true || !strings.Contains(string(me.raw), `"look":{}`) {
		t.Fatalf("after the update: %s", me.raw)
	}

	// A look set after the update survives the next start.
	s.expectStatus(http.StatusOK, "PUT", lookPath, map[string]string{"color": "pink"}, kate)
	if err := database.RunMigrations(); err != nil {
		t.Fatal(err)
	}
	look, _ := s.meLook(kate)
	wantLook(t, "after a second start", look, map[string]string{"color": "pink"})
}

// The admin's people list carries each person's look, read-only: the look
// as their devices set it, and nothing outside the lists.
func TestThePeopleListCarriesEachPersonsLook(t *testing.T) {
	s := newTestServer(t)
	kate := s.linkedDevice("Kate", "Kate's iPhone")
	s.linkedDevice("Chris", "Chris's iPhone")
	r := s.expectStatus(http.StatusOK, "PUT", lookPath,
		map[string]string{"color": "purple", "textSize": "large"}, kate)
	_, kateAt := lookOf(t, r.body)

	look, at := s.personLook("Kate")
	wantLook(t, "Kate on the people list", look, map[string]string{"color": "purple", "textSize": "large"})
	if at != kateAt {
		t.Fatalf("Kate's lookUpdatedAt on the list %v, the PUT said %v", at, kateAt)
	}
	look, at = s.personLook("Chris")
	wantLook(t, "Chris on the people list", look, nil)
	if at != 0 {
		t.Fatalf("Chris's lookUpdatedAt on the list: %v", at)
	}

	// The list's JSON for a person: the look is an object of strings.
	people := s.expectStatus(http.StatusOK, "GET", "/api/v1/admin/profiles", nil, bearer(testAPIToken))
	var decoded struct {
		Profiles []struct {
			Name string            `json:"name"`
			Look map[string]string `json:"look"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(people.raw, &decoded); err != nil {
		t.Fatalf("the people list's look is not an object of strings: %v", err)
	}
	if len(decoded.Profiles) != 2 {
		t.Fatalf("people: %s", people.raw)
	}
}
