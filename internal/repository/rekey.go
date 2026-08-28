package repository

import (
	"log"

	"github.com/stoutsolutions/alexandria-server/internal/catalog"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// contentKeyVersion names the current key algorithm. Bump it whenever
// catalog.ContentKey changes meaning, and every stored key is recomputed
// on the next startup.
//
// Keys are computed at WRITE time, so an algorithm fix alone only
// affects rows written afterwards — the first deployment kept reporting
// 3,335 movies for a 1,666-film library after the video fix shipped,
// because nothing had rewritten its rows. The server owns its own
// derived data; it must not wait for a phone to wander by and re-sync.
//
// v2: video keys on title|year (was: unkeyable, author-based only).
const contentKeyVersion = "2"

// RekeyCatalogIfNeeded recomputes every stored content key when the
// algorithm has changed since the keys were written.
//
// The one thing this loses is a CLIENT-supplied key (a Plex guid), since
// a stored key does not record where it came from. Acceptable on
// purpose: no client sends one yet, and a catalog sync REPLACES rows
// wholesale, so the next sync restores anything a rekey flattened.
func RekeyCatalogIfNeeded() error {
	stored, err := database.MetaGet("content_key_version")
	if err != nil {
		return err
	}
	if stored == contentKeyVersion {
		return nil
	}

	rows, err := database.DB.Query(`
		SELECT id, source, item_id, media_type, title,
		       COALESCE(author, ''), COALESCE(year, 0)
		FROM catalog_items`)
	if err != nil {
		return err
	}

	type rekey struct {
		id  string
		key any
	}
	var updates []rekey
	for rows.Next() {
		var id string
		var it catalog.Item
		if err := rows.Scan(&id, &it.Source, &it.ItemID, &it.MediaType,
			&it.Title, &it.Author, &it.Year); err != nil {
			rows.Close()
			return err
		}
		var key any
		if k := catalog.ContentKey(it); k != "" {
			key = k
		}
		updates = append(updates, rekey{id: id, key: key})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(
		`UPDATE catalog_items SET content_key = ? WHERE id = ?`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, u := range updates {
		if _, err := stmt.Exec(u.key, u.id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}

	if err := database.MetaSet("content_key_version", contentKeyVersion); err != nil {
		return err
	}
	if len(updates) > 0 {
		log.Printf("rekeyed %d catalog rows to content-key v%s",
			len(updates), contentKeyVersion)
	}
	return nil
}
