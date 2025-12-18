package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/models"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

type MediaServerHandler struct {
	serverRepo *repository.MediaServerRepository
	familyRepo *repository.FamilyRepository
}

func NewMediaServerHandler(
	serverRepo *repository.MediaServerRepository,
	familyRepo *repository.FamilyRepository,
) *MediaServerHandler {
	return &MediaServerHandler{
		serverRepo: serverRepo,
		familyRepo: familyRepo,
	}
}

// Request types
type CreateServerRequest struct {
	Name string                 `json:"name"`
	Type models.MediaServerType `json:"type"`
	URL  string                 `json:"url"`
}

type UpdateServerRequest struct {
	Name string                 `json:"name"`
	Type models.MediaServerType `json:"type"`
	URL  string                 `json:"url"`
}

type GrantAccessRequest struct {
	UserID string `json:"userId"`
}

// CreateServer adds a new media server to a family (admin only)
func (h *MediaServerHandler) CreateServer(c *fiber.Ctx) error {
	familyID := c.Params("id")
	userID := middleware.GetUserID(c)

	// Check if user is admin
	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check permissions",
		})
	}
	if !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only family admins can add media servers",
		})
	}

	var req CreateServerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" || req.Type == "" || req.URL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name, type, and url are required",
		})
	}

	// Validate server type
	if !isValidServerType(req.Type) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid server type",
		})
	}

	server, err := h.serverRepo.Create(familyID, req.Name, req.Type, req.URL, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create media server",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(server)
}

// GetServers lists all media servers for a family
func (h *MediaServerHandler) GetServers(c *fiber.Ctx) error {
	familyID := c.Params("id")
	userID := middleware.GetUserID(c)

	// Check membership
	membership, err := h.familyRepo.GetMembership(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check membership",
		})
	}
	if membership == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "you are not a member of this family",
		})
	}

	servers, err := h.serverRepo.GetByFamily(familyID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get servers",
		})
	}

	// Build response with access info
	isAdmin := membership.Role == string(models.RoleAdmin)
	var response []fiber.Map

	for _, s := range servers {
		hasAccess, _ := h.serverRepo.HasAccess(s.ID, userID)

		serverMap := fiber.Map{
			"id":        s.ID,
			"familyId":  s.FamilyID,
			"name":      s.Name,
			"type":      s.Type,
			"url":       s.URL,
			"createdBy": s.CreatedBy,
			"createdAt": s.CreatedAt,
			"hasAccess": hasAccess || isAdmin,
		}

		response = append(response, serverMap)
	}

	if response == nil {
		response = []fiber.Map{}
	}

	return c.JSON(response)
}

// GetServer retrieves a single media server
func (h *MediaServerHandler) GetServer(c *fiber.Ctx) error {
	familyID := c.Params("id")
	serverID := c.Params("serverId")
	userID := middleware.GetUserID(c)

	// Check membership
	membership, err := h.familyRepo.GetMembership(familyID, userID)
	if err != nil || membership == nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "you are not a member of this family",
		})
	}

	server, err := h.serverRepo.GetByID(serverID)
	if err == repository.ErrServerNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get server",
		})
	}

	// Verify server belongs to family
	if server.FamilyID != familyID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}

	isAdmin := membership.Role == string(models.RoleAdmin)
	hasAccess, _ := h.serverRepo.HasAccess(serverID, userID)

	response := fiber.Map{
		"id":        server.ID,
		"familyId":  server.FamilyID,
		"name":      server.Name,
		"type":      server.Type,
		"url":       server.URL,
		"createdBy": server.CreatedBy,
		"createdAt": server.CreatedAt,
		"hasAccess": hasAccess || isAdmin,
	}

	// Include access list for admins
	if isAdmin {
		accessList, _ := h.serverRepo.GetServerAccess(serverID)
		if accessList == nil {
			accessList = []*models.ServerAccessDetail{}
		}
		response["access"] = accessList
	}

	return c.JSON(response)
}

