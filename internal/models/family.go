package models

import "time"

type Family struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	InviteCode string    `json:"inviteCode,omitempty"`
	CreatedBy  string    `json:"createdBy"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

type FamilyMember struct {
	ID       string    `json:"id"`
	FamilyID string    `json:"familyId"`
	UserID   string    `json:"userId"`
	Role     string    `json:"role"`
	JoinedAt time.Time `json:"joinedAt"`
}

type FamilyRole string

const (
	RoleAdmin  FamilyRole = "admin"
	RoleMember FamilyRole = "member"
)

type FamilyWithMembers struct {
	Family  *Family         `json:"family"`
	Members []*MemberDetail `json:"members"`
}

type MemberDetail struct {
	ID          string    `json:"id"`
	UserID      string    `json:"userId"`
	Username    string    `json:"username"`
	DisplayName string    `json:"displayName,omitempty"`
	AvatarURL   string    `json:"avatarUrl,omitempty"`
	Role        string    `json:"role"`
	JoinedAt    time.Time `json:"joinedAt"`
}
