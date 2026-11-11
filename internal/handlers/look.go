package handlers

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// maxLookBody is the largest look change taken: six fields of a word each
// are about 150 bytes, so 2 KiB is room to spare and anything larger is not
// a look. Larger is 413, as every other size limit here answers.
const maxLookBody = 2 * 1024

// plainLookKey is a key an error may name back: short, letters, digits and
// "_" only. Anything else is described, never repeated.
var plainLookKey = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

// SetLook changes the calling device's own person's look (owner,
// 2026-11-11): PUT /api/v1/devices/me/look, advertised as "profiles.look".
//
//	{"color": "teal", "ambiance": "rain", "font": ""}
//
// Any of the six fields (repository.Look), each a string from its list, or
// "" to clear it back to the app's default. The fields sent are merged into
// the stored look; the fields left out stay as they are. The answer is the
// whole look as it now stands, the same shape GET /devices/me carries:
//
//	{"look": {"color": "teal", "ambiance": "rain"}, "lookUpdatedAt": 1762876800123}
//
// There is no person in the path: the token says whose look this is, so a
// device can change its own person's look and no one else's. Refused, with
// nothing written: a body that is not a JSON object, a key that is not a
// field, a value that is not a string or not on its list (400, naming the
// field), and a body over 2 KiB (413).
//
// Behind requireProfile: a link token only, like everything a device does.
func (h *LinkHandler) SetLook(c *fiber.Ctx) error {
	deviceID, _ := middleware.Device(c)
	profileID := middleware.ProfileID(c)
	if deviceID == "" || profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a device token is required",
		})
	}

	// The body as sent: a compressed one never gets this far
	// (middleware.RefuseEncodedBodies).
	raw := c.Request().Body()
	if len(raw) > maxLookBody {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": "a look is at most 2 KB",
		})
	}
	change, refusal := parseLookChange(raw)
	if refusal != "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": refusal})
	}

	look, at, err := h.profiles.MergeLook(profileID, change, time.Now())
	if errors.Is(err, repository.ErrBadLook) {
		// parseLookChange checks the same lists; this is the backstop.
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "not a look this server knows",
		})
	}
	if err != nil {
		log.Printf("look: could not keep a look change: %v", err)
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "try again in a moment",
		})
	}
	return c.JSON(fiber.Map{"look": look, "lookUpdatedAt": at})
}

// parseLookChange reads a look change: a JSON object whose every key is a
// field of a look and whose every value is a string on that field's list,
// or "". It answers the change, or a refusal naming the first bad field in
// key order (so the same body always gets the same answer). A refusal
// never repeats a value, and repeats a key only when plainLookKey allows.
func parseLookChange(raw []byte) (map[string]string, string) {
	notAnObject := "send a JSON object of look fields: " +
		strings.Join(repository.LookFieldNames(), ", ")
	body := bytes.TrimSpace(raw)
	// "null" decodes into a map without an error, so the shape is checked
	// first.
	if len(body) == 0 || body[0] != '{' {
		return nil, notAnObject
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, notAnObject
	}

	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	change := make(map[string]string, len(fields))
	for _, key := range keys {
		values := repository.LookValues(key)
		if values == nil {
			return nil, unknownLookField(key)
		}
		// A string, and only a string: null, a number, true or an object
		// is refused, not read as "".
		v := bytes.TrimSpace(fields[key])
		var s string
		if len(v) == 0 || v[0] != '"' || json.Unmarshal(v, &s) != nil {
			return nil, fmt.Sprintf("%q must be a string", key)
		}
		if s != "" && !lookValueIn(values, s) {
			return nil, fmt.Sprintf(`%q must be one of %s, or "" for the app's default`,
				key, strings.Join(values, ", "))
		}
		change[key] = s
	}
	return change, ""
}

func lookValueIn(values []string, s string) bool {
	for _, v := range values {
		if v == s {
			return true
		}
	}
	return false
}

// unknownLookField is the refusal for a key that is not a field of a look.
func unknownLookField(key string) string {
	fields := strings.Join(repository.LookFieldNames(), ", ")
	if plainLookKey.MatchString(key) {
		return fmt.Sprintf("%q is not a look field; the fields are %s", key, fields)
	}
	return "a key in the body is not a look field; the fields are " + fields
}
