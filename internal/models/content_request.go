package models

import "time"

type ContentRequest struct {
	ID          string     `json:"id"`
	FamilyID    string     `json:"familyId"`
	RequestedBy string     `json:"requestedBy"`
	Title       string     `json:"title"`
	ContentType string     `json:"contentType"`
	Description string     `json:"description,omitempty"`
	Status      string     `json:"status"`
	ResolvedBy  *string    `json:"resolvedBy,omitempty"`
	ResolvedAt  *time.Time `json:"resolvedAt,omitempty"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
}

type ContentType string

const (
	ContentTypeBook      ContentType = "book"
	ContentTypeAudiobook ContentType = "audiobook"
	ContentTypeMovie     ContentType = "movie"
	ContentTypeTVShow    ContentType = "tvshow"
	ContentTypeMusic     ContentType = "music"
	ContentTypeOther     ContentType = "other"
)

type RequestStatus string

const (
	StatusPending   RequestStatus = "pending"
	StatusApproved  RequestStatus = "approved"
	StatusRejected  RequestStatus = "rejected"
	StatusFulfilled RequestStatus = "fulfilled"
)

type ContentRequestWithUser struct {
	ContentRequest
	RequesterUsername    string `json:"requesterUsername"`
	RequesterDisplayName string `json:"requesterDisplayName,omitempty"`
}
