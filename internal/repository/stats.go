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
	// ClientEventID makes ingest idempotent. It must be stable for the
	// same real-world event forever, and unique across a person's
	// devices — otherwise a retry, or a phone and a watch reporting the
	// same session, silently doubles someone's hours.
	//
	// Optional: an event without one is still accepted, just not
	// deduplicated.
	ClientEventID string    `json:"clientEventId,omitempty"`
	Source        string    `json:"source,omitempty"`
	ItemID        string    `json:"itemId,omitempty"`
	ContentKey    string    `json:"contentKey,omitempty"`
	MediaType     string    `json:"mediaType,omitempty"`
	Title         string    `json:"title,omitempty"`
	Author        string    `json:"author,omitempty"`
	EventType     string    `json:"eventType"`
	Seconds       int       `json:"seconds"`
	OccurredAt    time.Time `json:"occurredAt"`
}

// StatsSummary is a person's own rollup, computed in SQL.
//
// Finished counts every completion. DistinctWorksFinished counts the
// WORKS behind them, and Rereads is the difference — so someone who read
// one book five times has read one book and reread it four times, rather
// than appearing to have read five. That distinction is the honest one,
// and it is what makes a year-in-review able to say something true.
type StatsSummary struct {
	TotalEvents           int              `json:"totalEvents"`
	TotalSeconds          int64            `json:"totalSeconds"`
	Finished              int              `json:"finished"`
	DistinctWorksFinished int              `json:"distinctWorksFinished"`
	Rereads               int              `json:"rereads"`
	FirstEventAt          *time.Time       `json:"firstEventAt,omitempty"`
	LastEventAt           *time.Time       `json:"lastEventAt,omitempty"`
	SecondsByType         map[string]int64 `json:"secondsByType"`
	TopAuthors            []AuthorTotal    `json:"topAuthors"`
	MostReread            []RereadTotal    `json:"mostReread"`
}

// RereadTotal is one work and how many times it was finished. Times is
// the total reads, so Times-1 is how often it was returned to.
type RereadTotal struct {
	Title  string `json:"title"`
	Author string `json:"author"`
	Times  int    `json:"times"`
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
//
// Ingest is classifying, not blindly appending: see ingest.go for why a
// stable client id alone is not enough once more than one device
// reports. Returns a breakdown so the caller can tell a real sync from
// a replay, and can surface a device whose clock is wrong instead of
// silently dropping its data.
func (r *StatsRepository) AppendEvents(
	profileID string,
	events []Event,
) (IngestResult, error) {
	var result IngestResult

	if len(events) > maxEventsPerBatch {
		return result, errors.New("too many events in one batch")
	}

	tx, err := database.DB.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	insert, err := tx.Prepare(`
		INSERT OR IGNORE INTO stats_events
			(id, profile_id, client_event_id, source, item_id, content_key,
			 media_type, title, author, event_type, seconds, occurred_at,
			 occurred_at_unix)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return result, err
	}
	defer insert.Close()

	now := time.Now().UTC()

	for _, e := range events {
		if e.EventType == "" {
			e.EventType = "progress"
		}
		if e.OccurredAt.IsZero() {
			e.OccurredAt = now
		}
		if e.Seconds < 0 {
			e.Seconds = 0
		}

		rejected, backfilled := classify(e, now)
		if rejected {
			result.Rejected++
			continue
		}

		// A second "finished" for a work just finished is the same
		// finish seen twice, not a reread. Counting it would inflate
		// both books-read and rereads at once.
		if dup, err := completionAlreadyRecorded(tx, profileID, e); err != nil {
			return result, err
		} else if dup {
			result.Overlapped++
			continue
		}

		// Same activity already recorded, by this device or another one?
		existingID, existingSecs, found, err := overlapping(tx, profileID, e)
		if err != nil {
			return result, err
		}
		if found {
			// Two reports of one session. Keep the fuller account: a
			// watch that saw twenty-five minutes and a phone that saw
			// twenty are describing the same listening, and the longer
			// reading is the more complete one — not something to add.
			if e.Seconds > existingSecs {
				if _, err := tx.Exec(
					`UPDATE stats_events SET seconds = ? WHERE id = ?`,
					e.Seconds, existingID,
				); err != nil {
					return result, err
				}
				result.Extended++
			} else {
				result.Overlapped++
			}
			continue
		}

		res, err := insert.Exec(
			uuid.New().String(), profileID, nullIfEmpty(e.ClientEventID),
			e.Source, e.ItemID, nullIfEmpty(e.ContentKey), e.MediaType,
			e.Title, e.Author, e.EventType, e.Seconds, e.OccurredAt.UTC(),
			e.OccurredAt.UTC().Unix(),
		)
		if err != nil {
			return result, err
		}
		// INSERT OR IGNORE swallowed it: the client id has been seen
		// before, so this is a retry rather than new activity.
		if n, _ := res.RowsAffected(); n == 0 {
			result.Duplicates++
			continue
		}

		result.Accepted++
		if backfilled {
			result.Backfilled++
		}
	}

	if err := tx.Commit(); err != nil {
		return result, err
	}
	return result, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Summarize builds one profile's own rollup.
func (r *StatsRepository) Summarize(profileID string) (*StatsSummary, error) {
	s := &StatsSummary{
		SecondsByType: map[string]int64{},
		TopAuthors:    []AuthorTotal{},
		MostReread:    []RereadTotal{},
	}

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

	// Works finished, and how many of those finishes were returns to
	// something already read. Identity is content_key where the item
	// could be keyed across services, so finishing a book on Plex and
	// again on Audiobookshelf is one work, reread — not two books.
	err = database.DB.QueryRow(`
		SELECT COUNT(*) FROM (
			SELECT COALESCE(content_key, source || ':' || item_id) AS work
			FROM stats_events
			WHERE profile_id = ? AND event_type = 'finished'
			GROUP BY work
		)
	`, profileID).Scan(&s.DistinctWorksFinished)
	if err != nil {
		return nil, err
	}
	s.Rereads = s.Finished - s.DistinctWorksFinished
	if s.Rereads < 0 {
		s.Rereads = 0
	}

	rereadRows, err := database.DB.Query(`
		SELECT MAX(title), MAX(author), COUNT(*) AS times
		FROM stats_events
		WHERE profile_id = ? AND event_type = 'finished'
		GROUP BY COALESCE(content_key, source || ':' || item_id)
		HAVING times > 1
		ORDER BY times DESC, MAX(title)
		LIMIT 10
	`, profileID)
	if err != nil {
		return nil, err
	}
	for rereadRows.Next() {
		var rt RereadTotal
		if err := rereadRows.Scan(&rt.Title, &rt.Author, &rt.Times); err != nil {
			rereadRows.Close()
			return nil, err
		}
		s.MostReread = append(s.MostReread, rt)
	}
	rereadRows.Close()
	if err := rereadRows.Err(); err != nil {
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
