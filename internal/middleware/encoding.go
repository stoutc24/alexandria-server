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
func RefuseEncodedBodies(c *fiber.Ctx) error {
	enc := strings.TrimSpace(c.Get(fiber.HeaderContentEncoding))
	if enc == "" || strings.EqualFold(enc, "identity") {
		return c.Next()
	}
	return c.Status(fiber.StatusUnsupportedMediaType).JSON(fiber.Map{
		"error":  "compressed request bodies are not accepted",
		"reason": "encoded-body",
	})
}
