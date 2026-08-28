package repository

import (
	"path/filepath"
	"testing"

	"github.com/stoutsolutions/alexandria-server/internal/catalog"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// newCatalogDB spins up a real SQLite file with the real migrations.
// The SQL here is the part that cannot be verified by reading it —
// NULL grouping and the summary aggregates only prove themselves
// against an actual database.
func newCatalogDB(t *testing.T) *CatalogRepository {
	t.Helper()

	path := filepath.Join(t.TempDir(), "catalog_test.db")
	if err := database.Connect(path); err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	if err := database.RunMigrations(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	return NewCatalogRepository()
}

func mkItem(id, title, author, mediaType string) catalog.Item {
	return catalog.Item{
		ItemID:    id,
		Title:     title,
		Author:    author,
		MediaType: mediaType,
	}
}

func TestReplaceSourceIsASnapshotNotAnAppend(t *testing.T) {
	repo := newCatalogDB(t)

	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("p1", "Discovery", "Daft Punk", "music"),
		mkItem("p2", "Homework", "Daft Punk", "music"),
	}); err != nil {
		t.Fatalf("first sync: %v", err)
	}

	// Homework was removed on the media server; the index must forget it.
	n, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("p1", "Discovery", "Daft Punk", "music"),
	})
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if n != 1 {
		t.Fatalf("written = %d, want 1", n)
	}

	items, err := repo.GetItems(ItemQuery{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(items) != 1 || items[0].Title != "Discovery" {
		t.Fatalf("stale row survived the resync: %+v", items)
	}
}

func TestReplaceSourceLeavesOtherSourcesAlone(t *testing.T) {
	repo := newCatalogDB(t)

	if _, err := repo.ReplaceSource("navidrome", []catalog.Item{
		mkItem("n1", "Rumours", "Fleetwood Mac", "music"),
	}); err != nil {
		t.Fatalf("navidrome sync: %v", err)
	}
	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("p1", "Discovery", "Daft Punk", "music"),
	}); err != nil {
		t.Fatalf("plex sync: %v", err)
	}

	counts, err := repo.CountBySource()
	if err != nil {
		t.Fatalf("counts: %v", err)
	}
	if counts["navidrome"] != 1 {
		t.Errorf("syncing plex disturbed navidrome: %v", counts)
	}
	if counts["plex"] != 1 {
		t.Errorf("plex count = %v", counts)
	}
}

func TestDuplicatesAreCrossSourceOnly(t *testing.T) {
	repo := newCatalogDB(t)

	// Discovery on three servers; Rumours twice on ONE server.
	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("p1", "Discovery", "Daft Punk", "music"),
		mkItem("p2", "Rumours", "Fleetwood Mac", "music"),
		mkItem("p3", "Rumours", "Fleetwood Mac", "music"),
	}); err != nil {
		t.Fatalf("plex: %v", err)
	}
	if _, err := repo.ReplaceSource("navidrome", []catalog.Item{
		mkItem("n1", "discovery", "daft punk", "music"),
	}); err != nil {
		t.Fatalf("navidrome: %v", err)
	}
	if _, err := repo.ReplaceSource("jellyfin", []catalog.Item{
		mkItem("j1", "Discovery!", "Daft  Punk", "music"),
	}); err != nil {
		t.Fatalf("jellyfin: %v", err)
	}

	keys, err := repo.GetDuplicateKeys(0)
	if err != nil {
		t.Fatalf("dupes: %v", err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected only the cross-server dupe, got %v", keys)
	}
	if keys[0] != "discovery|daft punk" {
		t.Fatalf("unexpected key %q", keys[0])
	}

	byKey, err := repo.GetByContentKeys(keys)
	if err != nil {
		t.Fatalf("by keys: %v", err)
	}
	if got := len(byKey[keys[0]]); got != 3 {
		t.Fatalf("expected 3 copies of Discovery, got %d", got)
	}
}

