package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/trailers"
)

// TrailerService is what the trailer routes need from internal/trailers.
// An interface so the handler tests run against a fake — no yt-dlp, no
// ffmpeg, no deno, no network.
type TrailerService interface {
	Available() bool
	Prepare(ctx context.Context, id string) (trailers.Entry, error)
	Lookup(id string) (trailers.Entry, bool)
	Touch(id string)
}

// TrailerHandler serves movie and show trailers to the TV.
//
// Jellyfin hands the app a YouTube URL per title, and an Apple TV cannot
// play a YouTube page. The companion resolves that link into a real MP4
// (download + ffmpeg merge, see internal/trailers) and streams it.
//
// This is WORK, not permission, like everything else here: the routes
// only ever fetch a YouTube URL the app was given by a media server, and
// they answer factual questions — is it ready, here are the bytes. Any
// token may call them; a trailer is public content, not anyone's data.
type TrailerHandler struct {
	svc TrailerService
}

func NewTrailerHandler(svc TrailerService) *TrailerHandler {
	return &TrailerHandler{svc: svc}
}

const (
	// How long GET /prepare may hold a request open waiting for the
	// download, in seconds. The default covers a typical trailer on a
	// home connection; the cap keeps a slow one from pinning a request
	// forever — past it the TV gets a 202 and polls.
	//
	// 90 s sits above the server's 60 s WriteTimeout on purpose: fasthttp
	// arms the write deadline AFTER the handler returns (server.go,
	// serveConn), so it bounds the response write, not the handler.
	defaultPrepareWait = 45
	maxPrepareWait     = 90
	// prepareRetryAfter is how soon a 202 asks the TV to come back.
	prepareRetryAfter = 5
)

func streamPath(id string) string { return "/api/v1/trailers/stream/" + id }

// Prepare makes a trailer playable and says where to stream it from.
//
// GET /api/v1/trailers/prepare?url=<youtube url>&wait=<seconds>
//
//	200 {id, ready:true, streamPath, title, durationSec, bytes}
//	202 {id, ready:false, retryAfterSec}   still downloading; poll again
//	400 the URL is not a YouTube video link
//	501 {error, reason:"tool_missing"}     yt-dlp/ffmpeg/deno not installed
//	502 {error, reason}                    the download failed; reason is
//	    "unavailable" (YouTube says so), "timeout", or "failed"
//
// The reason is the contract the TV branches on: every non-200 is a cue
// to fall back to the YouTube app rather than show an error.
func (h *TrailerHandler) Prepare(c *fiber.Ctx) error {
	id, err := trailers.ExtractVideoID(c.Query("url"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "url must be a YouTube video link (youtube.com/watch?v=…, youtu.be/…, /embed/…, /shorts/…)",
		})
	}
	if !h.svc.Available() {
		return c.Status(fiber.StatusNotImplemented).JSON(fiber.Map{
			"error":  "trailer tools (yt-dlp, ffmpeg, deno) are not installed on this companion",
			"reason": trailers.ReasonToolMissing,
		})
	}

	wait := c.QueryInt("wait", defaultPrepareWait)
	if wait < 0 {
		wait = 0
	}
	if wait > maxPrepareWait {
		wait = maxPrepareWait
	}
	ctx, cancel := context.WithTimeout(c.Context(), time.Duration(wait)*time.Second)
	defer cancel()

	entry, err := h.svc.Prepare(ctx, id)
	if err == nil {
		return c.JSON(fiber.Map{
			"id":          entry.ID,
			"ready":       true,
			"streamPath":  streamPath(entry.ID),
			"title":       entry.Title,
			"durationSec": entry.DurationSec,
			"bytes":       entry.Bytes,
		})
	}
	if errors.Is(err, trailers.ErrInProgress) {
		c.Set(fiber.HeaderRetryAfter, strconv.Itoa(prepareRetryAfter))
		return c.Status(fiber.StatusAccepted).JSON(fiber.Map{
			"id":            id,
			"ready":         false,
			"retryAfterSec": prepareRetryAfter,
		})
	}
	var te *trailers.Error
	if errors.As(err, &te) {
		status := fiber.StatusBadGateway
		if te.Reason == trailers.ReasonToolMissing {
			status = fiber.StatusNotImplemented
		}
		return c.Status(status).JSON(fiber.Map{"error": te.Msg, "reason": te.Reason})
	}
	return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{
		"error":  err.Error(),
		"reason": trailers.ReasonFailed,
	})
}

// Stream serves a prepared trailer's MP4.
//
// GET /api/v1/trailers/stream/:id
//
// Range requests are honoured (206 + Content-Range + Accept-Ranges):
// AVPlayer probes and seeks with Range and will not play a file from a
// server that answers 200 to every byte range. The id must match the
// YouTube id rule before the disk is touched, so nothing that looks
// like a path can reach the filesystem.
//
// The TV sends its bearer token on this request via AVURLAsset's header
// options, so the token middleware applies here exactly as elsewhere.
// Do NOT add a query-string token: a URL with a credential in it ends
// up in access logs and screenshots.
func (h *TrailerHandler) Stream(c *fiber.Ctx) error {
	id := c.Params("id")
	if !trailers.ValidID(id) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "invalid trailer id"})
	}
	entry, ok := h.svc.Lookup(id)
	if !ok {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "trailer is not cached; call /api/v1/trailers/prepare first",
		})
	}
	// Serving counts as use: eviction is oldest-mtime-first.
	h.svc.Touch(id)

	// fasthttp's file server does the byte-range work (206, 416,
	// Accept-Ranges, HEAD). It also picks the Content-Type, by extension
	// or by sniffing — pin it, so a client that trusts the header over
	// the bytes is never told anything but video/mp4.
	if err := c.SendFile(entry.Path); err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "trailer file is gone; call prepare again",
		})
	}
	switch c.Response().StatusCode() {
	case fiber.StatusOK, fiber.StatusPartialContent:
		c.Set(fiber.HeaderContentType, "video/mp4")
	case fiber.StatusRequestedRangeNotSatisfiable:
		// RFC 9110: a 416 says how big the thing actually is.
		c.Set(fiber.HeaderContentRange, fmt.Sprintf("bytes */%d", entry.Bytes))
	}
	return nil
}
