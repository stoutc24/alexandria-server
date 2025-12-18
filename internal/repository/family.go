package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/models"
)

var (
	ErrFamilyNotFound     = errors.New("family not found")
	ErrAlreadyMember      = errors.New("user is already a member of this family")
	ErrInvalidInviteCode  = errors.New("invalid invite code")
	ErrNotFamilyAdmin     = errors.New("user is not a family admin")
	ErrCannotRemoveSelf   = errors.New("cannot remove yourself from family")
	ErrFamilyFull         = errors.New("family has reached maximum member limit")
)

type FamilyRepository struct{}

func NewFamilyRepository() *FamilyRepository {
	return &FamilyRepository{}
}

func (r *FamilyRepository) Create(name, createdBy string) (*models.Family, error) {
	id := uuid.New().String()
	inviteCode := generateInviteCode()
	now := time.Now()

	tx, err := database.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		INSERT INTO families (id, name, invite_code, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, name, inviteCode, createdBy, now, now)
	if err != nil {
		return nil, err
	}

	memberID := uuid.New().String()
	_, err = tx.Exec(`
		INSERT INTO family_members (id, family_id, user_id, role, joined_at)
		VALUES (?, ?, ?, ?, ?)
	`, memberID, id, createdBy, models.RoleAdmin, now)
	if err != nil {
		return nil, err
	}

	if err = tx.Commit(); err != nil {
		return nil, err
	}

	return &models.Family{
		ID:         id,
		Name:       name,
		InviteCode: inviteCode,
		CreatedBy:  createdBy,
		CreatedAt:  now,
		UpdatedAt:  now,
	}, nil
}

func (r *FamilyRepository) GetByID(id string) (*models.Family, error) {
	family := &models.Family{}
	var inviteCode sql.NullString

	err := database.DB.QueryRow(`
		SELECT id, name, invite_code, created_by, created_at, updated_at
		FROM families WHERE id = ?
	`, id).Scan(
		&family.ID, &family.Name, &inviteCode, &family.CreatedBy,
		&family.CreatedAt, &family.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrFamilyNotFound
	}
	if err != nil {
		return nil, err
	}

	family.InviteCode = inviteCode.String
	return family, nil
}

func (r *FamilyRepository) GetByInviteCode(code string) (*models.Family, error) {
	family := &models.Family{}
	var inviteCode sql.NullString

	err := database.DB.QueryRow(`
		SELECT id, name, invite_code, created_by, created_at, updated_at
		FROM families WHERE invite_code = ?
	`, code).Scan(
		&family.ID, &family.Name, &inviteCode, &family.CreatedBy,
		&family.CreatedAt, &family.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrInvalidInviteCode
	}
	if err != nil {
		return nil, err
	}

	family.InviteCode = inviteCode.String
	return family, nil
}

func (r *FamilyRepository) GetUserFamilies(userID string) ([]*models.Family, error) {
	rows, err := database.DB.Query(`
		SELECT f.id, f.name, f.invite_code, f.created_by, f.created_at, f.updated_at
		FROM families f
		INNER JOIN family_members fm ON f.id = fm.family_id
		WHERE fm.user_id = ?
		ORDER BY f.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var families []*models.Family
	for rows.Next() {
		family := &models.Family{}
		var inviteCode sql.NullString
		if err := rows.Scan(
			&family.ID, &family.Name, &inviteCode, &family.CreatedBy,
			&family.CreatedAt, &family.UpdatedAt,
		); err != nil {
			return nil, err
		}
		family.InviteCode = inviteCode.String
		families = append(families, family)
	}

	return families, rows.Err()
}

func (r *FamilyRepository) GetMembers(familyID string) ([]*models.MemberDetail, error) {
	rows, err := database.DB.Query(`
		SELECT fm.id, fm.user_id, u.username, u.display_name, u.avatar_url, fm.role, fm.joined_at
		FROM family_members fm
		INNER JOIN users u ON fm.user_id = u.id
		WHERE fm.family_id = ?
		ORDER BY fm.joined_at
	`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var members []*models.MemberDetail
	for rows.Next() {
		member := &models.MemberDetail{}
		var displayName, avatarURL sql.NullString
		if err := rows.Scan(
			&member.ID, &member.UserID, &member.Username,
			&displayName, &avatarURL, &member.Role, &member.JoinedAt,
		); err != nil {
			return nil, err
		}
		member.DisplayName = displayName.String
		member.AvatarURL = avatarURL.String
		members = append(members, member)
	}

	return members, rows.Err()
}

func (r *FamilyRepository) CountMembers(familyID string) (int, error) {
	var count int
	err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM family_members WHERE family_id = ?
	`, familyID).Scan(&count)
	return count, err
}

func (r *FamilyRepository) AddMember(familyID, userID string, role models.FamilyRole) error {
	id := uuid.New().String()
	_, err := database.DB.Exec(`
		INSERT INTO family_members (id, family_id, user_id, role, joined_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, familyID, userID, role, time.Now())

	if err != nil && isUniqueConstraintError(err, "family_members") {
		return ErrAlreadyMember
	}
	return err
}

func (r *FamilyRepository) RemoveMember(familyID, userID string) error {
	result, err := database.DB.Exec(`
		DELETE FROM family_members WHERE family_id = ? AND user_id = ?
	`, familyID, userID)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrFamilyNotFound
	}
	return nil
}

func (r *FamilyRepository) GetMembership(familyID, userID string) (*models.FamilyMember, error) {
	member := &models.FamilyMember{}
	err := database.DB.QueryRow(`
		SELECT id, family_id, user_id, role, joined_at
		FROM family_members WHERE family_id = ? AND user_id = ?
	`, familyID, userID).Scan(
		&member.ID, &member.FamilyID, &member.UserID, &member.Role, &member.JoinedAt,
	)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return member, nil
}

func (r *FamilyRepository) IsAdmin(familyID, userID string) (bool, error) {
	var role string
	err := database.DB.QueryRow(`
		SELECT role FROM family_members WHERE family_id = ? AND user_id = ?
	`, familyID, userID).Scan(&role)

	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return role == string(models.RoleAdmin), nil
}

func (r *FamilyRepository) RegenerateInviteCode(familyID string) (string, error) {
	newCode := generateInviteCode()
	result, err := database.DB.Exec(`
		UPDATE families SET invite_code = ?, updated_at = ? WHERE id = ?
	`, newCode, time.Now(), familyID)
	if err != nil {
		return "", err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return "", ErrFamilyNotFound
	}
	return newCode, nil
}

func generateInviteCode() string {
	bytes := make([]byte, 4)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}
