package repository

import (
	"database/sql"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/catalog"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// CatalogRepository stores the household's merged library index.
//
// There is no tenant column. One deployment serves one household, and
// the shared API token is the boundary — adding a per-user scope here
// would drag identity back into a server that deliberately has none.
//
// It reuses catalog.Item rather than defining a parallel struct in
// models: catalog.ContentKey is defined over that exact shape, and a
// second copy of the struct is a second thing to keep in sync with the
// dedupe rules. One struct, one key function.
type CatalogRepository struct{}

func NewCatalogRepository() *CatalogRepository {
	return &CatalogRepository{}
}

// ReplaceSource swaps in a fresh snapshot of one source's library.
//
// A library sync must be a REPLACE, not an append: an item deleted on
// the media server has to disappear from the index too, or the index
// lies forever. The delete is scoped to the one source and touches only
// rows in this server's own cache — never media on anybody's server,
// and never another source's rows.
//
// The whole swap runs in one transaction, so a failure part-way leaves
// the previous snapshot intact rather than a half-empty library.
func (r *CatalogRepository) ReplaceSource(
	source string,
	items []catalog.Item,
) (int, error) {
	return r.writeSource(source, items, true)
}

// AppendToSource adds items without clearing what is already stored.
//
// A large library does not fit in one request — 20,000 rows is about
// 4 MB of JSON, which is already at the edge of what a server should
// accept in one go. So the app clears the source with the first chunk
// and appends the rest, and the whole library lands without ever
// holding it all in memory on either side.
func (r *CatalogRepository) AppendToSource(
	source string,
	items []catalog.Item,
) (int, error) {
	return r.writeSource(source, items, false)
}

func (r *CatalogRepository) writeSource(
	source string,
	items []catalog.Item,
	replace bool,
) (int, error) {
	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() // no-op once Commit succeeds

	if replace {
		if _, err := tx.Exec(
			`DELETE FROM catalog_items WHERE source = ?`, source,
		); err != nil {
			return 0, err
		}
	}

	// OR REPLACE so a re-sent chunk updates rather than failing on the
	// (source, item_id) unique constraint — a retried chunk must be
	// harmless.
	stmt, err := tx.Prepare(`
		INSERT OR REPLACE INTO catalog_items
			(id, source, item_id, content_key, media_type,
			 title, author, series, genre, year, duration_sec)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	written := 0
	for _, it := range items {
		it.Source = source // the request owns the source, not the payload

		// NULL rather than "" for an unkeyable item, so SQL grouping
		// treats each one as distinct instead of lumping them together.
		var key any
		if k := catalog.ContentKey(it); k != "" {
			key = k
		}

		if _, err := stmt.Exec(
			uuid.New().String(), source, it.ItemID, key,
			it.MediaType, it.Title, it.Author, it.Series, it.Genre,
			it.Year, it.DurationS,
		); err != nil {
			return 0, err
		}
		written++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

// ItemQuery narrows a listing. Zero values mean "no filter".
type ItemQuery struct {
	MediaType string
	Source    string
	Limit     int
	Offset    int
}

// GetItems returns raw rows, ordered so paging is stable.
func (r *CatalogRepository) GetItems(q ItemQuery) ([]catalog.Item, error) {
	sqlStr := `
		SELECT item_id, source, media_type, title,
		       COALESCE(author, ''), COALESCE(series, ''),
		       COALESCE(genre, ''), COALESCE(year, 0),
		       COALESCE(duration_sec, 0)
		FROM catalog_items
		WHERE 1 = 1`
	args := []any{}

	if q.MediaType != "" {
		sqlStr += ` AND media_type = ?`
		args = append(args, q.MediaType)
	}
	if q.Source != "" {
		sqlStr += ` AND source = ?`
		args = append(args, q.Source)
	}
	sqlStr += ` ORDER BY title, source`

	if q.Limit > 0 {
		sqlStr += ` LIMIT ?`
		args = append(args, q.Limit)
		if q.Offset > 0 {
			sqlStr += ` OFFSET ?`
			args = append(args, q.Offset)
		}
	}

	rows, err := database.DB.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	items := []catalog.Item{}
	for rows.Next() {
		var it catalog.Item
		if err := rows.Scan(
			&it.ItemID, &it.Source, &it.MediaType, &it.Title,
			&it.Author, &it.Series, &it.Genre, &it.Year, &it.DurationS,
		); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// CountBySource reports how many rows each source contributed.
func (r *CatalogRepository) CountBySource() (map[string]int, error) {
	rows, err := database.DB.Query(`
		SELECT source, COUNT(*) FROM catalog_items
		GROUP BY source ORDER BY source
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var source string
		var n int
		if err := rows.Scan(&source, &n); err != nil {
			return nil, err
		}
		out[source] = n
	}
	return out, rows.Err()
}

// GetDuplicateKeys returns the content keys held by more than one
// SOURCE. Two copies on one server are that server's business; the
// cross-service duplicate is the one the app cannot see on its own.
func (r *CatalogRepository) GetDuplicateKeys(limit int) ([]string, error) {
	sqlStr := `
		SELECT content_key FROM catalog_items
		WHERE content_key IS NOT NULL
		GROUP BY content_key
		HAVING COUNT(DISTINCT source) > 1
		ORDER BY COUNT(DISTINCT source) DESC, content_key`
	args := []any{}
	if limit > 0 {
		sqlStr += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err := database.DB.Query(sqlStr, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	keys := []string{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}

// GetByContentKeys loads every copy of the given works.
func (r *CatalogRepository) GetByContentKeys(
	keys []string,
) (map[string][]catalog.Item, error) {
	out := map[string][]catalog.Item{}
	if len(keys) == 0 {
		return out, nil
	}

	// Chunked to stay clear of SQLite's variable limit on large
	// libraries; the whole point of this server is big collections.
	const chunk = 400
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]

		placeholders := ""
		args := []any{}
		for i, k := range batch {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, k)
		}

		rows, err := database.DB.Query(`
			SELECT content_key, item_id, source, media_type, title,
			       COALESCE(author, ''), COALESCE(series, ''),
			       COALESCE(genre, ''), COALESCE(year, 0),
			       COALESCE(duration_sec, 0)
			FROM catalog_items
			WHERE content_key IN (`+placeholders+`)
			ORDER BY content_key, source
		`, args...)
		if err != nil {
			return nil, err
		}

		for rows.Next() {
			var key string
			var it catalog.Item
			if err := rows.Scan(
				&key, &it.ItemID, &it.Source, &it.MediaType, &it.Title,
				&it.Author, &it.Series, &it.Genre, &it.Year, &it.DurationS,
			); err != nil {
				rows.Close()
				return nil, err
			}
			out[key] = append(out[key], it)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}

// Summary is the cheap answer to "how big is this library, really".
type Summary struct {
	RawItems        int            `json:"rawItems"`
	DistinctWorks   int            `json:"distinctWorks"`
	DuplicateCopies int            `json:"duplicateCopies"`
	Ungroupable     int            `json:"ungroupable"`
	BySource        map[string]int `json:"bySource"`
	ByMediaType     map[string]int `json:"byMediaType"`
}

// Summarize computes the rollup in SQL rather than pulling every row
// into memory — this is the query the phone cannot afford to run.
func (r *CatalogRepository) Summarize() (*Summary, error) {
	s := &Summary{
		BySource:    map[string]int{},
		ByMediaType: map[string]int{},
	}

	var ungroupable sql.NullInt64
	err := database.DB.QueryRow(`
		SELECT COUNT(*),
		       COUNT(CASE WHEN content_key IS NULL THEN 1 END)
		FROM catalog_items
	`).Scan(&s.RawItems, &ungroupable)
	if err != nil {
		return nil, err
	}
	s.Ungroupable = int(ungroupable.Int64)

	// Distinct works = keyed works + every unkeyable row standing alone.
	var keyedWorks, keyedRows int
	err = database.DB.QueryRow(`
		SELECT COUNT(*), COALESCE(SUM(n), 0) FROM (
			SELECT COUNT(*) AS n FROM catalog_items
			WHERE content_key IS NOT NULL
			GROUP BY content_key
		)
	`).Scan(&keyedWorks, &keyedRows)
	if err != nil {
		return nil, err
	}
	s.DistinctWorks = keyedWorks + s.Ungroupable
	s.DuplicateCopies = keyedRows - keyedWorks

	bySource, err := r.CountBySource()
	if err != nil {
		return nil, err
	}
	s.BySource = bySource

	rows, err := database.DB.Query(`
		SELECT media_type, COUNT(*) FROM catalog_items
		GROUP BY media_type ORDER BY media_type
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var t string
		var n int
		if err := rows.Scan(&t, &n); err != nil {
			return nil, err
		}
		s.ByMediaType[t] = n
	}
	return s, rows.Err()
}
