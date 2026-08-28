package handlers

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/catalog"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// CatalogHandler serves the household's merged library index.
//
// Every route here answers a FACTUAL question — what is in the library,
// which rows are the same work, how big is it. None of them returns an
// entitlement, a tier, or an "allowed" flag, and the app must never
// infer permission from any of them. See docs/COMPANION_DESIGN.md:
// the server may do work; it must never grant permission.
type CatalogHandler struct {
	catalogRepo *repository.CatalogRepository
}

func NewCatalogHandler(catalogRepo *repository.CatalogRepository) *CatalogHandler {
	return &CatalogHandler{catalogRepo: catalogRepo}
}

// maxSyncItems caps one sync REQUEST, not a library.
//
// 20,000 rows is roughly 4 MB of JSON, which is exactly Fiber's default
// body limit — a library that size was being refused with a 413 before
// this handler ever ran. Large libraries now arrive in chunks: the first
// replaces the source, the rest append.
const maxSyncItems = 5000

type SyncCatalogRequest struct {
	Source string         `json:"source"`
	Items  []catalog.Item `json:"items"`
	// Mode is "replace" (the default) or "append". A library too large
	// for one request sends its first chunk as replace and the rest as
	// append, so the source is swapped atomically-enough without ever
	// holding the whole thing in memory.
	Mode string `json:"mode,omitempty"`
}

type SyncCatalogResponse struct {
	Source  string `json:"source"`
	Written int    `json:"written"`
}

// SyncSource replaces the stored snapshot for one media server.
//
// Replace, not merge: an item removed from the media server has to
// leave the index too. This rewrites only this server's own cache rows
// for that one source — it never touches media on a media server.
func (h *CatalogHandler) SyncSource(c *fiber.Ctx) error {
	var req SyncCatalogRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if req.Source == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "source is required",
		})
	}
	if len(req.Items) > maxSyncItems {
		return c.Status(fiber.StatusRequestEntityTooLarge).JSON(fiber.Map{
			"error": "too many items in one request; send at most " +
				strconv.Itoa(maxSyncItems) + " and continue with " +
				`mode:"append"`,
		})
	}

	var written int
	var err error
	if req.Mode == "append" {
		written, err = h.catalogRepo.AppendToSource(req.Source, req.Items)
	} else {
		written, err = h.catalogRepo.ReplaceSource(req.Source, req.Items)
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to store catalog",
		})
	}

	return c.JSON(SyncCatalogResponse{Source: req.Source, Written: written})
}

type ItemsResponse struct {
	Groups []catalog.Group `json:"groups"`
	Count  int             `json:"count"`
}

// GetItems returns the library already collapsed across servers, so the
// phone renders groups instead of deriving them.
func (h *CatalogHandler) GetItems(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 200)
	if limit < 1 || limit > 1000 {
		limit = 200
	}

	items, err := h.catalogRepo.GetItems(repository.ItemQuery{
		MediaType: c.Query("type"),
		Source:    c.Query("source"),
		Limit:     limit,
		Offset:    c.QueryInt("offset", 0),
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read catalog",
		})
	}

	// `prefer` lets the caller say which server it can actually play
	// from, so the representative copy stays usable there.
	var prefer []string
	if p := c.Query("prefer"); p != "" {
		prefer = append(prefer, p)
	}

	groups := catalog.Collapse(items, prefer)
	return c.JSON(ItemsResponse{Groups: groups, Count: len(groups)})
}

// GetDuplicates lists works held on more than one server, so the app can
// say "3 copies across your servers" honestly.
//
// It REPORTS duplicates. It does not delete anything, and there is no
// route here that does.
func (h *CatalogHandler) GetDuplicates(c *fiber.Ctx) error {
	limit := c.QueryInt("limit", 200)
	if limit < 1 || limit > 1000 {
		limit = 200
	}

	keys, err := h.catalogRepo.GetDuplicateKeys(limit)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read catalog",
		})
	}

	byKey, err := h.catalogRepo.GetByContentKeys(keys)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to read catalog",
		})
	}

	groups := make([]catalog.Group, 0, len(keys))
	for _, k := range keys {
		copies := byKey[k]
		if len(copies) == 0 {
			continue
		}
		groups = append(groups, catalog.Group{
			Key:            k,
			Representative: copies[0],
			Copies:         copies,
		})
	}

	return c.JSON(ItemsResponse{Groups: groups, Count: len(groups)})
}

// GetSummary is the rollup the phone currently rebuilds every launch.
func (h *CatalogHandler) GetSummary(c *fiber.Ctx) error {
	summary, err := h.catalogRepo.Summarize()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to summarize catalog",
		})
	}
	return c.JSON(summary)
}
