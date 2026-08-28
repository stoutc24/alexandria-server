package repository

import (
	"testing"

	"github.com/stoutsolutions/alexandria-server/internal/catalog"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// The live-deployment case: a database whose rows were keyed under the
// OLD algorithm (video unkeyable) must heal itself at startup — the
// owner's server kept reporting 3,335 movies for 1,666 films because
// keys are computed at write time and nothing had rewritten its rows.
func TestRekeyHealsAnOldDatabase(t *testing.T) {
	repo := newCatalogDB(t)

	// Rows land keyed under the CURRENT algorithm...
	if _, err := repo.ReplaceSource("plex", []catalog.Item{
		{ItemID: "77623", MediaType: "movie", Title: "Footloose", Year: 1984},
		{ItemID: "77654", MediaType: "movie", Title: "Footloose", Year: 1984},
		{ItemID: "90001", MediaType: "movie", Title: "Footloose", Year: 2011},
	}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	// ...so simulate the old database: strip every key and mark the
	// stored version as the author-only era.
	if _, err := database.DB.Exec(
		`UPDATE catalog_items SET content_key = NULL`); err != nil {
		t.Fatalf("strip: %v", err)
	}
	if err := database.MetaSet("content_key_version", "1"); err != nil {
		t.Fatalf("version: %v", err)
	}

	// Sanity: with keys stripped, nothing groups.
	before, err := repo.Summarize()
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if before.DistinctWorks != 3 {
		t.Fatalf("precondition: expected 3 ungrouped, got %d",
			before.DistinctWorks)
	}

	if err := RekeyCatalogIfNeeded(); err != nil {
		t.Fatalf("rekey: %v", err)
	}

	after, err := repo.Summarize()
	if err != nil {
		t.Fatalf("summarize: %v", err)
	}
	if after.DistinctWorks != 2 {
		t.Errorf("DistinctWorks = %d, want 2 (1984's two sections "+
			"collapse, 2011 stands apart)", after.DistinctWorks)
	}
	if after.DuplicateCopies != 1 {
		t.Errorf("DuplicateCopies = %d, want 1", after.DuplicateCopies)
	}

	// And it must be a one-time cost: a second call is a no-op.
	if err := RekeyCatalogIfNeeded(); err != nil {
		t.Fatalf("second rekey: %v", err)
	}
	v, _ := database.MetaGet("content_key_version")
	if v != contentKeyVersion {
		t.Errorf("version = %q, want %q", v, contentKeyVersion)
	}
}
