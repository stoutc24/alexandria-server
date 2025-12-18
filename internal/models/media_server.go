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
	ID        string          `json:"id"`
	FamilyID  string          `json:"familyId"`
	Name      string          `json:"name"`
	Type      MediaServerType `json:"type"`
	URL       string          `json:"url"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

// MediaServerWithAccess includes access information for listing
type MediaServerWithAccess struct {
	ID        string          `json:"id"`
	FamilyID  string          `json:"familyId"`
	Name      string          `json:"name"`
	Type      MediaServerType `json:"type"`
	URL       string          `json:"url"`
	CreatedBy string          `json:"createdBy"`
	CreatedAt time.Time       `json:"createdAt"`
	HasAccess bool            `json:"hasAccess"`
	GrantedAt *time.Time      `json:"grantedAt,omitempty"`
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

// UserServerAccess is what the app receives - list of servers user can access
type UserServerAccess struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      MediaServerType `json:"type"`
	URL       string          `json:"url"`
	GrantedAt time.Time       `json:"grantedAt"`
}
