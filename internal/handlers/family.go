package handlers

import (
	"github.com/gofiber/fiber/v2"
	"github.com/stoutsolutions/alexandria-server/internal/middleware"
	"github.com/stoutsolutions/alexandria-server/internal/models"
	"github.com/stoutsolutions/alexandria-server/internal/repository"
)

type FamilyHandler struct {
	familyRepo *repository.FamilyRepository
}

func NewFamilyHandler(familyRepo *repository.FamilyRepository) *FamilyHandler {
	return &FamilyHandler{
		familyRepo: familyRepo,
	}
}

type CreateFamilyRequest struct {
	Name string `json:"name"`
}

type JoinFamilyRequest struct {
	InviteCode string `json:"inviteCode"`
}

func (h *FamilyHandler) Create(c *fiber.Ctx) error {
	var req CreateFamilyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.Name == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "family name is required",
		})
	}

	userID := middleware.GetUserID(c)
	family, err := h.familyRepo.Create(req.Name, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to create family",
		})
	}

	return c.Status(fiber.StatusCreated).JSON(family)
}

func (h *FamilyHandler) GetMyFamilies(c *fiber.Ctx) error {
	userID := middleware.GetUserID(c)
	families, err := h.familyRepo.GetUserFamilies(userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get families",
		})
	}

	if families == nil {
		families = []*models.Family{}
	}

	return c.JSON(families)
}

func (h *FamilyHandler) GetFamily(c *fiber.Ctx) error {
	familyID := c.Params("id")
	userID := middleware.GetUserID(c)

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

	family, err := h.familyRepo.GetByID(familyID)
	if err == repository.ErrFamilyNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "family not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get family",
		})
	}

	members, err := h.familyRepo.GetMembers(familyID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to get family members",
		})
	}

	if members == nil {
		members = []*models.MemberDetail{}
	}

	isAdmin := membership.Role == string(models.RoleAdmin)
	if !isAdmin {
		family.InviteCode = ""
	}

	return c.JSON(models.FamilyWithMembers{
		Family:  family,
		Members: members,
	})
}

func (h *FamilyHandler) Join(c *fiber.Ctx) error {
	var req JoinFamilyRequest
	if err := c.BodyParser(&req); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invalid request body",
		})
	}

	if req.InviteCode == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "invite code is required",
		})
	}

	family, err := h.familyRepo.GetByInviteCode(req.InviteCode)
	if err == repository.ErrInvalidInviteCode {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "invalid invite code",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to find family",
		})
	}

	userID := middleware.GetUserID(c)
	err = h.familyRepo.AddMember(family.ID, userID, models.RoleMember)
	if err == repository.ErrAlreadyMember {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{
			"error": "you are already a member of this family",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to join family",
		})
	}

	family.InviteCode = ""

	return c.JSON(fiber.Map{
		"message": "successfully joined family",
		"family":  family,
	})
}

func (h *FamilyHandler) Leave(c *fiber.Ctx) error {
	familyID := c.Params("id")
	userID := middleware.GetUserID(c)

	membership, err := h.familyRepo.GetMembership(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check membership",
		})
	}
	if membership == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "you are not a member of this family",
		})
	}

	err = h.familyRepo.RemoveMember(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to leave family",
		})
	}

	return c.JSON(fiber.Map{
		"message": "successfully left family",
	})
}

func (h *FamilyHandler) RemoveMember(c *fiber.Ctx) error {
	familyID := c.Params("id")
	memberUserID := c.Params("userId")
	userID := middleware.GetUserID(c)

	if memberUserID == userID {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "use the leave endpoint to leave a family",
		})
	}

	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check permissions",
		})
	}
	if !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only admins can remove members",
		})
	}

	err = h.familyRepo.RemoveMember(familyID, memberUserID)
	if err == repository.ErrFamilyNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "member not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to remove member",
		})
	}

	return c.JSON(fiber.Map{
		"message": "member removed successfully",
	})
}

func (h *FamilyHandler) RegenerateInviteCode(c *fiber.Ctx) error {
	familyID := c.Params("id")
	userID := middleware.GetUserID(c)

	isAdmin, err := h.familyRepo.IsAdmin(familyID, userID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to check permissions",
		})
	}
	if !isAdmin {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error": "only admins can regenerate invite codes",
		})
	}

	newCode, err := h.familyRepo.RegenerateInviteCode(familyID)
	if err == repository.ErrFamilyNotFound {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{
			"error": "family not found",
		})
	}
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": "failed to regenerate invite code",
		})
	}

	return c.JSON(fiber.Map{
		"inviteCode": newCode,
	})
}
