package model

import "time"

// Phase 62: chauffeur matching — the rider's declared relationship with
// a chauffeur. A favorite is a strong positive signal, a block is a hard
// exclusion. One row per (tenant, client, crew member).
type ChauffeurLink struct {
	TenantID     string    `json:"tenant_id"`
	ClientID     string    `json:"client_id"`
	CrewMemberID string    `json:"crew_member_id"`
	Kind         string    `json:"kind"` // favorite | blocked
	CreatedAt    time.Time `json:"created_at"`
}

const (
	LinkFavorite = "favorite"
	LinkBlocked  = "blocked"
)

func ValidLinkKind(k string) bool { return k == LinkFavorite || k == LinkBlocked }
