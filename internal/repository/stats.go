package repository

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// Event is one listening or reading event.
//
// Every query in this file takes a profileID and filters on it. There is
// deliberately no "read anyone's events" method for an admin route to
// call — the absence is the privacy boundary, so keep it absent.
type Event struct {
	Source     string    `json:"source,omitempty"`
	ItemID     string    `json:"itemId,omitempty"`
	ContentKey string    `json:"contentKey,omitempty"`
	MediaType  string    `json:"mediaType,omitempty"`
	Title      string    `json:"title,omitempty"`
	Author     string    `json:"author,omitempty"`
	EventType  string    `json:"eventType"`
	Seconds    int       `json:"seconds"`
	OccurredAt time.Time `json:"occurredAt"`
}

// StatsSummary is a person's own rollup, computed in SQL.
type StatsSummary struct {
	TotalEvents   int              `json:"totalEvents"`
	TotalSeconds  int64            `json:"totalSeconds"`
	Finished      int              `json:"finished"`
	FirstEventAt  *time.Time       `json:"firstEventAt,omitempty"`
	LastEventAt   *time.Time       `json:"lastEventAt,omitempty"`
	SecondsByType map[string]int64 `json:"secondsByType"`
	TopAuthors    []AuthorTotal    `json:"topAuthors"`
}

type AuthorTotal struct {
	Author  string `json:"author"`
	Seconds int64  `json:"seconds"`
}

type StatsRepository struct{}

func NewStatsRepository() *StatsRepository {
	return &StatsRepository{}
}

// maxEventsPerBatch caps one append so a bad client cannot fill the disk
// in a single request.
const maxEventsPerBatch = 1000

// AppendEvents records events for ONE profile.
//
// The profileID comes from the caller's token, never from the request
// body — otherwise anyone could write into someone else's history by
// naming them.
func (r *StatsRepository) AppendEvents(
	profileID string,
	events []Event,
) (int, error) {
	if len(events) > maxEventsPerBatch {
		return 0, errors.New("too many events in one batch")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`
		INSERT INTO stats_events
			(id, profile_id, source, item_id, content_key, media_type,
			 title, author, event_type, seconds, occurred_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	written := 0
	for _, e := range events {
		if e.EventType == "" {
			e.EventType = "progress"
		}
		if e.OccurredAt.IsZero() {
			e.OccurredAt = time.Now().UTC()
		}
		if e.Seconds < 0 {
			e.Seconds = 0
		}

		if _, err := stmt.Exec(
			uuid.New().String(), profileID, e.Source, e.ItemID,
			nullIfEmpty(e.ContentKey), e.MediaType, e.Title, e.Author,
			e.EventType, e.Seconds, e.OccurredAt.UTC(),
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Summarize builds one profile's own rollup.
func (r *StatsRepository) Summarize(profileID string) (*StatsSummary, error) {
	s := &StatsSummary{SecondsByType: map[string]int64{}, TopAuthors: []AuthorTotal{}}

	var first, last *time.Time
	var firstRaw, lastRaw any
	err := database.DB.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(seconds), 0),
		       COUNT(CASE WHEN event_type = 'finished' THEN 1 END),
		       MIN(occurred_at), MAX(occurred_at)
		FROM stats_events WHERE profile_id = ?
	`, profileID).Scan(&s.TotalEvents, &s.TotalSeconds, &s.Finished,
		&firstRaw, &lastRaw)
	if err != nil {
		return nil, err
	}
	first = asTime(firstRaw)
	last = asTime(lastRaw)
	s.FirstEventAt, s.LastEventAt = first, last

	rows, err := database.DB.Query(`
		SELECT COALESCE(media_type, ''), COALESCE(SUM(seconds), 0)
		FROM stats_events WHERE profile_id = ?
		GROUP BY media_type
	`, profileID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var t string
		var secs int64
		if err := rows.Scan(&t, &secs); err != nil {
			rows.Close()
			return nil, err
		}
		if t != "" {
			s.SecondsByType[t] = secs
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	authorRows, err := database.DB.Query(`
		SELECT author, SUM(seconds) AS secs
		FROM stats_events
		WHERE profile_id = ? AND author IS NOT NULL AND author != ''
		GROUP BY author
		ORDER BY secs DESC, author
		LIMIT 10
	`, profileID)
	if err != nil {
		return nil, err
	}
	defer authorRows.Close()
	for authorRows.Next() {
		var a AuthorTotal
		if err := authorRows.Scan(&a.Author, &a.Seconds); err != nil {
			return nil, err
		}
		s.TopAuthors = append(s.TopAuthors, a)
	}
	return s, authorRows.Err()
}

// asTime copes with SQLite returning MIN/MAX of a DATETIME as either a
// time.Time or a string depending on how it was written.
func asTime(v any) *time.Time {
	switch t := v.(type) {
	case time.Time:
		return &t
	case string:
		for _, layout := range []string{
			time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05.999999999 -0700 MST",
			"2006-01-02 15:04:05",
		} {
			if parsed, err := time.Parse(layout, t); err == nil {
				return &parsed
			}
		}
	}
	return nil
}

// DeleteForProfile erases one person's entire history.
//
// Reachable BOTH from that person's own token and from admin profile
// deletion: someone must always be able to erase themselves without
// asking the admin.
func (r *StatsRepository) DeleteForProfile(profileID string) (int64, error) {
	res, err := database.DB.Exec(
		`DELETE FROM stats_events WHERE profile_id = ?`, profileID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
