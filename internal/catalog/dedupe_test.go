package catalog

import "testing"

func item(title, author, source string) Item {
	return Item{
		ItemID:    source + ":" + title,
		Source:    source,
		MediaType: "music",
		Title:     title,
		Author:    author,
	}
}

func TestContentKeyMatchesAcrossServers(t *testing.T) {
	a := ContentKey(item("Discovery", "Daft Punk", "plex"))
	b := ContentKey(item("discovery", "daft punk", "navidrome"))
	c := ContentKey(item("Discovery!", "Daft  Punk", "jellyfin"))
	if a == "" {
		t.Fatal("expected a key")
	}
	if a != b || a != c {
		t.Fatalf("same album keyed differently: %q %q %q", a, b, c)
	}
}

// The failure mode worse than a duplicate: merging two different works
// because both are credited to a placeholder.
func TestPlaceholderCreatorsAreNeverGrouped(t *testing.T) {
	for _, creator := range []string{
		"", "Unknown", "unknown author", "Various Artists", "VA", "N/A",
		"Anonymous",
	} {
		if k := ContentKey(item("Greatest Hits", creator, "plex")); k != "" {
			t.Errorf("creator %q produced key %q; must refuse to group",
				creator, k)
		}
	}
}

func TestDifferentWorksStayDistinct(t *testing.T) {
	if ContentKey(item("Discovery", "Daft Punk", "plex")) ==
		ContentKey(item("Homework", "Daft Punk", "plex")) {
		t.Fatal("two albums by one artist must not collide")
	}
}

func TestCollapseMergesCopiesAndPrefersSource(t *testing.T) {
	items := []Item{
		item("Rumours", "Fleetwood Mac", "plex"),
		item("Discovery", "Daft Punk", "plex"),
		item("Discovery", "Daft Punk", "navidrome"),
		item("Discovery", "Daft Punk", "jellyfin"),
	}
	groups := Collapse(items, []string{"navidrome"})

	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	// Order preserved: Discovery sits where its first copy appeared.
	d := groups[1]
	if len(d.Copies) != 3 {
		t.Fatalf("expected 3 copies of Discovery, got %d", len(d.Copies))
	}
	if d.Representative.Source != "navidrome" {
		t.Errorf("preferred source ignored: representative is %q",
			d.Representative.Source)
	}
	if len(d.Sources()) != 3 {
		t.Errorf("expected 3 distinct sources, got %v", d.Sources())
	}
}

func TestUngroupableItemsAreAllKept(t *testing.T) {
	items := []Item{
		item("Untitled", "", "plex"),
		item("Untitled", "", "navidrome"),
	}
	groups := Collapse(items, nil)
	if len(groups) != 2 {
		t.Fatalf("two unkeyable items must stay separate, got %d groups",
			len(groups))
	}
}

func TestSummarizeCountsThingsNotCopies(t *testing.T) {
	items := []Item{
		item("Discovery", "Daft Punk", "plex"),
		item("Discovery", "Daft Punk", "navidrome"),
		item("Discovery", "Daft Punk", "jellyfin"),
		item("Rumours", "Fleetwood Mac", "plex"),
		item("Untitled", "", "plex"),
	}
	s := Summarize(items)
	if s.RawItems != 5 {
		t.Errorf("RawItems = %d, want 5", s.RawItems)
	}
	if s.Distinct != 3 {
		t.Errorf("Distinct = %d, want 3 (Discovery, Rumours, Untitled)",
			s.Distinct)
	}
	if s.Duplicates != 2 {
		t.Errorf("Duplicates = %d, want 2", s.Duplicates)
	}
	if s.Ungrouped != 1 {
		t.Errorf("Ungrouped = %d, want 1", s.Ungrouped)
	}
}

func video(title string, year int, source string) Item {
	return Item{
		ItemID:    source + ":" + title,
		Source:    source,
		MediaType: "movie",
		Title:     title,
		Year:      year,
	}
}

// The finding from the first real library: a Plex with overlapping
// sections ("All Movies" superset + genre shelves) indexes each film
// once per section. Author-based keying left every one ungroupable, so
// a 1,666-film library reported 3,335 movies.
func TestTheSameFilmAcrossPlexSectionsIsOneWork(t *testing.T) {
	a := ContentKey(video("Footloose", 1984, "plex"))
	b := ContentKey(video("Footloose", 1984, "plex"))
	if a == "" {
		t.Fatal("a movie with a year must be keyable")
	}
	if a != b {
		t.Fatalf("same film keyed differently: %q %q", a, b)
	}
}

