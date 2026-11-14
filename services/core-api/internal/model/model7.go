package model

import "time"

// Phase 31: media pipeline + premium site template.
//
// Media is a tenant-owned binary asset (site imagery — hero, service
// cards). Uploads are JSON base64 through POST /v1/media (JSON keeps the
// API shape uniform; ~8 MB decoded cap), stored in Postgres bytea with
// tenant RLS, and served PUBLICLY at /site/{slug}/media/{id} — the public
// site is the only consumer, so the serving path is tenant-scoped through
// the URL slug and carries long cache headers (ids are uuids; content is
// immutable per id).
type Media struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Kind        string    `json:"kind"` // hero | service | misc
	ContentType string    `json:"content_type"`
	Data        []byte    `json:"-"` // never API-serialized
	SizeBytes   int       `json:"size_bytes"`
	CreatedAt   time.Time `json:"created_at"`
}

const (
	MediaKindHero    = "hero"
	MediaKindService = "service"
	MediaKindMisc    = "misc"
)

// MaxMediaBytes caps a single upload (decoded). 8 MB covers high-quality
// hero photography while keeping bytea rows and the JSON request body
// (~33% base64 overhead) within sane memory bounds for the inline store.
const MaxMediaBytes = 8 << 20

// MediaContentTypes is the accepted upload set. SVG is deliberately
// excluded — scriptable markup served from the platform origin is an XSS
// surface; raster formats only.
var MediaContentTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}
