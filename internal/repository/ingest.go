package repository

import (
	"database/sql"
	"time"
)

// Ingest rules for activity arriving from several devices.
//
// Exact-duplicate detection (same client event id) only catches a retry
// or a replay. It does nothing about the harder case: a phone and a
// watch INDEPENDENTLY recording the same twenty minutes, each with its
// own local row id. Different ids, so both would land, and the person's
// total would double. A device that has been offline for a month
// dumping its backlog is the same problem at scale.
//
// What makes it tractable: you cannot listen to the same book twice at
// the same time. Two events for the same item whose time ranges overlap
// are the same real activity, whoever reported them.
//
// So ingest classifies rather than blindly appends:
//
//   duplicate  — same client event id; ignore
//   overlap    — same item, overlapping time; already counted
//   extended   — same activity, but this report is longer; keep the longer
//   rejected   — implausible; a bad clock or a runaway counter
//   backfilled — genuinely old; counted, but flagged so the app does not
//                mistake a month-old import for activity happening now
//   accepted   — new

// Bounds for a plausible event. These exist because one device with a
// broken clock or a runaway timer can poison a person's totals in a way
// that is tedious to unpick afterwards.
const (
	// A single event longer than a day is a counter that ran away, not
	// someone listening. Real sessions are minutes to hours.
	maxEventSeconds = 24 * 60 * 60

	// Clock skew is normal; a device claiming to report from next week
	// is not. Two days is generous for timezone and drift mistakes.
	maxFutureSkew = 48 * time.Hour

	// Anything older than this is treated as backfill: still counted,
	// but not evidence of current activity.
	backfillAge = 7 * 24 * time.Hour
)

// earliestPlausible guards against a device whose clock reset to the
// epoch, which would otherwise anchor someone's history in 1970.
var earliestPlausible = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)

// IngestResult explains what happened to a batch, so a client can tell a
// real sync from a replay and surface a device with a broken clock
// instead of silently discarding its data.
type IngestResult struct {
	Accepted   int `json:"accepted"`
	Duplicates int `json:"duplicates"`
	Overlapped int `json:"overlapped"`
	Extended   int `json:"extended"`
	Rejected   int `json:"rejected"`
	Backfilled int `json:"backfilled"`
}

// Total is how many events were examined.
func (r IngestResult) Total() int {
	return r.Accepted + r.Duplicates + r.Overlapped + r.Extended + r.Rejected
}

// classify decides whether an event is worth storing at all.
//
// Returns a reason when the event should be rejected outright.
func classify(e Event, now time.Time) (rejected bool, backfilled bool) {
	if e.Seconds > maxEventSeconds {
		return true, false
	}
	if e.OccurredAt.After(now.Add(maxFutureSkew)) {
		return true, false
	}
	if e.OccurredAt.Before(earliestPlausible) {
		return true, false
	}
	return false, e.OccurredAt.Before(now.Add(-backfillAge))
}

// overlapping finds an existing event for the same content whose time
// range intersects this one.
//
// Identity is content_key when the item could be keyed across services —
// so finishing the same album on Plex and on Navidrome is recognised as
// one thing — and falls back to source+item_id when it could not.
//
// The comparison is integer epoch seconds, deliberately. An earlier
// version compared SQLite text dates and matched nothing at all,
// silently, because the driver's stored format did not match the
// literal being compared against. Arithmetic on instants belongs on
// integers.
func overlapping(
	tx *sql.Tx,
	profileID string,
	e Event,
) (id string, seconds int, ok bool, err error) {
	// A zero-length event has no range to overlap with. Completions are
	// points in time and are handled by completionAlreadyRecorded.
	if e.Seconds <= 0 {
		return "", 0, false, nil
	}

	start := e.OccurredAt.UTC().Unix()
	end := start + int64(e.Seconds)

	var row *sql.Row
	if e.ContentKey != "" {
		row = tx.QueryRow(`
			SELECT id, seconds FROM stats_events
			WHERE profile_id = ? AND content_key = ? AND seconds > 0
			  AND occurred_at_unix IS NOT NULL
			  AND occurred_at_unix < ?
			  AND occurred_at_unix + seconds > ?
			ORDER BY seconds DESC LIMIT 1
		`, profileID, e.ContentKey, end, start)
	} else if e.ItemID != "" {
		row = tx.QueryRow(`
			SELECT id, seconds FROM stats_events
			WHERE profile_id = ? AND source = ? AND item_id = ? AND seconds > 0
			  AND occurred_at_unix IS NOT NULL
			  AND occurred_at_unix < ?
			  AND occurred_at_unix + seconds > ?
			ORDER BY seconds DESC LIMIT 1
		`, profileID, e.Source, e.ItemID, end, start)
	} else {
		// Nothing to identify it by; treat as new rather than guess.
		return "", 0, false, nil
	}

	err = row.Scan(&id, &seconds)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return id, seconds, true, nil
}

// rereadGap is how far apart two completions of the same work must be
// before the second one counts as a genuine reread.
//
// Below it, a second "finished" is almost always the same finish seen
// twice — the phone and the watch both noticing, or a progress bar
// crossing the threshold either side of a sync. Nobody reads a book
// twice in a day; somebody rereading it does so weeks later.
const rereadGap = 20 * time.Hour

// completionAlreadyRecorded reports whether this work was already
// finished so recently that this is the same finish, not a reread.
//
// Getting this wrong in either direction spoils the statistic: too
// narrow and one finish becomes two reads, too wide and a genuine
// reread is swallowed.
func completionAlreadyRecorded(
	tx *sql.Tx,
	profileID string,
	e Event,
) (bool, error) {
	if e.EventType != "finished" {
		return false, nil
	}

	at := e.OccurredAt.UTC().Unix()
	lo := at - int64(rereadGap.Seconds())
	hi := at + int64(rereadGap.Seconds())

	var n int
	var err error
	if e.ContentKey != "" {
		err = tx.QueryRow(`
			SELECT COUNT(*) FROM stats_events
			WHERE profile_id = ? AND event_type = 'finished'
			  AND content_key = ?
			  AND occurred_at_unix BETWEEN ? AND ?
		`, profileID, e.ContentKey, lo, hi).Scan(&n)
	} else if e.ItemID != "" {
		err = tx.QueryRow(`
			SELECT COUNT(*) FROM stats_events
			WHERE profile_id = ? AND event_type = 'finished'
			  AND source = ? AND item_id = ?
			  AND occurred_at_unix BETWEEN ? AND ?
		`, profileID, e.Source, e.ItemID, lo, hi).Scan(&n)
	} else {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
