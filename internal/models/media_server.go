package models

import "time"

// Supported media server types
type MediaServerType string

const (
	ServerTypePlex          MediaServerType = "plex"
	ServerTypeAudiobookshelf MediaServerType = "audiobookshelf"
	ServerTypeKavita        MediaServerType = "kavita"
	ServerTypeJellyfin      MediaServerType = "jellyfin"
	ServerTypeCalibre       MediaServerType = "calibre"
	ServerTypeCalibreWeb    MediaServerType = "calibre-web"
	ServerTypeStoryteller   MediaServerType = "storyteller"
	ServerTypeBookLore      MediaServerType = "booklore"
)

// MediaServer represents a configured media server connection
type MediaServer struct {
	ID                  string          `json:"id"`
	FamilyID            string          `json:"familyId"`
	Name                string          `json:"name"`
	Type                MediaServerType `json:"type"`
	URL                 string          `json:"url"`
	EncryptedCredentials string         `json:"-"` // Never expose encrypted blob
	CreatedBy           string          `json:"createdBy"`
	CreatedAt           time.Time       `json:"createdAt"`
	UpdatedAt           time.Time       `json:"updatedAt"`
}

// MediaServerCredentials holds the decrypted auth info for a server
// Different server types use different auth methods
type MediaServerCredentials struct {
	// Username/Password auth (ABS, Kavita, Calibre-Web, etc.)
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`

	// API Key auth (some servers)
	APIKey string `json:"apiKey,omitempty"`

	// Token auth (Plex, Jellyfin)
	Token string `json:"token,omitempty"`

	// Additional fields for specific servers
	ServerID string `json:"serverId,omitempty"` // Plex server ID
	LibraryID string `json:"libraryId,omitempty"` // Specific library selection
}

// MediaServerResponse is what we send to clients (with decrypted creds for authorized users)
type MediaServerResponse struct {
	ID          string              `json:"id"`
	FamilyID    string              `json:"familyId"`
	Name        string              `json:"name"`
	Type        MediaServerType     `json:"type"`
	URL         string              `json:"url"`
	Credentials *MediaServerCredentials `json:"credentials,omitempty"` // Only included for authorized users
	CreatedBy   string              `json:"createdBy"`
	CreatedAt   time.Time           `json:"createdAt"`
}

// MediaServerWithAccess includes access information
type MediaServerWithAccess struct {
	MediaServerResponse
	HasAccess   bool      `json:"hasAccess"`
	GrantedAt   *time.Time `json:"grantedAt,omitempty"`
	GrantedBy   string    `json:"grantedBy,omitempty"`
}

// ServerAccess represents a user's access to a media server
type ServerAccess struct {
	ID        string    `json:"id"`
	ServerID  string    `json:"serverId"`
	UserID    string    `json:"userId"`
	GrantedBy string    `json:"grantedBy"`
	GrantedAt time.Time `json:"grantedAt"`
}

// ServerAccessDetail includes user information
type ServerAccessDetail struct {
	ID          string    `json:"id"`
	ServerID    string    `json:"serverId"`
	UserID      string    `json:"userId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName,omitempty"`
	GrantedBy   string    `json:"grantedBy"`
	GrantedAt   time.Time `json:"grantedAt"`
}

// UserServerAccess is what the app receives - server info with credentials
type UserServerAccess struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	Type        MediaServerType         `json:"type"`
	URL         string                  `json:"url"`
	Credentials MediaServerCredentials  `json:"credentials"`
	GrantedAt   time.Time               `json:"grantedAt"`
}
