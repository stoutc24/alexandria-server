package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// StatsHandler serves a person their OWN numbers.
//
// Every route derives the profile from the caller's token via
// middleware.ProfileID. None of them accepts a profile id in the path,
// the query, or the body — if they did, one token would be able to read
// or write another person's history just by naming them.
type StatsHandler struct {
	stats *repository.StatsRepository
}

func NewStatsHandler(stats *repository.StatsRepository) *StatsHandler {
	return &StatsHandler{stats: stats}
}

type AppendEventsRequest struct {
	Events []repository.Event `json:"events"`
}

// AppendEvents records listening/reading activity for the caller.
func (h *StatsHandler) AppendEvents(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		// Unreachable behind RequireProfile; a guard against someone
		// later mounting this route under weaker middleware.
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}

	var req AppendEventsRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}
	if len(req.Events) == 0 {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "events is required",
		})
	}

	result, err := h.stats.AppendEvents(profileID, req.Events)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": err.Error(),
		})
	}

	// The breakdown matters to the caller: "written: 0" could mean a
	// successful replay, a batch of overlaps already covered by another
	// device, or a device whose clock is wrong and whose data is being
	// thrown away. Those need telling apart.
	return c.JSON(fiber.Map{
		"written":    result.Accepted, // kept for older clients
		"accepted":   result.Accepted,
		"duplicates": result.Duplicates,
		"overlapped": result.Overlapped,
		"extended":   result.Extended,
		"rejected":   result.Rejected,
		"backfilled": result.Backfilled,
	})
}

// GetSummary returns the caller's own rollup.
func (h *StatsHandler) GetSummary(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}

	summary, err := h.stats.Summarize(profileID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to summarize stats",
		})
	}
	return c.JSON(summary)
}

// DeleteOwnStats erases the caller's history.
//
// Reachable with a person's OWN token on purpose: erasing yourself must
// not require asking the admin for permission.
func (h *StatsHandler) DeleteOwnStats(c *fiber.Ctx) error {
	profileID := middleware.ProfileID(c)
	if profileID == "" {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
			"error": "a profile token is required",
		})
	}

	deleted, err := h.stats.DeleteForProfile(profileID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete stats",
		})
	}
	return c.JSON(fiber.Map{"deleted": deleted})
}