// UpdateServer updates a media server configuration (admin only)
func (h *MediaServerHandler) UpdateServer(c *fiber.Ctx) error {
	familyID := c.Params("id")
	serverID := c.Params("serverId")
	userID := middleware.GetUserID(c)

	// Check if user is admin
	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil || !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only family admins can update media servers",
		})
	}

	// Verify server exists and belongs to family
	server, err := h.serverRepo.GetByID(serverID)
	if err == repository.ErrServerNotFound || server.FamilyID != familyID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get server",
		})
	}

	var req UpdateServerRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" || req.Type == "" || req.URL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "name, type, and url are required",
		})
	}

	if !isValidServerType(req.Type) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid server type",
		})
	}

	err = h.serverRepo.Update(serverID, req.Name, req.Type, req.URL)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to update server",
		})
	}

	return c.JSON(fiber.Map{
		"message": "server updated successfully",
	})
}

// DeleteServer removes a media server (admin only)
func (h *MediaServerHandler) DeleteServer(c *fiber.Ctx) error {
	familyID := c.Params("id")
	serverID := c.Params("serverId")
	userID := middleware.GetUserID(c)

	// Check if user is admin
	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil || !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only family admins can delete media servers",
		})
	}

	// Verify server belongs to family
	server, err := h.serverRepo.GetByID(serverID)
	if err == repository.ErrServerNotFound || server.FamilyID != familyID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}

	err = h.serverRepo.Delete(serverID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to delete server",
		})
	}

	return c.JSON(fiber.Map{
		"message": "server deleted successfully",
	})
}

// GrantAccess gives a user access to a media server (admin only)
func (h *MediaServerHandler) GrantAccess(c *fiber.Ctx) error {
	familyID := c.Params("id")
	serverID := c.Params("serverId")
	userID := middleware.GetUserID(c)

	// Check if user is admin
	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil || !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only family admins can grant server access",
		})
	}

	var req GrantAccessRequest
	if err := c.BodyParser(&req); err != nil || req.UserID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "userId is required",
		})
	}

	// Verify server belongs to family
	server, err := h.serverRepo.GetByID(serverID)
	if err == repository.ErrServerNotFound || server.FamilyID != familyID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}

	// Verify target user is in the family
	membership, err := h.familyRepo.GetMembership(familyID, req.UserID)
	if err != nil || membership == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "user is not a member of this family",
		})
	}

	err = h.serverRepo.GrantAccess(serverID, req.UserID, userID)
	if err == repository.ErrAlreadyHasAccess {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "user already has access to this server",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to grant access",
		})
	}

	return c.JSON(fiber.Map{
		"message": "access granted successfully",
	})
}

// RevokeAccess removes a user's access to a media server (admin only)
func (h *MediaServerHandler) RevokeAccess(c *fiber.Ctx) error {
	familyID := c.Params("id")
	serverID := c.Params("serverId")
	targetUserID := c.Params("userId")
	userID := middleware.GetUserID(c)

	// Check if user is admin
	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil || !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only family admins can revoke server access",
		})
	}

	// Verify server belongs to family
	server, err := h.serverRepo.GetByID(serverID)
	if err == repository.ErrServerNotFound || server.FamilyID != familyID {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "server not found",
		})
	}

	err = h.serverRepo.RevokeAccess(serverID, targetUserID)
	if err == repository.ErrAccessNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "user does not have access to this server",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to revoke access",
		})
	}

	return c.JSON(fiber.Map{
		"message": "access revoked successfully",
	})
}

// GetMyServers returns all servers the current user has access to
// This is the main endpoint the app calls to get available services
func (h *MediaServerHandler) GetMyServers(c *fiber.Ctx) error {
	userID := middleware.GetUserID(c)

	servers, err := h.serverRepo.GetUserServers(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get servers",
		})
	}

	if servers == nil {
		servers = []*models.UserServerAccess{}
	}

	return c.JSON(servers)
}

// Helper to validate server type
func isValidServerType(t models.MediaServerType) bool {
	validTypes := []models.MediaServerType{
		models.ServerTypePlex,
		models.ServerTypeAudiobookshelf,
		models.ServerTypeKavita,
		models.ServerTypeJellyfin,
		models.ServerTypeCalibre,
		models.ServerTypeCalibreWeb,
		models.ServerTypeStoryteller,
		models.ServerTypeBookLore,
	}

	for _, valid := range validTypes {
		if t == valid {
			return true
		}
	}
	return false
}
