package domain

// Phase 30: Site Studio (theme validation, content generation with
// provenance, draft apply) + tenant insights overview.

import (
	"context"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

func TestThemeValidationAndPresets(t *testing.T) {
	// Presets resolve; overrides win.
	r := ResolveTheme(&model.SiteTheme{Preset: "azure-modern", Accent: "#FF0000"})
	if r.Primary != "#0b2447" || r.Accent != "#FF0000" { // preset base, override wins
		t.Fatalf("override resolution wrong: %+v", r)
	}
	if ResolveTheme(nil).Name != "noir-gold" {
		t.Fatal("nil theme must resolve to the default preset")
	}
	// Validation: bad preset, bad color, bad enum.
	for _, bad := range []*model.SiteTheme{
		{Preset: "nope"},
		{Primary: "red"},
		{Font: "handwritten"},
		{Corners: "squircle"},
	} {
		if err := ValidateTheme(bad); err == nil {
			t.Fatalf("accepted invalid theme %+v", bad)
		}
	}
	if err := ValidateTheme(&model.SiteTheme{Preset: "custom", Primary: "#123abc", Font: "sans"}); err != nil {
		t.Fatalf("valid custom theme rejected: %v", err)
	}
}

func TestStudioGenerateFallbackAndApply(t *testing.T) {
	s := testServices(t, Deps{}) // no agent URL → template fallback
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	catalog := &model.CatalogService{ID: NewID(), TenantID: tenantID,
		Vertical: "PRIVATE_TOUR", Name: "Sintra Tour", BaseMinor: 35000,
		Currency: "EUR", Active: true}
	if err := s.Store.Catalog().Add(ctx, catalog); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Geo().AddArea(ctx, &model.ServiceArea{ID: NewID(), TenantID: tenantID,
		Name: "Lisbon", Polygon: [][2]float64{{1, 1}, {2, 1}, {2, 2}}}); err != nil {
		t.Fatal(err)
	}

	draft, err := s.Studio.GenerateContent(ctx, tenantID, "Luxury tours for hotel guests", "en")
	if err != nil {
		t.Fatal(err)
	}
	if draft.Engine != "template" {
		t.Fatalf("no agent configured — engine must be template, got %q", draft.Engine)
	}
	if draft.Tagline == "" || draft.Description == "" {
		t.Fatal("fallback draft incomplete")
	}
	if draft.ServiceDescs[catalog.ID] == "" {
		t.Fatal("active service missing from draft")
	}

	// Apply: brand_i18n + desc_i18n written, site renders them.
	if err := s.Studio.ApplyDraft(ctx, tenantID, draft); err != nil {
		t.Fatal(err)
	}
	tn, _ := s.Store.Tenants().Get(ctx, tenantID)
	if tn.SiteConfig.BrandI18n["en"]["tagline"] != draft.Tagline {
		t.Fatal("tagline not applied to brand_i18n")
	}
	services, _ := s.Store.Catalog().List(ctx, tenantID)
	if services[0].DescI18n["en"] != draft.ServiceDescs[catalog.ID] {
		t.Fatal("service desc_i18n not applied")
	}

	// Foreign service ID → wholesale refusal.
	bad := &StudioDraft{Locale: "en", ServiceDescs: map[string]string{"not-a-service": "x"}}
	if err := s.Studio.ApplyDraft(ctx, tenantID, bad); err == nil {
		t.Fatal("foreign service ID accepted")
	}
}

func TestStudioSetTheme(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	theme, err := s.Studio.SetTheme(ctx, tenantID, &model.SiteTheme{Preset: "emerald-classic"})
	if err != nil {
		t.Fatal(err)
	}
	if theme.Preset != "emerald-classic" {
		t.Fatal("theme not persisted")
	}
	tn, _ := s.Store.Tenants().Get(ctx, tenantID)
	if tn.SiteConfig.Theme == nil || tn.SiteConfig.Theme.Preset != "emerald-classic" {
		t.Fatal("site_config.theme missing")
	}
	if _, err := s.Studio.SetTheme(ctx, tenantID, &model.SiteTheme{Accent: "zzz"}); err == nil {
		t.Fatal("invalid accent accepted")
	}
}

func TestInsightsOverview(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// One confirmed booking with a quote and a completed assignment.
	sr, err := s.Booking.CreateRequest(ctx, &model.ServiceRequest{
		TenantID: tenantID, ClientID: "c1", IdempotencyKey: "kpi-" + NewID()[:8],
		Items: []model.ServiceItem{{Vertical: "CHAUFFEUR_POINT_TO_POINT",
			PickupAddress: "A", DropoffAddress: "B", PickupAt: s.Now().Add(time.Hour).Unix(), Passengers: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	q := &model.Quote{ID: NewID(), TenantID: tenantID, RequestID: sr.ID,
		TotalMinor: 12000, Currency: "EUR", CreatedAt: s.Now()}
	if err := s.Store.Quotes().Create(ctx, q); err != nil {
		t.Fatal(err)
	}
	b := &model.Booking{ID: NewID(), TenantID: tenantID, RequestID: sr.ID, QuoteID: q.ID,
		State: "CONFIRMED", CreatedAt: s.Now()}
	if err := s.Store.Bookings().Create(ctx, b); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Payments().AddPosting(ctx, &model.LedgerPosting{
		ID: NewID(), TenantID: tenantID, BookingID: b.ID,
		DebitCode: "CLIENT_CLEARING", CreditCode: "TENANT_REVENUE",
		AmountMinor: 11400, Currency: "EUR", IdempotencyKey: "pay-" + NewID()[:8],
		CreatedAt: s.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	fleet := &model.FleetResource{ID: NewID(), TenantID: tenantID, Kind: "sedan", Label: "S-Class 1", Active: true}
	crew := &model.CrewMember{ID: NewID(), TenantID: tenantID, FullName: "A. Driver", Active: true}
	_ = s.Store.Dispatch().AddFleetResource(ctx, fleet)
	_ = s.Store.Dispatch().AddCrewMember(ctx, crew)
	a := &model.Assignment{ID: NewID(), TenantID: tenantID, BookingID: b.ID,
		FleetResourceID: fleet.ID, CrewMemberID: crew.ID, Status: "COMPLETED_TRIP",
		StartAt: s.Now().Add(-4 * time.Hour), EndAt: s.Now().Add(-2 * time.Hour), CreatedAt: s.Now()}
	if err := s.Store.Dispatch().CreateAssignment(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := s.Store.Reviews().Create(ctx, &model.Review{ID: NewID(), TenantID: tenantID,
		ClientID: "c1", AssignmentID: a.ID, Rating: 5, CreatedAt: s.Now()}); err != nil {
		t.Fatal(err)
	}

	o, err := s.Insights.Overview(ctx, tenantID, 30)
	if err != nil {
		t.Fatal(err)
	}
	if o.Totals.BookingsCreated != 1 || o.Totals.BookingsConfirmed != 1 {
		t.Fatalf("booking totals wrong: %+v", o.Totals)
	}
	if o.Totals.GMVMinor != 12000 || o.Totals.CollectedMinor != 11400 {
		t.Fatalf("money totals wrong: %+v", o.Totals)
	}
	if o.Totals.TripsCompleted != 1 || o.Totals.ReviewAvg != "5.0" {
		t.Fatalf("trip/review totals wrong: %+v", o.Totals)
	}
	if len(o.ByService) != 1 || o.ByService[0].Bookings != 1 {
		t.Fatalf("service mix wrong: %+v", o.ByService)
	}
	if len(o.Fleet) != 1 || o.Fleet[0].HoursBooked < 1.99 || o.Fleet[0].HoursBooked > 2.01 {
		t.Fatalf("fleet kpi wrong: %+v", o.Fleet)
	}
	if o.Fleet[0].UtilizationPct <= 0 {
		t.Fatal("utilization should be positive for a booked window")
	}
	if len(o.Drivers) != 1 || o.Drivers[0].ReviewAvg != 5.0 || o.Drivers[0].Completed != 1 {
		t.Fatalf("driver kpi wrong: %+v", o.Drivers)
	}
	if len(o.Notes) == 0 {
		t.Fatal("honesty notes missing")
	}
}
