package showcase

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type watchedReadout struct {
	Requests []watchedRequest `json:"requests"`
	Marked   map[string]bool  `json:"marked"`
	Refuse   bool             `json:"refuse"`
	Legacy   bool             `json:"legacy"`
	Ms       int64            `json:"ms"`
}

func watchedReadoutOf(t *testing.T, srv *httptest.Server) watchedReadout {
	t.Helper()
	_, data := jfGet(t, srv, "/__showcase/watched")
	var out watchedReadout
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("watched readout: %v", err)
	}
	return out
}

func watchedSend(t *testing.T, srv *httptest.Server, method, path string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, data
}

// plexItems reads a MediaContainer's Metadata.
func plexItems(t *testing.T, srv *httptest.Server, path string) []map[string]any {
	t.Helper()
	code, data := jfGet(t, srv, path)
	if code != http.StatusOK {
		t.Fatalf("%s answered %d", path, code)
	}
	var body struct {
		MediaContainer struct {
			Metadata []map[string]any
		}
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return body.MediaContainer.Metadata
}

func plexItem(t *testing.T, srv *httptest.Server, key string) map[string]any {
	t.Helper()
	items := plexItems(t, srv, "/library/metadata/"+key)
	if len(items) != 1 {
		t.Fatalf("/library/metadata/%s: %d items", key, len(items))
	}
	return items[0]
}

func hasKey(items []map[string]any, key string) bool {
	for _, item := range items {
		if item["ratingKey"] == key {
			return true
		}
	}
	return false
}

func num(v any) int {
	f, _ := v.(float64)
	return int(f)
}

// Plex: /:/scrobble and /:/unscrobble keep the mark, and the item, its
// season's and show's counts and On Deck all read it next.
func TestPlexWatchedMarks(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(0)
	srv := httptest.NewServer(New(cat, "").PlexHandler())
	defer srv.Close()
	scrobble := func(route, key string) int {
		code, body := jfGet(t, srv, "/:/"+route+"?identifier=com.plexapp.plugins.library&key="+key)
		if len(body) != 0 {
			t.Errorf("/:/%s answered a body of %d bytes, want none", route, len(body))
		}
		return code
	}

	var fresh, started *Movie
	for _, m := range cat.Movies {
		if m.Progress == 0 && fresh == nil {
			fresh = m
		}
		if m.Progress > 0 && m.Progress < 1 && started == nil {
			started = m
		}
	}
	var show *Show
	for _, sh := range cat.Shows {
		if sh.NextUp != nil && sh.NextUp.Progress > 0 {
			show = sh
			break
		}
	}
	if fresh == nil || started == nil || show == nil {
		t.Fatalf("the catalog lacks an unwatched film, a started film or a show part-way through")
	}

	// --- A film nobody started: watched, then unwatched.
	freshKey := cat.PlexKey(fresh.ID)
	if item := plexItem(t, srv, freshKey); item["viewCount"] != nil || item["viewOffset"] != nil {
		t.Fatalf("%s before: %v / %v", fresh.Title, item["viewCount"], item["viewOffset"])
	}
	if code := scrobble("scrobble", freshKey); code != http.StatusOK {
		t.Fatalf("scrobble answered %d", code)
	}
	if item := plexItem(t, srv, freshKey); num(item["viewCount"]) != 1 || item["viewOffset"] != nil {
		t.Errorf("%s after scrobble: viewCount %v, viewOffset %v", fresh.Title, item["viewCount"], item["viewOffset"])
	}
	listed := false
	for _, item := range plexItems(t, srv, "/library/sections/"+plexMovies+"/all?type=1") {
		if item["ratingKey"] == freshKey {
			listed = num(item["viewCount"]) == 1
		}
	}
	if !listed {
		t.Errorf("the library list does not show %s watched", fresh.Title)
	}
	if code := scrobble("unscrobble", freshKey); code != http.StatusOK {
		t.Fatalf("unscrobble answered %d", code)
	}
	if item := plexItem(t, srv, freshKey); item["viewCount"] != nil || item["viewOffset"] != nil {
		t.Errorf("%s after unscrobble: %v / %v", fresh.Title, item["viewCount"], item["viewOffset"])
	}

	// --- A film part-way through leaves Continue Watching when marked.
	startedKey := cat.PlexKey(started.ID)
	if !hasKey(plexItems(t, srv, "/library/onDeck"), startedKey) {
		t.Fatalf("%s is not on deck to begin with", started.Title)
	}
	scrobble("scrobble", startedKey)
	if hasKey(plexItems(t, srv, "/library/onDeck"), startedKey) {
		t.Errorf("%s is still on deck after it was marked watched", started.Title)
	}
	if item := plexItem(t, srv, startedKey); num(item["viewCount"]) != 1 || item["viewOffset"] != nil {
		t.Errorf("%s: viewCount %v, viewOffset %v", started.Title, item["viewCount"], item["viewOffset"])
	}

	// --- An episode part-way through: marked watched, the next one is up
	// and the season's and show's counts move by one.
	episode := show.NextUp
	episodes := show.AllEpisodes()
	var following *Episode
	for i, e := range episodes {
		if e == episode && i+1 < len(episodes) {
			following = episodes[i+1]
		}
	}
	if following == nil {
		t.Fatalf("%s has no episode after its next-up", show.Title)
	}
	showKey, seasonKey := cat.PlexKey(show.ID), cat.PlexKey(episode.Season.ID)
	episodeKey, followingKey := cat.PlexKey(episode.ID), cat.PlexKey(following.ID)
	watchedBefore, total := show.Counts()
	seasonBefore := num(plexItem(t, srv, seasonKey)["viewedLeafCount"])
	if !hasKey(plexItems(t, srv, "/library/onDeck"), episodeKey) {
		t.Fatalf("the show's next-up episode is not on deck to begin with")
	}
	scrobble("scrobble", episodeKey)
	deck := plexItems(t, srv, "/library/onDeck")
	if hasKey(deck, episodeKey) || !hasKey(deck, followingKey) {
		t.Errorf("on deck after the episode was marked: has it %v, has the next %v", hasKey(deck, episodeKey), hasKey(deck, followingKey))
	}
	if got := num(plexItem(t, srv, showKey)["viewedLeafCount"]); got != watchedBefore+1 {
		t.Errorf("the show's viewedLeafCount is %d, want %d", got, watchedBefore+1)
	}
	if got := num(plexItem(t, srv, seasonKey)["viewedLeafCount"]); got != seasonBefore+1 {
		t.Errorf("the season's viewedLeafCount is %d, want %d", got, seasonBefore+1)
	}

	// --- A season: every episode under it.
	scrobble("scrobble", seasonKey)
	season := plexItem(t, srv, seasonKey)
	if num(season["viewedLeafCount"]) != num(season["leafCount"]) || num(season["leafCount"]) != len(episode.Season.Episodes) {
		t.Errorf("the season after scrobble: %v of %v", season["viewedLeafCount"], season["leafCount"])
	}
	for _, item := range plexItems(t, srv, "/library/metadata/"+seasonKey+"/children") {
		if num(item["viewCount"]) != 1 || item["viewOffset"] != nil {
			t.Errorf("episode %v of the marked season: viewCount %v, viewOffset %v", item["index"], item["viewCount"], item["viewOffset"])
		}
	}

	// --- The whole show, unwatched: no episode watched, off the deck.
	scrobble("unscrobble", showKey)
	if got := plexItem(t, srv, showKey); num(got["viewedLeafCount"]) != 0 || num(got["leafCount"]) != total {
		t.Errorf("the show after unscrobble: %v of %v, want 0 of %d", got["viewedLeafCount"], got["leafCount"], total)
	}
	for _, item := range plexItems(t, srv, "/library/metadata/"+showKey+"/allLeaves") {
		if item["viewCount"] != nil || item["viewOffset"] != nil {
			t.Errorf("an episode of the unwatched show still reads %v / %v", item["viewCount"], item["viewOffset"])
		}
	}
	for _, item := range plexItems(t, srv, "/library/onDeck") {
		if item["grandparentRatingKey"] == showKey {
			t.Errorf("an episode of the unwatched show is on deck")
		}
	}

	// --- A key that names nothing: 200 as before, nothing marked.
	if code := scrobble("scrobble", "99999999"); code != http.StatusOK {
		t.Errorf("an unknown key answered %d, want 200 as before", code)
	}

	// --- The readout: each request as it came.
	got := watchedReadoutOf(t, srv)
	if len(got.Requests) != 7 {
		t.Fatalf("%d requests in the readout, want 7: %+v", len(got.Requests), got.Requests)
	}
	first := watchedRequest{Method: "GET", Path: "/:/scrobble", Query: "identifier=com.plexapp.plugins.library&key=" + freshKey,
		Item: fresh.ID, Kind: "movie", Watched: true, Status: 200}
	if got.Requests[0] != first {
		t.Errorf("the first request reads %+v, want %+v", got.Requests[0], first)
	}
	if r := got.Requests[1]; r.Path != "/:/unscrobble" || r.Watched || r.Item != fresh.ID {
		t.Errorf("the second request reads %+v", r)
	}
	if r := got.Requests[4]; r.Kind != "season" || !r.Watched {
		t.Errorf("the season's request reads %+v", r)
	}
	if r := got.Requests[5]; r.Kind != "show" || r.Watched {
		t.Errorf("the show's request reads %+v", r)
	}
	if r := got.Requests[6]; r.Item != "" || r.Kind != "" || r.Status != 200 {
		t.Errorf("the unknown key's request reads %+v", r)
	}
	if got.Marked[fresh.ID] || !got.Marked[started.ID] {
		t.Errorf("marked: %v", got.Marked)
	}

	// --- A server that refuses: 500, and nothing changes.
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?refuse=1")
	if code := scrobble("scrobble", freshKey); code != http.StatusInternalServerError {
		t.Errorf("a refusing server answered %d, want 500", code)
	}
	if item := plexItem(t, srv, freshKey); item["viewCount"] != nil {
		t.Errorf("a refused mark was kept")
	}
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?refuse=0")

	// --- A slow server: the answer, and the mark with it, take that long.
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?ms=120")
	if got := watchedReadoutOf(t, srv); got.Ms != 120 || got.Refuse {
		t.Errorf("readout after ms=120: %+v", got)
	}
	began := time.Now()
	if code := scrobble("scrobble", freshKey); code != http.StatusOK || time.Since(began) < 120*time.Millisecond {
		t.Errorf("a slow server answered %d after %s, want 200 after 120 ms", code, time.Since(began))
	}
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?ms=0")

	// --- Forgotten: everything reads as the catalog has it again.
	watchedSend(t, srv, http.MethodDelete, "/__showcase/watched")
	if got := watchedReadoutOf(t, srv); len(got.Requests) != 0 || len(got.Marked) != 0 || got.Refuse {
		t.Errorf("readout not cleared: %+v", got)
	}
	if !hasKey(plexItems(t, srv, "/library/onDeck"), startedKey) || !hasKey(plexItems(t, srv, "/library/onDeck"), episodeKey) {
		t.Errorf("on deck is not the catalog's again once the marks are forgotten")
	}
	if got := num(plexItem(t, srv, showKey)["viewedLeafCount"]); got != watchedBefore {
		t.Errorf("the show's viewedLeafCount is %d once the marks are forgotten, want %d", got, watchedBefore)
	}
	// The routes beside them are untouched.
	if code, _ := jfGet(t, srv, "/:/timeline"); code != http.StatusOK {
		t.Errorf("/:/timeline answered %d", code)
	}
}

// Until a mark is made, a Server that has the stand-in answers exactly what
// it always did: every list a mark could change, byte for byte, against a
// Server whose marks were made and forgotten.
func TestWatchedMarksChangeNothingUntilMade(t *testing.T) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	plexCat, jfCat := Build(now, nil).Half(0), Build(now, nil).Half(1)
	plex := httptest.NewServer(New(plexCat, "").PlexHandler())
	defer plex.Close()
	jf := httptest.NewServer(New(jfCat, "").JellyfinHandler())
	defer jf.Close()

	plexPaths := []string{"/library/onDeck", "/library/sections/" + plexMovies + "/all?type=1", "/library/sections/" + plexShows + "/all?type=2",
		"/library/metadata/" + plexCat.PlexKey(plexCat.Shows[0].ID) + "/children", "/library/metadata/" + plexCat.PlexKey(plexCat.Shows[0].ID) + "/allLeaves"}
	jfPaths := []string{"/Users/" + jfUserID + "/Items/Resume?MediaTypes=Video", "/Shows/NextUp?UserId=" + jfUserID,
		"/Users/" + jfUserID + "/Items?ParentId=" + jfMovieView + "&IncludeItemTypes=Movie&Recursive=true",
		"/Shows/" + jfCat.JFID(jfCat.Shows[0].ID) + "/Seasons?UserId=" + jfUserID,
		"/Shows/" + jfCat.JFID(jfCat.Shows[0].ID) + "/Episodes?UserId=" + jfUserID}
	read := func(srv *httptest.Server, paths []string) []string {
		var out []string
		for _, path := range paths {
			code, data := jfGet(t, srv, path)
			if code != http.StatusOK {
				t.Fatalf("%s answered %d", path, code)
			}
			out = append(out, string(data))
		}
		return out
	}
	plexBefore, jfBefore := read(plex, plexPaths), read(jf, jfPaths)

	// A mark on each, which changes those lists …
	jfGet(t, plex, "/:/scrobble?key="+plexCat.PlexKey(plexCat.Shows[0].ID))
	watchedSend(t, jf, http.MethodPost, "/UserPlayedItems/"+jfCat.JFID(jfCat.Shows[0].ID)+"?userId="+jfUserID)
	if plexMarked, jfMarked := read(plex, plexPaths), read(jf, jfPaths); plexMarked[3] == plexBefore[3] || jfMarked[3] == jfBefore[3] {
		t.Fatalf("the marks changed nothing (the test proves nothing)")
	}
	// … and forgotten: the same bytes as before any mark.
	watchedSend(t, plex, http.MethodDelete, "/__showcase/watched")
	watchedSend(t, jf, http.MethodDelete, "/__showcase/watched")
	for i, got := range read(plex, plexPaths) {
		if got != plexBefore[i] {
			t.Errorf("Plex %s differs once the marks are forgotten", plexPaths[i])
		}
	}
	for i, got := range read(jf, jfPaths) {
		if got != jfBefore[i] {
			t.Errorf("Jellyfin %s differs once the marks are forgotten", jfPaths[i])
		}
	}
}