// The year is what makes title-only safe: the remakes must not merge.
func TestRemakesStayDistinct(t *testing.T) {
	if ContentKey(video("Footloose", 1984, "plex")) ==
		ContentKey(video("Footloose", 2011, "plex")) {
		t.Fatal("Footloose 1984 and 2011 merged — the exact failure the " +
			"year exists to prevent")
	}
}

// No year, no key — refusing to guess beats merging two different
// films that happen to share a title.
func TestAFilmWithoutAYearIsNotGrouped(t *testing.T) {
	if k := ContentKey(video("Footloose", 0, "plex")); k != "" {
		t.Fatalf("a yearless film produced key %q", k)
	}
}

// A client-supplied key wins outright: the app can send Plex's guid,
// which survives retitling and is stronger than anything derivable.
func TestAClientSuppliedKeyIsHonored(t *testing.T) {
	a := video("Footloose", 1984, "plex")
	a.ContentKey = "plex://movie/5d9f34ecd5fd3f001ee1409b"
	b := video("Footloose (Remastered)", 1984, "plex")
	b.ContentKey = "plex://movie/5d9f34ecd5fd3f001ee1409b"

	ka, kb := ContentKey(a), ContentKey(b)
	if ka == "" || ka != kb {
		t.Fatalf("shared guid did not group retitled copies: %q %q", ka, kb)
	}

	// And a DIFFERENT guid must keep same-titled items apart.
	c := video("Footloose", 1984, "plex")
	c.ContentKey = "plex://movie/other"
	if ContentKey(c) == ka {
		t.Fatal("different guids merged")
	}
}

// Books are untouched by the video rule — the creator guard still holds.
func TestBooksStillRequireACreator(t *testing.T) {
	book := Item{Source: "abs", MediaType: "audiobook",
		Title: "Greatest Hits", Author: "Unknown", Year: 2020}
	if k := ContentKey(book); k != "" {
		t.Fatalf("a placeholder-credited book grouped via year: %q", k)
	}
}

// Plex's TV sections send show-level rows typed as episodes: title
// equals series, and there is no year. Measured on a live library,
// 301 such rows stood for 156 shows, duplicated across the overlapping
// "All Shows" section exactly as the films were.
func show(title, source string) Item {
	return Item{
		ItemID:    source + ":" + title,
		Source:    source,
		MediaType: "tvEpisode",
		Title:     title,
		Series:    title,
	}
}

func TestTheSameShowAcrossPlexSectionsIsOneWork(t *testing.T) {
	a := ContentKey(show("Band of Brothers", "plex"))
	b := ContentKey(show("Band of Brothers", "plex"))
	if a == "" {
		t.Fatal("a show-level row must be keyable without a year")
	}
	if a != b {
		t.Fatalf("same show keyed differently: %q %q", a, b)
	}
}

func TestDifferentShowsStayDistinct(t *testing.T) {
	if ContentKey(show("Arcane", "plex")) ==
		ContentKey(show("Barry", "plex")) {
		t.Fatal("two unrelated shows merged")
	}
}

// The guard that matters most: a real episode carries an episode title
// that differs from its series, and nothing in the schema can key one
// safely. It must stay ungroupable rather than collapse the season.
func TestRealEpisodesAreNeverCollapsedIntoTheShow(t *testing.T) {
	ep := show("Band of Brothers", "plex")
	ep.Title = "Currahee"
	if k := ContentKey(ep); k != "" {
		t.Fatalf("an episode produced key %q — a whole season would "+
			"collapse into one work", k)
	}
}

// A show with a year keys on title|year like any other video, so
// reboots stay apart on the same rule as remakes.
func TestShowRebootsStayDistinct(t *testing.T) {
	old, new := show("Battlestar Galactica", "plex"), show("Battlestar Galactica", "plex")
	old.Year, new.Year = 1978, 2004
	if ContentKey(old) == ContentKey(new) {
		t.Fatal("1978 and 2004 Battlestar merged")
	}
}

// A film is not a show: series must not smuggle a yearless movie past
// the refuse-to-guess rule.
func TestAYearlessFilmIsStillNotGrouped(t *testing.T) {
	m := video("Footloose", 0, "plex")
	m.Series = "Footloose"
	if k := ContentKey(m); k != "" {
		t.Fatalf("a yearless film produced key %q via series", k)
	}
}
