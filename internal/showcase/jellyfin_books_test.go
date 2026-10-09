package showcase

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The Jellyfin dialect's audiobooks (Server.Books, SHOWCASE_JELLYFIN_BOOKS=1):
// off, nothing about books leaks and every route answers as before; on, the
// books view, its items, Latest, Resume, the authors, the author filter, the
// WAV stream, the write routes and the reports readout.

func jfBooksServers(t *testing.T) (on *Server, off *Server) {
	t.Helper()
	cat := Build(time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC), nil).Half(1)
	on = New(cat, "")
	on.Books = true
	off = New(cat, "")
	off.Books = false
	return on, off
}

func jfGet(t *testing.T, srv *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func jfPost(t *testing.T, srv *httptest.Server, path, body string) (int, []byte) {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func jfItems(t *testing.T, data []byte) []map[string]any {
	t.Helper()
	var env struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("envelope: %v: %s", err, string(data[:min(len(data), 200)]))
	}
	return env.Items
}

func audioBookCount(cat *Catalog) int {
	n := 0
	for _, b := range cat.Books {
		if !b.Ebook {
			n++
		}
	}
	return n
}

const booksItemsQuery = "/Users/" + jfUserID + "/Items?ParentId=" + jfBooksView + "&IncludeItemTypes=MusicAlbum,AudioBook&Recursive=true&SortBy=SortName&SortOrder=Ascending"

func TestJellyfinBooksOffIsUnchanged(t *testing.T) {
	_, off := jfBooksServers(t)
	srv := httptest.NewServer(off.JellyfinHandler())
	defer srv.Close()
	// Three views, no books view.
	_, data := jfGet(t, srv, "/Users/"+jfUserID+"/Views")
	views := jfItems(t, data)
	if len(views) != 3 {
		t.Fatalf("views off: %d", len(views))
	}
	for _, v := range views {
		if v["CollectionType"] == "books" {
			t.Fatal("books view leaked with Books off")
		}
	}
	// A books query answers nothing.
	_, data = jfGet(t, srv, booksItemsQuery)
	if n := len(jfItems(t, data)); n != 0 {
		t.Fatalf("books items off: %d", n)
	}
	// A book's id resolves to no item, no stream.
	book := off.cat.Books[0]
	jf := off.cat.JFID(book.ID)
	if code, _ := jfGet(t, srv, "/Users/"+jfUserID+"/Items/"+jf); code != 404 {
		t.Fatalf("book detail off: %d", code)
	}
	if code, _ := jfGet(t, srv, "/Audio/"+jf+"/universal"); code != 404 {
		t.Fatalf("book stream off: %d", code)
	}
	// The write routes fall through to the landing page, as they always did.
	code, data := jfPost(t, srv, "/UserPlayedItems/"+jf+"?userId="+jfUserID, "")
	if code != 200 || !strings.Contains(string(data), "<html>") {
		t.Fatalf("UserPlayedItems off: %d %s", code, string(data[:min(len(data), 60)]))
	}
	code, data = jfPost(t, srv, "/UserItems/"+jf+"/UserData?userId="+jfUserID, `{"PlaybackPositionTicks":10}`)
	if code != 200 || !strings.Contains(string(data), "<html>") {
		t.Fatalf("UserItems off: %d", code)
	}
	code, data = jfGet(t, srv, "/__showcase/reports")
	if code != 200 || !strings.Contains(string(data), "<html>") {
		t.Fatalf("reports off: %d", code)
	}
	// Resume for the books view with Books off: today's answer (the part-played
	// music albums), pinned so a flag-off regression shows.
	_, data = jfGet(t, srv, "/Users/"+jfUserID+"/Items/Resume?ParentId="+jfBooksView+"&MediaTypes=Audio&IncludeItemTypes=Audio,AudioBook")
	want := 0
	for _, a := range off.cat.PlayedAlbums() {
		if a.Played < 1 {
			want++
		}
	}
	if n := len(jfItems(t, data)); n != want {
		t.Fatalf("resume off: %d albums, want %d", n, want)
	}
	// Latest for the books view answers nothing.
	_, data = jfGet(t, srv, "/Users/"+jfUserID+"/Items/Latest?ParentId="+jfBooksView+"&Limit=5")
	var latest []map[string]any
	_ = json.Unmarshal(data, &latest)
	if len(latest) != 0 {
		t.Fatalf("latest off: %d", len(latest))
	}
	// Album artists scoped to the books view answer the music artists, as today.
	_, data = jfGet(t, srv, "/Artists/AlbumArtists?UserId="+jfUserID+"&ParentId="+jfBooksView)
	if n := len(jfItems(t, data)); n != len(off.cat.Artists) {
		t.Fatalf("authors off: %d, want the %d artists", n, len(off.cat.Artists))
	}
}

func TestJellyfinBooksOn(t *testing.T) {
	on, _ := jfBooksServers(t)
	srv := httptest.NewServer(on.JellyfinHandler())
	defer srv.Close()
	wantBooks := audioBookCount(on.cat)

	// Views: four, the last a books library.
	_, data := jfGet(t, srv, "/Users/"+jfUserID+"/Views")
	views := jfItems(t, data)
	if len(views) != 4 || views[3]["CollectionType"] != "books" || views[3]["Id"] != jfBooksView {
		t.Fatalf("views on: %v", views)
	}

	// Library: every audiobook, each an AudioBook with a duration, a cover and UserData.
	_, data = jfGet(t, srv, booksItemsQuery)
	items := jfItems(t, data)
	if len(items) != wantBooks {
		t.Fatalf("books on: %d, want %d", len(items), wantBooks)
	}
	for _, it := range items {
		if it["Type"] != "AudioBook" || it["MediaType"] != "Audio" {
			t.Fatalf("type: %v %v", it["Type"], it["MediaType"])
		}
		if ticks, _ := it["RunTimeTicks"].(float64); ticks <= 0 {
			t.Fatalf("RunTimeTicks: %v", it["RunTimeTicks"])
		}
		if tags, _ := it["ImageTags"].(map[string]any); tags["Primary"] == nil {
			t.Fatalf("ImageTags: %v", it["ImageTags"])
		}
		if _, hasUD := it["UserData"].(map[string]any); !hasUD {
			t.Fatal("UserData missing")
		}
		if _, leaked := it["Chapters"]; leaked {
			t.Fatal("list rows carry no Chapters")
		}
	}
	// A–Z by SortName ("The" dropped).
	if items[0]["Name"] == "" || items[0]["SortName"].(string) > items[1]["SortName"].(string) {
		t.Fatalf("sort: %v > %v", items[0]["SortName"], items[1]["SortName"])
	}

	// Detail: Chapters (the catalog's) and one MediaSource.
	var book *Book
	for _, b := range on.cat.Books {
		if !b.Ebook && b.Progress > 0 && b.Progress < 1 {
			book = b
			break
		}
	}
	if book == nil {
		t.Fatal("no in-progress audiobook in the catalog")
	}
	jf := on.cat.JFID(book.ID)
	code, data := jfGet(t, srv, "/Users/"+jfUserID+"/Items/"+jf+"?Fields=Chapters,MediaSources")
	if code != 200 {
		t.Fatalf("detail: %d", code)
	}
	var detail map[string]any
	_ = json.Unmarshal(data, &detail)
	chapters, _ := detail["Chapters"].([]any)
	if len(chapters) != len(book.Chapters) {
		t.Fatalf("chapters: %d, want %d", len(chapters), len(book.Chapters))
	}
	first := chapters[0].(map[string]any)
	if first["Name"] != book.Chapters[0].Title || first["StartPositionTicks"].(float64) != 0 {
		t.Fatalf("first chapter: %v", first)
	}
	sources, _ := detail["MediaSources"].([]any)
	if len(sources) != 1 || sources[0].(map[string]any)["Id"] != jf {
		t.Fatalf("media sources: %v", detail["MediaSources"])
	}
	ud := detail["UserData"].(map[string]any)
	wantTicks := float64(ticks(book.Progress * book.Duration))
	if ud["PlaybackPositionTicks"].(float64) != wantTicks {
		t.Fatalf("resume ticks: %v, want %v", ud["PlaybackPositionTicks"], wantTicks)
	}
	if detail["AlbumArtist"] != book.Author.Name || detail["ProductionYear"].(float64) == 0 {
		t.Fatalf("author/year: %v %v", detail["AlbumArtist"], detail["ProductionYear"])
	}

	// Latest: newest first, audiobooks only, honours Limit.
	_, data = jfGet(t, srv, "/Users/"+jfUserID+"/Items/Latest?ParentId="+jfBooksView+"&Limit=5&IncludeItemTypes=MusicAlbum,AudioBook")
	var latest []map[string]any
	if err := json.Unmarshal(data, &latest); err != nil || len(latest) != 5 {
		t.Fatalf("latest: %v %d", err, len(latest))
	}
	newest := on.cat.RecentBooks(len(on.cat.Books))
	for _, b := range newest {
		if !b.Ebook {
			if latest[0]["Id"] != on.cat.JFID(b.ID) {
				t.Fatalf("latest[0] = %v, want %s", latest[0]["Name"], b.Title)
			}
			break
		}
	}

	// Resume: the in-progress audiobooks, each with a position.
	_, data = jfGet(t, srv, "/Users/"+jfUserID+"/Items/Resume?ParentId="+jfBooksView+"&MediaTypes=Audio&IncludeItemTypes=Audio,AudioBook&Limit=20")
	resume := jfItems(t, data)
	wantResume := 0
	for _, b := range on.cat.InProgressBooks() {
		if !b.Ebook {
			wantResume++
		}
	}
	if len(resume) != wantResume || wantResume == 0 {
		t.Fatalf("resume: %d, want %d", len(resume), wantResume)
	}
	for _, it := range resume {
		if it["UserData"].(map[string]any)["PlaybackPositionTicks"].(float64) <= 0 {
			t.Fatalf("resume row without a position: %v", it["Name"])
		}
	}
	if resume[0]["Id"] != on.cat.JFID(on.cat.InProgressBooks()[0].ID) {
		t.Fatalf("resume order: %v", resume[0]["Name"])
	}

	// Authors: the album artists of the books view, A–Z, with book counts.
	_, data = jfGet(t, srv, "/Artists/AlbumArtists?UserId="+jfUserID+"&ParentId="+jfBooksView+"&SortBy=SortName&SortOrder=Ascending")
	authors := jfItems(t, data)
	wantAuthors := 0
	for _, a := range on.cat.Authors {
		for _, b := range a.Books {
			if !b.Ebook {
				wantAuthors++
				break
			}
		}
	}
	if len(authors) != wantAuthors {
		t.Fatalf("authors: %d, want %d", len(authors), wantAuthors)
	}
	for i := 1; i < len(authors); i++ {
		if authors[i-1]["SortName"].(string) > authors[i]["SortName"].(string) {
			t.Fatalf("authors unsorted at %d", i)
		}
	}
	// The author filter: that author's audiobooks only.
	author := book.Author
	_, data = jfGet(t, srv, booksItemsQuery+"&ArtistIds="+on.cat.JFID(author.ID))
	byAuthor := jfItems(t, data)
	wantByAuthor := 0
	for _, b := range author.Books {
		if !b.Ebook {
			wantByAuthor++
		}
	}
	if len(byAuthor) != wantByAuthor {
		t.Fatalf("by author: %d, want %d", len(byAuthor), wantByAuthor)
	}
	for _, it := range byAuthor {
		if it["AlbumArtist"] != author.Name {
			t.Fatalf("by author leaked %v", it["AlbumArtist"])
		}
	}
	// The author by id, and the author's square image.
	if code, _ := jfGet(t, srv, "/Artists/"+on.cat.JFID(author.ID)); code != 200 {
		t.Fatalf("author by id: %d", code)
	}

	// Search within the books view.
	_, data = jfGet(t, srv, booksItemsQuery+"&SearchTerm="+strings.ToLower(strings.Fields(book.Title)[len(strings.Fields(book.Title))-1]))
	if n := len(jfItems(t, data)); n == 0 || n > wantBooks {
		t.Fatalf("search: %d", n)
	}

	// The stream: a WAV, ranged.
	resp, err := http.Get(srv.URL + "/Audio/" + jf + "/universal?UserId=" + jfUserID + "&ApiKey=x&Container=mp3,aac&PlaySessionId=ps1")
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4)
	_, _ = io.ReadFull(resp.Body, head)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "audio/wav" || string(head) != "RIFF" {
		t.Fatalf("stream: %d %s %q", resp.StatusCode, resp.Header.Get("Content-Type"), head)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/Audio/"+jf+"/universal", nil)
	req.Header.Set("Range", "bytes=0-99")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	ranged, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 206 || len(ranged) != 100 {
		t.Fatalf("range: %d %d", resp.StatusCode, len(ranged))
	}
	// The book's cover.
	if code, _ := jfGet(t, srv, "/Items/"+jf+"/Images/Primary?tag=x&maxWidth=400"); code != 200 {
		t.Fatalf("cover: %d", code)
	}
	if code, _ := jfGet(t, srv, "/Items/"+jfBooksView+"/Images/Primary"); code != 200 {
		t.Fatalf("view image: %d", code)
	}

	// Reports: Playing / Progress / Stopped are recorded with their facts;
	// Stopped moves the book's position; the write routes answer and move it too.
	_, _ = jfGet(t, srv, "/__showcase/reports") // read
	reqDel, _ := http.NewRequest(http.MethodDelete, srv.URL+"/__showcase/reports", nil)
	if resp, err := http.DefaultClient.Do(reqDel); err == nil {
		resp.Body.Close()
	}
	posTicks := int64(ticks(1234))
	report := func(route string, pos int64, paused bool) {
		code, _ := jfPost(t, srv, "/Sessions/Playing"+route,
			fmt.Sprintf(`{"ItemId":%q,"MediaSourceId":%q,"PlaySessionId":"ps-abc","PositionTicks":%d,"IsPaused":%v,"PlayMethod":"DirectPlay"}`, jf, jf, pos, paused))
		if code != 204 {
			t.Fatalf("Sessions/Playing%s: %d", route, code)
		}
	}
	report("", posTicks, false)
	report("/Progress", posTicks+ticks(60), false)
	report("/Stopped", posTicks+ticks(120), false)
	_, data = jfGet(t, srv, "/__showcase/reports")
	var readout struct {
		Reports []reportRecord `json:"reports"`
	}
	if err := json.Unmarshal(data, &readout); err != nil || len(readout.Reports) != 3 {
		t.Fatalf("reports: %v %d", err, len(readout.Reports))
	}
	if r := readout.Reports[0]; r.Route != "Sessions/Playing" || r.ItemID != jf || r.PlaySessionID != "ps-abc" || r.PositionTicks != posTicks {
		t.Fatalf("playing record: %+v", r)
	}
	if r := readout.Reports[2]; r.Route != "Sessions/Playing/Stopped" || r.PositionTicks != posTicks+ticks(120) {
		t.Fatalf("stopped record: %+v", r)
	}
	// The stop moved the book: its detail now resumes at 1354 s.
	_, data = jfGet(t, srv, "/Users/"+jfUserID+"/Items/"+jf)
	_ = json.Unmarshal(data, &detail)
	if got := detail["UserData"].(map[string]any)["PlaybackPositionTicks"].(float64); got != float64(posTicks+ticks(120)) {
		t.Fatalf("position after stop: %v, want %d", got, posTicks+ticks(120))
	}
	// UserData write: applied verbatim, echoed, recorded.
	code, data = jfPost(t, srv, "/UserItems/"+jf+"/UserData?userId="+jfUserID, fmt.Sprintf(`{"PlaybackPositionTicks":%d}`, ticks(5000)))
	if code != 200 {
		t.Fatalf("UserItems: %d", code)
	}
	var echoed map[string]any
	_ = json.Unmarshal(data, &echoed)
	if echoed["PlaybackPositionTicks"].(float64) != float64(ticks(5000)) {
		t.Fatalf("UserData echo: %v", echoed)
	}
	// Played write: finished.
	code, data = jfPost(t, srv, "/UserPlayedItems/"+jf+"?userId="+jfUserID, "")
	if code != 200 {
		t.Fatalf("UserPlayedItems: %d", code)
	}
	_ = json.Unmarshal(data, &echoed)
	if echoed["Played"] != true {
		t.Fatalf("played echo: %v", echoed)
	}
	_, data = jfGet(t, srv, "/__showcase/reports")
	_ = json.Unmarshal(data, &readout)
	if len(readout.Reports) != 5 || readout.Reports[3].Route != "UserItems/UserData" || readout.Reports[4].Route != "UserPlayedItems" {
		t.Fatalf("write records: %d %+v", len(readout.Reports), readout.Reports)
	}
	// Unknown ids on the write routes: 404.
	if code, _ := jfPost(t, srv, "/UserItems/nope/UserData?userId="+jfUserID, `{}`); code != 404 {
		t.Fatalf("UserItems unknown: %d", code)
	}
	// The legacy 10.x routes still answer as before.
	if code, _ := jfPost(t, srv, "/Users/"+jfUserID+"/PlayedItems/"+jf, ""); code != 200 {
		t.Fatalf("legacy PlayedItems: %d", code)
	}
}

// Music queries are untouched by the books block: albums by id (no view),
// a music view's albums, and server-wide MusicAlbum searches never gain a book.
func TestJellyfinBooksDoNotLeakIntoMusic(t *testing.T) {
	on, _ := jfBooksServers(t)
	srv := httptest.NewServer(on.JellyfinHandler())
	defer srv.Close()
	for _, q := range []string{
		"/Users/" + jfUserID + "/Items?ParentId=" + jfMusicView + "&IncludeItemTypes=MusicAlbum&Recursive=true",
		"/Users/" + jfUserID + "/Items?IncludeItemTypes=MusicAlbum&Recursive=true&SearchTerm=the",
		"/Users/" + jfUserID + "/Items?IncludeItemTypes=MusicAlbum&Recursive=true&Ids=" + on.cat.JFID(on.cat.Albums[0].ID),
		"/Users/" + jfUserID + "/Items/Latest?ParentId=" + jfMusicView + "&IncludeItemTypes=MusicAlbum&Limit=20",
	} {
		_, data := jfGet(t, srv, q)
		var items []map[string]any
		if strings.Contains(q, "Latest") {
			_ = json.Unmarshal(data, &items)
		} else {
			items = jfItems(t, data)
		}
		for _, it := range items {
			if it["Type"] == "AudioBook" {
				t.Fatalf("a book leaked into %s: %v", q, it["Name"])
			}
		}
	}
	// The music view's album artists stay the artists.
	_, data := jfGet(t, srv, "/Artists/AlbumArtists?UserId="+jfUserID+"&ParentId="+jfMusicView)
	if n := len(jfItems(t, data)); n != len(on.cat.Artists) {
		t.Fatalf("music artists: %d, want %d", n, len(on.cat.Artists))
	}
}
