package middleware

import (
	"strings"
	"time"

	"github.com/valyala/fasthttp"

	"github.com/stoutsolutions/alexandria-server/internal/adminauth"
	"github.com/stoutsolutions/alexandria-server/internal/config"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// SmallBodyLimit is the largest request body taken from a caller who has
// shown no credential this server knows. The sign-ins, an invite and a link
// request are a few hundred bytes each; 16 KiB is room to spare.
const SmallBodyLimit = 16 * 1024

// BodyCap is the web server's "headers are in" hook: it decides how large a
// body this request may send BEFORE any of the body is read (security
// review, 2026-11-10).
//
// The server reads a body into memory, and makes room for the whole
// declared Content-Length the moment the headers arrive — up to the body
// limit, 8 MiB, which the catalogue sync needs. That happened on every
// route, before any sign-in: forty connections that each declared 8 MiB and
// then sent nothing held about 290 MB for a minute, on a container capped
// at 256.
//
// So the full limit is for callers who are somebody: a linked device's
// token, a signed-in admin (cookie, a phone's sign-in, or the environment
// token). Anyone else gets SmallBodyLimit, and a larger declaration is
// refused (413) without a byte of room being made. A body no larger than
// that needs no decision, so the lookup below runs only for big requests.
func BodyCap(
	cfg *config.Config,
	profiles *repository.ProfileRepository,
	auth *repository.AdminAuthRepository,
) func(*fasthttp.RequestHeader) fasthttp.RequestConfig {
	return func(h *fasthttp.RequestHeader) fasthttp.RequestConfig {
		n := h.ContentLength()
		// -2 is no body at all; -1 is chunked, its size not yet known.
		if n == -2 || (n >= 0 && n <= SmallBodyLimit) {
			return fasthttp.RequestConfig{}
		}
		if holdsCredential(h, cfg, profiles, auth) {
			return fasthttp.RequestConfig{}
		}
		return fasthttp.RequestConfig{MaxRequestBodySize: SmallBodyLimit}
	}
}

// holdsCredential reports whether the request's headers carry a credential
// this server would accept somewhere. It decides room for a body, nothing
// else: every route still checks its own way in.
func holdsCredential(
	h *fasthttp.RequestHeader,
	cfg *config.Config,
	profiles *repository.ProfileRepository,
	auth *repository.AdminAuthRepository,
) bool {
	if raw := h.Cookie(AdminSessionCookie); len(raw) > 0 {
		if ok, err := auth.SessionValid(adminauth.HashToken(string(raw))); err == nil && ok {
			return true
		}
	}
	parts := strings.SplitN(string(h.Peek("Authorization")), " ", 2)
	if len(parts) != 2 || parts[0] != "Bearer" {
		return false
	}
	token := parts[1]
	if isAdminToken(cfg, token) {
		return true
	}
	if strings.HasPrefix(token, adminauth.DeviceTokenPrefix) {
		_, state, err := auth.LookupDeviceToken(adminauth.HashToken(token), time.Now())
		return err == nil && state == repository.DeviceTokenValid
	}
	_, err := profiles.FindByToken(token)
	return err == nil
}
