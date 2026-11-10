package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
)

// RefuseEncodedBodies answers 415 to a request whose body is compressed:
// any Content-Encoding but identity. Fiber unpacks a gzip, deflate or
// brotli body with no size limit the moment a handler reads it, and
// BodyLimit counts only the bytes as sent, so one small request — to the
// sign-in, before any token — could expand to gigabytes (security review,
// 2026-11-02: 65 KB became 64 MiB). No Alexandria app compresses what it
// sends. It goes in front of every route.
//
// EVERY Content-Encoding header is read, and every value in each (security
// review, 2026-11-10): Fiber unpacks by the LAST such header it visits, so
// a request that sent "identity" and then "gzip" on two lines passed a
// check of the first and was unpacked all the same — 49 KB took the
// process from 38 MB to 216 MB, with no credential.
func RefuseEncodedBodies(c *fiber.Ctx) error {
	encoded := false
	c.Request().Header.VisitAll(func(key, value []byte) {
		if encoded || !strings.EqualFold(string(key), fiber.HeaderContentEncoding) {
			return
		}
		for _, enc := range strings.Split(string(value), ",") {
			enc = strings.TrimSpace(enc)
			if enc != "" && !strings.EqualFold(enc, "identity") {
				encoded = true
				return
			}
		}
	})
	if !encoded {
		return c.Next()
	}
	return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
		"error":  "compressed request bodies are not accepted",
		"reason": "encoded-body",
	})
}
