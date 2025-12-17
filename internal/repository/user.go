package repository

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/models"
)

var (
	ErrUserNotFound      = errors.New("user not found")
	ErrEmailExists       = errors.New("email already exists")
	ErrUsernameExists    = errors.New("username already exists")
)

type UserRepository struct{}

func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

func (r *UserRepository) Create(email, username, passwordHash string) (*models.User, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := database.DB.Exec(`
		INSERT INTO users (id, email, username, password_hash, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, id, email, username, passwordHash, now, now)

	if err != nil {
		if isUniqueConstraintError(err, "email") {
			return nil, ErrEmailExists
		}
		if isUniqueConstraintError(err, "username") {
			return nil, ErrUsernameExists
		}
		return nil, err
	}

	return &models.User{
		ID:           id,
		Email:        email,
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (r *UserRepository) GetByID(id string) (*models.User, error) {
	user := &models.User{}
	var displayName, avatarURL sql.NullString

	err := database.DB.QueryRow(`
		SELECT id, email, username, password_hash, display_name, avatar_url, created_at, updated_at
		FROM users WHERE id = ?
	`, id).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&displayName, &avatarURL, &user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	user.DisplayName = displayName.String
	user.AvatarURL = avatarURL.String
	return user, nil
}

func (r *UserRepository) GetByEmail(email string) (*models.User, error) {
	user := &models.User{}
	var displayName, avatarURL sql.NullString

	err := database.DB.QueryRow(`
		SELECT id, email, username, password_hash, display_name, avatar_url, created_at, updated_at
		FROM users WHERE email = ?
	`, email).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&displayName, &avatarURL, &user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	user.DisplayName = displayName.String
	user.AvatarURL = avatarURL.String
	return user, nil
}

func (r *UserRepository) GetByUsername(username string) (*models.User, error) {
	user := &models.User{}
	var displayName, avatarURL sql.NullString

	err := database.DB.QueryRow(`
		SELECT id, email, username, password_hash, display_name, avatar_url, created_at, updated_at
		FROM users WHERE username = ?
	`, username).Scan(
		&user.ID, &user.Email, &user.Username, &user.PasswordHash,
		&displayName, &avatarURL, &user.CreatedAt, &user.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	if err != nil {
		return nil, err
	}

	user.DisplayName = displayName.String
	user.AvatarURL = avatarURL.String
	return user, nil
}

func (r *UserRepository) UpdateProfile(id, displayName, avatarURL string) error {
	result, err := database.DB.Exec(`
		UPDATE users SET display_name = ?, avatar_url = ?, updated_at = ?
		WHERE id = ?
	`, displayName, avatarURL, time.Now(), id)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrUserNotFound
	}
	return nil
}

func isUniqueConstraintError(err error, field string) bool {
	return err != nil && (
		contains(err.Error(), "UNIQUE constraint failed") &&
		contains(err.Error(), field))
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
