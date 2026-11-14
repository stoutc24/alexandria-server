package handlers

// A picked Plex person's addresses, kept current by the admin's app
// (owner, 2026-11-14). The admin's app reads its Plex servers' addresses
// from plex.tv with the admin's own Plex sign-in, and when they differ
// from what is saved here, hands them over for every person picked from
// that server. Only addresses proven to be that server are kept
// (repository.ReplacePlexConnections); nobody's key is read or written.

import (
	"errors"
	"log"

	"github.com/gofiber/fiber/v2"

	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

// maxPlexConnectionsBody bounds the work one request can ask for: plex.tv
// lists a handful per server, and at most a dozen are kept.
const maxPlexConnectionsBody = 64

// PlexServers is every Plex server people were picked from, with the
// addresses saved for it: {servers: [{serverId, serverName, picks,
// saved: [[{uri, local?, relay?}]]}]}. No secret.
func (h *AccountsHandler) PlexServers(c *fiber.Ctx) error {
	servers, err := h.accounts.PlexServers()
	if err != nil {
		return serverError(c, "could not read the Plex servers", err)
	}
	return c.JSON(fiber.Map{"servers": servers})
}

type plexConnectionsBody struct {
	Connections []repository.PlexConnection `json:"connections"`
}

// UpdatePlexConnections replaces the saved addresses of every person picked
// from one Plex server: {connections: [{uri, local?, relay?}]} →
// {updated, skipped}. Addresses not proven to be that server are skipped;
// a list that changes nothing writes nothing.
func (h *AccountsHandler) UpdatePlexConnections(c *fiber.Ctx) error {
	var body plexConnectionsBody
	if err := c.BodyParser(&body); err != nil {
		return refuse(c, fiber.StatusBadRequest, "bad-request", "invalid request body")
	}
	if len(body.Connections) == 0 || len(body.Connections) > maxPlexConnectionsBody {
		return refuse(c, fiber.StatusBadRequest, "bad-service",
			"send between 1 and 64 addresses")
	}
	serverID := c.Params("serverId")
	res, err := h.accounts.ReplacePlexConnections(serverID, body.Connections, h.now())
	switch {
	case errors.Is(err, repository.ErrPlexServerNotFound):
		return refuse(c, fiber.StatusNotFound, "plex-server-not-found", err.Error())
	case errors.Is(err, repository.ErrBadPlex):
		return refuse(c, fiber.StatusBadRequest, "bad-service",
			"none of these addresses is proven to be that Plex server")
	case err != nil:
		return serverError(c, "could not save the Plex addresses", err)
	}
	if res.Updated > 0 || res.Skipped > 0 {
		// The server's id and counts only: addresses carry the home's IP.
		log.Printf("plex addresses for server %s: %d pick(s) updated, %d address(es) skipped%s",
			shortID(serverID), res.Updated, res.Skipped, callerForLog(c))
	}
	return c.JSON(res)
}

// shortID is the first characters of an id, enough to tell servers apart
// in a log.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8] + "…"
	}
	return id
}
