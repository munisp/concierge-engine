package server

// Internal (package server) — landingLD is an unexported handler helper.

import (
	"net/http/httptest"
	"strings"
	"testing"

	"concierge-engine/services/core-api/internal/domain"
	"concierge-engine/services/core-api/internal/model"
	"concierge-engine/services/core-api/internal/store"
)

// TestLandingReviewLD seeds a publish-consented review directly in the
// store and asserts the landing graph carries both the aggregate and the
// individual review node with the real rating.
func TestLandingReviewLD(t *testing.T) {
	svc := domain.NewServices(store.NewMemory(), domain.Deps{})
	tenant := &model.Tenant{ID: "t1", Slug: "seo", Name: "SEO Co"}
	if err := svc.Store.Tenants().Create(t.Context(), tenant); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.Reviews().Create(t.Context(), &model.Review{
		ID: "r1", TenantID: "t1", ClientID: "c1", AssignmentID: "a1",
		Rating: 5, Text: "Flawless airport pickup.", PublishConsent: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Store.Reviews().Create(t.Context(), &model.Review{
		ID: "r2", TenantID: "t1", ClientID: "c2", AssignmentID: "a2",
		Rating: 1, Text: "private feedback", PublishConsent: false,
	}); err != nil {
		t.Fatal(err)
	}
	h := &handlers{svc: svc}
	req := httptest.NewRequest("GET", "/site/seo/", nil)
	nodes := h.landingLD(req, tenant, sitePage{Description: "d", Canonical: "https://x/"}, nil, nil)
	b := marshalLDGraph(nodes...)
	s := string(b)
	if !strings.Contains(s, `"ratingValue":5`) || strings.Contains(s, "private feedback") {
		t.Fatalf("review LD wrong: %s", s)
	}
	if !strings.Contains(s, `"reviewCount":1`) { // only the consented one
		t.Fatalf("aggregate must count consented only: %s", s)
	}
}

// TestAsciiFold29: the OG card bitmap font is ASCII-only — diacritics fold
// to their base letters so names like "Fátima" render legibly on cards.
// Page HTML is unaffected (full UTF-8); this is card-only.
func TestAsciiFold29(t *testing.T) {
	if got := asciiFold("Fátima — São Jorge ção"); got != "Fatima — Sao Jorge cao" {
		t.Fatalf("asciiFold mismatch: %q", got)
	}
	if got := asciiFold("Über Ñandú"); got != "Uber Nandu" {
		t.Fatalf("asciiFold mismatch: %q", got)
	}
}