func jfUserDataOf(t *testing.T, srv *httptest.Server, id string) map[string]any {
	t.Helper()
	code, data := jfGet(t, srv, "/Users/"+jfUserID+"/Items/"+id)
	if code != http.StatusOK {
		t.Fatalf("item %s answered %d", id, code)
	}
	var item struct {
		UserData map[string]any
	}
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatalf("item %s: %v", id, err)
	}
	return item.UserData
}

// Jellyfin: POST / DELETE on /UserPlayedItems (10.9+) and on the legacy
// /Users/{uid}/PlayedItems keep the mark for a film, an episode, a season
// or a series; anything else on those routes is answered as it always was.
func TestJellyfinWatchedMarks(t *testing.T) {
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	srv := httptest.NewServer(New(cat, "").JellyfinHandler())
	defer srv.Close()

	var fresh *Movie
	for _, m := range cat.Movies {
		if m.Progress == 0 {
			fresh = m
			break
		}
	}
	var show *Show
	for _, sh := range cat.Shows {
		if sh.NextUp != nil {
			show = sh
			break
		}
	}
	if fresh == nil || show == nil {
		t.Fatalf("the catalog lacks an unwatched film or a show somebody started")
	}
	movieID, showID := cat.JFID(fresh.ID), cat.JFID(show.ID)
	modern := "/UserPlayedItems/" + movieID + "?userId=" + jfUserID
	legacy := "/Users/" + jfUserID + "/PlayedItems/" + movieID

	// --- The 10.9+ route: played, then unplayed.
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != false {
		t.Fatalf("%s before: %v", fresh.Title, ud)
	}
	code, data := watchedSend(t, srv, http.MethodPost, modern)
	var echoed map[string]any
	_ = json.Unmarshal(data, &echoed)
	if code != http.StatusOK || echoed["Played"] != true {
		t.Fatalf("POST %s: %d %s", modern, code, string(data))
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != true || num(ud["PlaybackPositionTicks"]) != 0 {
		t.Errorf("%s after POST: %v", fresh.Title, ud)
	}
	if code, _ := watchedSend(t, srv, http.MethodDelete, modern); code != http.StatusOK {
		t.Fatalf("DELETE %s: %d", modern, code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != false {
		t.Errorf("%s after DELETE: %v", fresh.Title, ud)
	}

	// --- A server before 10.9: the new route is a 404 and marks nothing;
	// the legacy one works both ways.
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?legacy=1")
	if code, _ := watchedSend(t, srv, http.MethodPost, modern); code != http.StatusNotFound {
		t.Errorf("the new route on a legacy server answered %d, want 404", code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != false {
		t.Errorf("a 404 marked the film")
	}
	if code, _ := watchedSend(t, srv, http.MethodPost, legacy); code != http.StatusOK {
		t.Errorf("the legacy POST answered %d", code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != true {
		t.Errorf("the legacy POST did not mark the film: %v", ud)
	}
	if code, _ := watchedSend(t, srv, http.MethodDelete, legacy); code != http.StatusOK {
		t.Errorf("the legacy DELETE answered %d", code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != false {
		t.Errorf("the legacy DELETE did not unmark the film: %v", ud)
	}
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?legacy=0")

	// --- A whole series: every episode, the counts, and Next Up.
	_, total := show.Counts()
	if code, _ := watchedSend(t, srv, http.MethodPost, "/UserPlayedItems/"+showID+"?userId="+jfUserID); code != http.StatusOK {
		t.Fatalf("POST for the series answered %d", code)
	}
	if ud := jfUserDataOf(t, srv, showID); num(ud["UnplayedItemCount"]) != 0 || ud["Played"] != true {
		t.Errorf("the series after POST: %v", ud)
	}
	_, data = jfGet(t, srv, "/Shows/"+showID+"/Episodes?UserId="+jfUserID)
	episodes := jfItems(t, data)
	if len(episodes) != total {
		t.Fatalf("%d episodes listed, want %d", len(episodes), total)
	}
	for _, e := range episodes {
		if ud, _ := e["UserData"].(map[string]any); ud["Played"] != true {
			t.Errorf("episode %v of the marked series: %v", e["Name"], ud)
		}
	}
	_, data = jfGet(t, srv, "/Shows/"+showID+"/Seasons?UserId="+jfUserID)
	for _, se := range jfItems(t, data) {
		if ud, _ := se["UserData"].(map[string]any); num(ud["UnplayedItemCount"]) != 0 {
			t.Errorf("season %v of the marked series: %v", se["Name"], ud)
		}
	}
	_, data = jfGet(t, srv, "/Shows/NextUp?UserId="+jfUserID)
	for _, e := range jfItems(t, data) {
		if e["SeriesId"] == showID {
			t.Errorf("a series watched to the end is still on Next Up")
		}
	}
	// One season back to unwatched: the series' count follows.
	seasonID := cat.JFID(show.Seasons[0].ID)
	if code, _ := watchedSend(t, srv, http.MethodDelete, "/UserPlayedItems/"+seasonID+"?userId="+jfUserID); code != http.StatusOK {
		t.Fatalf("DELETE for the season answered %d", code)
	}
	if ud := jfUserDataOf(t, srv, showID); num(ud["UnplayedItemCount"]) != len(show.Seasons[0].Episodes) || ud["Played"] != false {
		t.Errorf("the series after its first season was unmarked: %v", ud)
	}

	// --- A server that refuses: 500, and nothing changes.
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?refuse=1")
	if code, _ := watchedSend(t, srv, http.MethodPost, modern); code != http.StatusInternalServerError {
		t.Errorf("a refusing server answered %d, want 500", code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != false {
		t.Errorf("a refused mark was kept")
	}
	watchedSend(t, srv, http.MethodPost, "/__showcase/watched?refuse=0")

	// --- The readout: method, path and raw query of each.
	got := watchedReadoutOf(t, srv)
	want := []watchedRequest{
		{Method: "POST", Path: "/UserPlayedItems/" + movieID, Query: "userId=" + jfUserID, Item: fresh.ID, Kind: "movie", Watched: true, Status: 200},
		{Method: "DELETE", Path: "/UserPlayedItems/" + movieID, Query: "userId=" + jfUserID, Item: fresh.ID, Kind: "movie", Watched: false, Status: 200},
		{Method: "POST", Path: "/UserPlayedItems/" + movieID, Query: "userId=" + jfUserID, Item: fresh.ID, Kind: "movie", Watched: true, Status: 404},
		{Method: "POST", Path: legacy, Query: "", Item: fresh.ID, Kind: "movie", Watched: true, Status: 200},
		{Method: "DELETE", Path: legacy, Query: "", Item: fresh.ID, Kind: "movie", Watched: false, Status: 200},
		{Method: "POST", Path: "/UserPlayedItems/" + showID, Query: "userId=" + jfUserID, Item: show.ID, Kind: "show", Watched: true, Status: 200},
		{Method: "DELETE", Path: "/UserPlayedItems/" + seasonID, Query: "userId=" + jfUserID, Item: show.Seasons[0].ID, Kind: "season", Watched: false, Status: 200},
		{Method: "POST", Path: "/UserPlayedItems/" + movieID, Query: "userId=" + jfUserID, Item: fresh.ID, Kind: "movie", Watched: true, Status: 500},
	}
	if len(got.Requests) != len(want) {
		t.Fatalf("%d requests in the readout, want %d: %+v", len(got.Requests), len(want), got.Requests)
	}
	for i := range want {
		if got.Requests[i] != want[i] {
			t.Errorf("request %d reads %+v, want %+v", i, got.Requests[i], want[i])
		}
	}

	// --- Not a video: both routes answer as they always did, and nothing
	// is recorded. (Books off: /UserPlayedItems is the landing page.)
	albumID := cat.JFID(cat.Albums[0].ID)
	code, data = watchedSend(t, srv, http.MethodPost, "/Users/"+jfUserID+"/PlayedItems/"+albumID)
	if code != http.StatusOK || !strings.Contains(string(data), `"Played":true`) {
		t.Errorf("the legacy route for an album answered %d %s", code, string(data))
	}
	code, data = watchedSend(t, srv, http.MethodPost, "/UserPlayedItems/"+albumID+"?userId="+jfUserID)
	if code != http.StatusOK || !strings.Contains(string(data), "Alexandria Showcase") {
		t.Errorf("/UserPlayedItems for an album answered %d %s", code, string(data[:min(len(data), 80)]))
	}
	if after := watchedReadoutOf(t, srv); len(after.Requests) != len(want) {
		t.Errorf("a request for an album was recorded")
	}
}

// With the audiobooks on, a book's played mark still goes to the books'
// own route (jellyfin_books_test.go), and a film's to the watched marks.
func TestJellyfinWatchedMarksBesideBooks(t *testing.T) {
	on, _ := jfBooksServers(t)
	srv := httptest.NewServer(on.JellyfinHandler())
	defer srv.Close()

	movieID := on.cat.JFID(on.cat.Movies[0].ID)
	if code, _ := watchedSend(t, srv, http.MethodPost, "/UserPlayedItems/"+movieID+"?userId="+jfUserID); code != http.StatusOK {
		t.Fatalf("a film's POST answered %d", code)
	}
	if ud := jfUserDataOf(t, srv, movieID); ud["Played"] != true {
		t.Errorf("the film was not marked: %v", ud)
	}
	var book *Book
	for _, b := range on.cat.Books {
		if !b.Ebook {
			book = b
			break
		}
	}
	if book == nil {
		t.Fatalf("no audiobook in the catalog")
	}
	if code, _ := watchedSend(t, srv, http.MethodPost, "/UserPlayedItems/"+on.cat.JFID(book.ID)+"?userId="+jfUserID); code != http.StatusOK {
		t.Fatalf("a book's POST answered %d", code)
	}
	if got := watchedReadoutOf(t, srv); len(got.Requests) != 1 || got.Requests[0].Kind != "movie" {
		t.Errorf("the watched readout holds %+v, want the film alone", got.Requests)
	}
	_, data := jfGet(t, srv, "/__showcase/reports")
	if !strings.Contains(string(data), `"route":"UserPlayedItems"`) {
		t.Errorf("the book's mark did not reach the books' route: %s", string(data))
	}
}
