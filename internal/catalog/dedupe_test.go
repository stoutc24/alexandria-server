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