// Unkeyable rows must never collapse into each other in SQL. They are
// stored as NULL precisely so GROUP BY treats them as distinct.
func TestUngroupableRowsStayDistinctInSQL(t *testing.T) {
	repo := newCatalogDB(t)

	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("p1", "Untitled", "", "music"),
		mkItem("p2", "Untitled", "Various Artists", "music"),
	}); err != nil {
		t.Fatalf("plex: %v", err)
	}
	if _, err := repo.ReplaceSource("navidrome", []catalog.Item{
		mkItem("n1", "Untitled", "Unknown", "music"),
	}); err != nil {
		t.Fatalf("navidrome: %v", err)
	}

	keys, err := repo.GetDuplicateKeys(0)
	if err != nil {
		t.Fatalf("dupes: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("placeholder creators were merged across servers: %v", keys)
	}

	s, err := repo.Summarize()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.Ungroupable != 3 {
		t.Errorf("Ungroupable = %d, want 3", s.Ungroupable)
	}
	if s.DistinctWorks != 3 {
		t.Errorf("DistinctWorks = %d, want 3", s.DistinctWorks)
	}
	if s.DuplicateCopies != 0 {
		t.Errorf("DuplicateCopies = %d, want 0", s.DuplicateCopies)
	}
}

func TestSummarizeMatchesTheInMemoryDedupe(t *testing.T) {
	repo := newCatalogDB(t)

	plex := []catalog.Item{
		mkItem("p1", "Discovery", "Daft Punk", "music"),
		mkItem("p2", "Rumours", "Fleetwood Mac", "music"),
		mkItem("p3", "Untitled", "", "music"),
	}
	navidrome := []catalog.Item{
		mkItem("n1", "Discovery", "Daft Punk", "music"),
	}
	if _, err := repo.ReplaceSource("plex", plex); err != nil {
		t.Fatalf("plex: %v", err)
	}
	if _, err := repo.ReplaceSource("navidrome", navidrome); err != nil {
		t.Fatalf("navidrome: %v", err)
	}

	got, err := repo.Summarize()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}

	// The SQL rollup and the pure Go collapse must agree. If they ever
	// drift, the app and the server tell the user different numbers
	// about the same library.
	all := make([]catalog.Item, 0, len(plex)+len(navidrome))
	for _, it := range plex {
		it.Source = "plex"
		all = append(all, it)
	}
	for _, it := range navidrome {
		it.Source = "navidrome"
		all = append(all, it)
	}
	want := catalog.Summarize(all)

	if got.RawItems != want.RawItems {
		t.Errorf("RawItems: sql=%d memory=%d", got.RawItems, want.RawItems)
	}
	if got.DistinctWorks != want.Distinct {
		t.Errorf("Distinct: sql=%d memory=%d", got.DistinctWorks, want.Distinct)
	}
	if got.DuplicateCopies != want.Duplicates {
		t.Errorf("Duplicates: sql=%d memory=%d",
			got.DuplicateCopies, want.Duplicates)
	}
	if got.Ungroupable != want.Ungrouped {
		t.Errorf("Ungrouped: sql=%d memory=%d",
			got.Ungroupable, want.Ungrouped)
	}
	if got.BySource["plex"] != 3 || got.BySource["navidrome"] != 1 {
		t.Errorf("BySource = %v", got.BySource)
	}
	if got.ByMediaType["music"] != 4 {
		t.Errorf("ByMediaType = %v", got.ByMediaType)
	}
}

// The same item id from two different servers is two rows, not one.
// UNIQUE(source, item_id) has to be scoped by source or a shared id
// scheme would silently drop a copy.
func TestSameItemIDFromDifferentSourcesCoexist(t *testing.T) {
	repo := newCatalogDB(t)

	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		mkItem("shared-id", "Discovery", "Daft Punk", "music"),
	}); err != nil {
		t.Fatalf("plex: %v", err)
	}
	if _, err := repo.ReplaceSource("navidrome", []catalog.Item{
		mkItem("shared-id", "Discovery", "Daft Punk", "music"),
	}); err != nil {
		t.Fatalf("navidrome: %v", err)
	}

	s, err := repo.Summarize()
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if s.RawItems != 2 {
		t.Errorf("RawItems = %d, want 2 (one per source)", s.RawItems)
	}
	if s.DistinctWorks != 1 {
		t.Errorf("DistinctWorks = %d, want 1", s.DistinctWorks)
	}
}
