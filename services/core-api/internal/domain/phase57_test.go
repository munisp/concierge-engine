package domain

// Phase 57: partner portal tokens, portal view, and WhatsApp offer honesty.

import (
	"context"
	"testing"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

func TestPartnerTokenRoundtrip57(t *testing.T) {
	s := testServices(t, Deps{})
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "Mr Travel", 10)

	tok := s.Trips.PartnerTokenFor(tenantID, p.ID)
	tid, pid, err := s.Trips.ParsePartnerToken(tok)
	if err != nil || tid != tenantID || pid != p.ID {
		t.Fatalf("roundtrip: %s %s %v", tid, pid, err)
	}
	// Chauffeur parser rejects partner tokens (and vice versa).
	if _, _, err := s.Trips.ParseToken(tok); err != httpx.ErrForbidden {
		t.Fatalf("partner token on chauffeur surface: %v", err)
	}
	// A chauffeur token must not open the portal.
	ct := s.Trips.TokenFor(tenantID, "some-assignment")
	if _, _, err := s.Trips.ParsePartnerToken(ct); err != httpx.ErrForbidden {
		t.Fatalf("chauffeur token on partner surface: %v", err)
	}
	// Tampered token rejected.
	if _, _, err := s.Trips.ParsePartnerToken(tok[:len(tok)-2] + "xx"); err != httpx.ErrForbidden {
		t.Fatalf("tampered token: %v", err)
	}
}

func TestPartnerPortalView57(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	p := seedPartner56(t, s, tenantID, "Younique Tailor Tours", 12.5)
	bookingID := bookForPartner56(t, s, tenantID, 12000)
	if _, err := s.Dispatch.Assign(ctx, &model.Assignment{
		TenantID: tenantID, BookingID: bookingID, PartnerID: p.ID,
	}); err != nil {
		t.Fatal(err)
	}

	view, err := s.PartnerOps.PortalView(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if view.PartnerName != "Younique Tailor Tours" || len(view.Assignments) != 1 {
		t.Fatalf("portal view: %+v", view)
	}
	if view.Settlement.Totals["PENDING"] != 1500 {
		t.Fatalf("portal settlement: %+v", view.Settlement.Totals)
	}

	// Suspended partner: portal closes even with a valid token.
	if _, err := s.Partners.SetStatus(ctx, tenantID, p.ID, "SUSPENDED"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PartnerOps.PortalView(ctx, tenantID, p.ID); err != httpx.ErrForbidden {
		t.Fatalf("suspended partner portal: %v", err)
	}
}

func TestPartnerWhatsAppOfferHonesty57(t *testing.T) {
	s := testServices(t, Deps{}) // no WhatsApp adapter configured
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	// No phone → skipped:no-phone, link still usable.
	p := seedPartner56(t, s, tenantID, "The Little Orange", 10)
	res, err := s.PartnerOps.NotifyOffer(ctx, tenantID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.WhatsApp != "skipped:no-phone" || res.PortalURL == "" {
		t.Fatalf("no-phone notify: %+v", res)
	}

	// Phone set but no adapter → skipped:no-adapter, never a fake "sent".
	p2 := seedPartner56(t, s, tenantID, "Mr Travel", 10)
	p2.Phone = "+2348012345678"
	if err := s.Store.Partners().Update(ctx, p2); err != nil {
		t.Fatal(err)
	}
	res, err = s.PartnerOps.NotifyOffer(ctx, tenantID, p2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.WhatsApp != "skipped:no-adapter" {
		t.Fatalf("no-adapter notify: %+v", res)
	}
}
