package repository

import (
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/stoutsolutions/alexandria-server/internal/crypto"
	"github.com/stoutsolutions/alexandria-server/internal/database"
	"github.com/stoutsolutions/alexandria-server/internal/models"
)

var (
	ErrServerNotFound    = errors.New("media server not found")
	ErrAccessNotFound    = errors.New("server access not found")
	ErrAlreadyHasAccess  = errors.New("user already has access to this server")
)

type MediaServerRepository struct {
	encryptor *crypto.Encryptor
}

func NewMediaServerRepository(encryptor *crypto.Encryptor) *MediaServerRepository {
	return &MediaServerRepository{encryptor: encryptor}
}

// Create adds a new media server to a family
func (r *MediaServerRepository) Create(
	familyID, name string,
	serverType models.MediaServerType,
	url string,
	creds *models.MediaServerCredentials,
	createdBy string,
) (*models.MediaServer, error) {
	id := uuid.New().String()
	now := time.Now()

	encryptedCreds, err := r.encryptor.EncryptCredentials(creds)
	if err != nil {
		return nil, err
	}

	_, err = database.DB.Exec(`
		INSERT INTO media_servers (id, family_id, name, type, url, encrypted_credentials, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, id, familyID, name, serverType, url, encryptedCreds, createdBy, now, now)

	if err != nil {
		return nil, err
	}

	return &models.MediaServer{
		ID:                   id,
		FamilyID:             familyID,
		Name:                 name,
		Type:                 serverType,
		URL:                  url,
		EncryptedCredentials: encryptedCreds,
		CreatedBy:            createdBy,
		CreatedAt:            now,
		UpdatedAt:            now,
	}, nil
}

// GetByID retrieves a media server by ID
func (r *MediaServerRepository) GetByID(id string) (*models.MediaServer, error) {
	server := &models.MediaServer{}

	err := database.DB.QueryRow(`
		SELECT id, family_id, name, type, url, encrypted_credentials, created_by, created_at, updated_at
		FROM media_servers WHERE id = ?
	`, id).Scan(
		&server.ID, &server.FamilyID, &server.Name, &server.Type,
		&server.URL, &server.EncryptedCredentials, &server.CreatedBy,
		&server.CreatedAt, &server.UpdatedAt,
	)

	if err == sql.ErrNoRows {
		return nil, ErrServerNotFound
	}
	if err != nil {
		return nil, err
	}

	return server, nil
}

// GetByFamily retrieves all media servers for a family
func (r *MediaServerRepository) GetByFamily(familyID string) ([]*models.MediaServer, error) {
	rows, err := database.DB.Query(`
		SELECT id, family_id, name, type, url, encrypted_credentials, created_by, created_at, updated_at
		FROM media_servers WHERE family_id = ?
		ORDER BY name
	`, familyID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*models.MediaServer
	for rows.Next() {
		server := &models.MediaServer{}
		if err := rows.Scan(
			&server.ID, &server.FamilyID, &server.Name, &server.Type,
			&server.URL, &server.EncryptedCredentials, &server.CreatedBy,
			&server.CreatedAt, &server.UpdatedAt,
		); err != nil {
			return nil, err
		}
		servers = append(servers, server)
	}

	return servers, rows.Err()
}

// Update updates a media server's configuration
func (r *MediaServerRepository) Update(
	id, name string,
	serverType models.MediaServerType,
	url string,
	creds *models.MediaServerCredentials,
) error {
	encryptedCreds, err := r.encryptor.EncryptCredentials(creds)
	if err != nil {
		return err
	}

	result, err := database.DB.Exec(`
		UPDATE media_servers
		SET name = ?, type = ?, url = ?, encrypted_credentials = ?, updated_at = ?
		WHERE id = ?
	`, name, serverType, url, encryptedCreds, time.Now(), id)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrServerNotFound
	}

	return nil
}

// Delete removes a media server
func (r *MediaServerRepository) Delete(id string) error {
	result, err := database.DB.Exec(`DELETE FROM media_servers WHERE id = ?`, id)
	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrServerNotFound
	}

	return nil
}

// DecryptCredentials decrypts the credentials for a media server
func (r *MediaServerRepository) DecryptCredentials(server *models.MediaServer) (*models.MediaServerCredentials, error) {
	return r.encryptor.DecryptCredentials(server.EncryptedCredentials)
}

// GrantAccess gives a user access to a media server
func (r *MediaServerRepository) GrantAccess(serverID, userID, grantedBy string) error {
	id := uuid.New().String()

	_, err := database.DB.Exec(`
		INSERT INTO server_access (id, server_id, user_id, granted_by, granted_at)
		VALUES (?, ?, ?, ?, ?)
	`, id, serverID, userID, grantedBy, time.Now())

	if err != nil && isUniqueConstraintError(err, "server_access") {
		return ErrAlreadyHasAccess
	}

	return err
}

// RevokeAccess removes a user's access to a media server
func (r *MediaServerRepository) RevokeAccess(serverID, userID string) error {
	result, err := database.DB.Exec(`
		DELETE FROM server_access WHERE server_id = ? AND user_id = ?
	`, serverID, userID)

	if err != nil {
		return err
	}

	rows, _ := result.RowsAffected()
	if rows == 0 {
		return ErrAccessNotFound
	}

	return nil
}

// HasAccess checks if a user has access to a media server
func (r *MediaServerRepository) HasAccess(serverID, userID string) (bool, error) {
	var count int
	err := database.DB.QueryRow(`
		SELECT COUNT(*) FROM server_access WHERE server_id = ? AND user_id = ?
	`, serverID, userID).Scan(&count)

	if err != nil {
		return false, err
	}

	return count > 0, nil
}

// GetServerAccess lists all users with access to a server
func (r *MediaServerRepository) GetServerAccess(serverID string) ([]*models.ServerAccessDetail, error) {
	rows, err := database.DB.Query(`
		SELECT sa.id, sa.server_id, sa.user_id, u.username, u.display_name, sa.granted_by, sa.granted_at
		FROM server_access sa
		INNER JOIN users u ON sa.user_id = u.id
		WHERE sa.server_id = ?
		ORDER BY sa.granted_at
	`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var access []*models.ServerAccessDetail
	for rows.Next() {
		a := &models.ServerAccessDetail{}
		var displayName sql.NullString
		if err := rows.Scan(
			&a.ID, &a.ServerID, &a.UserID, &a.Username,
			&displayName, &a.GrantedBy, &a.GrantedAt,
		); err != nil {
			return nil, err
		}
		a.DisplayName = displayName.String
		access = append(access, a)
	}

	return access, rows.Err()
}

// GetUserServers returns all servers a user has access to (with decrypted credentials)
func (r *MediaServerRepository) GetUserServers(userID string) ([]*models.UserServerAccess, error) {
	rows, err := database.DB.Query(`
		SELECT ms.id, ms.name, ms.type, ms.url, ms.encrypted_credentials, sa.granted_at
		FROM media_servers ms
		INNER JOIN server_access sa ON ms.id = sa.server_id
		WHERE sa.user_id = ?
		ORDER BY ms.name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var servers []*models.UserServerAccess
	for rows.Next() {
		var encryptedCreds string
		s := &models.UserServerAccess{}

		if err := rows.Scan(
			&s.ID, &s.Name, &s.Type, &s.URL, &encryptedCreds, &s.GrantedAt,
		); err != nil {
			return nil, err
		}

		// Decrypt credentials
		creds, err := r.encryptor.DecryptCredentials(encryptedCreds)
		if err != nil {
			return nil, err
		}
		s.Credentials = *creds

		servers = append(servers, s)
	}

	return servers, rows.Err()
}
