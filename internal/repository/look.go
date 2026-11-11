package repository

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"

	"github.com/stoutsolutions/alexandria-server/internal/database"
)

// ErrBadLook: a look change named a field, or a value, that is not on the
// lists. The handler says which before it gets here; this is the
// repository refusing to store anything off the lists all the same.
var ErrBadLook = errors.New("not a look this server knows")

// Look is how a person's apps look on every device linked to them (owner,
// 2026-11-11): the color of their profile's circle, the accent on buttons
// and focus, the page background, what is behind Now Playing, the font and
// the text size. Each field holds one value from its list (LookValues) or
// is empty, which means the app's own default, and an empty field is left
// out of the JSON. Nothing else is ever stored or sent: no free text, so
// nothing anyone types can reach another screen through a look.
type Look struct {
	Color      string `json:"color,omitempty"`
	Accent     string `json:"accent,omitempty"`
	Background string `json:"background,omitempty"`
	Ambiance   string `json:"ambiance,omitempty"`
	Font       string `json:"font,omitempty"`
	TextSize   string `json:"textSize,omitempty"`
}

// lookColors are the profile colors. The accent takes the same twelve, or
// "default".
var lookColors = []string{
	"red", "orange", "amber", "gold", "green", "teal",
	"cyan", "blue", "indigo", "purple", "pink", "slate",
}

// lookFields is every field of a look, in the order the API documents
// them, with the values each may take. "" (the app's default) is valid for
// every field and is not listed.
var lookFields = []struct {
	name   string
	values []string
}{
	{"color", lookColors},
	{"accent", append(append([]string{}, lookColors...), "default")},
	{"background", []string{"midnight", "black", "charcoal", "forest", "plum", "ocean"}},
	{"ambiance", []string{"none", "coverColors", "fireplace", "rain", "snowfall", "library", "starfield"}},
	{"font", []string{"default", "rounded", "serif"}},
	{"textSize", []string{"normal", "large", "extraLarge"}},
}

// LookFieldNames is every field of a look, in order.
func LookFieldNames() []string {
	out := make([]string, 0, len(lookFields))
	for _, f := range lookFields {
		out = append(out, f.name)
	}
	return out
}

// LookValues is the values a field may take, in order, without "". Nil for
// a name that is not a field of a look.
func LookValues(field string) []string {
	for _, f := range lookFields {
		if f.name == field {
			return append([]string{}, f.values...)
		}
	}
	return nil
}

// validLookValue reports whether value may be kept in field: "" (the app's
// default) or one of its values.
func validLookValue(field, value string) bool {
	values := LookValues(field)
	if values == nil {
		return false
	}
	if value == "" {
		return true
	}
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// slot is where field lives in l; nil for a name that is not a field.
func (l *Look) slot(field string) *string {
	switch field {
	case "color":
		return &l.Color
	case "accent":
		return &l.Accent
	case "background":
		return &l.Background
	case "ambiance":
		return &l.Ambiance
	case "font":
		return &l.Font
	case "textSize":
		return &l.TextSize
	}
	return nil
}

// clean is l without any value that is off its list — what a look stored
// by a newer server, one that knows more values, reads as here. Only values
// on the lists ever leave this server.
func (l Look) clean() Look {
	var out Look
	for _, f := range lookFields {
		if v := *l.slot(f.name); validLookValue(f.name, v) {
			*out.slot(f.name) = v
		}
	}
	return out
}

// decodeLook reads a stored look. Stored text that is not a look (never
// written by this server) reads as no look at all, the apps' defaults,
// rather than failing every read of the person: a look is cosmetic, and
// Kids mode beside it on GET /devices/me is not. The next change replaces
// it.
func decodeLook(raw string) Look {
	var l Look
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		log.Printf("look: a stored look could not be read, so the defaults stand in: %v", err)
		return Look{}
	}
	return l.clean()
}

// GetLook reads a person's look and when it last changed, in unix
// milliseconds (0: never). ErrProfileNotFound when there is no such person.
func (r *ProfileRepository) GetLook(profileID string) (Look, int64, error) {
	var raw string
	var at int64
	err := database.DB.QueryRow(
		`SELECT look, look_updated_at FROM profiles WHERE id = ?`, profileID,
	).Scan(&raw, &at)
	if err == sql.ErrNoRows {
		return Look{}, 0, ErrProfileNotFound
	}
	if err != nil {
		return Look{}, 0, err
	}
	return decodeLook(raw), at, nil
}

// lookWrites keeps to one look change at a time. A change is a read, a
// merge and a write, not one statement, so two devices of one person
// changing different fields at the same moment could otherwise lose one of
// them. This process is the database's only writer.
var lookWrites sync.Mutex

// MergeLook applies a change to a person's look and returns the look and
// its time as they now stand. Each field in change is set to its value, or
// cleared by ""; every field it leaves out is kept. The time moves (to now,
// in unix milliseconds, and always past the last) only when the look
// actually changed, so a device can tell a change from a repeat.
//
// Any field or value off the lists is refused (ErrBadLook) and nothing is
// written, whatever the caller checked first.
func (r *ProfileRepository) MergeLook(
	profileID string, change map[string]string, now time.Time,
) (Look, int64, error) {
	for field, value := range change {
		if !validLookValue(field, value) {
			return Look{}, 0, ErrBadLook
		}
	}

	lookWrites.Lock()
	defer lookWrites.Unlock()

	look, at, err := r.GetLook(profileID)
	if err != nil {
		return Look{}, 0, err
	}
	next := look
	for field, value := range change {
		*next.slot(field) = value
	}
	if next == look {
		return look, at, nil
	}

	stamp := now.UnixMilli()
	if stamp <= at {
		stamp = at + 1
	}
	raw, err := json.Marshal(next)
	if err != nil {
		return Look{}, 0, err
	}
	res, err := database.DB.Exec(
		`UPDATE profiles SET look = ?, look_updated_at = ? WHERE id = ?`,
		string(raw), stamp, profileID)
	if err != nil {
		return Look{}, 0, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Look{}, 0, ErrProfileNotFound
	}
	return next, stamp, nil
}
