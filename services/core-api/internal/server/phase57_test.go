package server_test

// Phase 57: partner portal over HTTP — token-authed view + respond, the
// admin portal-link/notify routes, and cross-surface token rejection.

import (
	"net/http"
	"strings"
	"testing"
)

func TestPartnerPortalFlow57(t *testing.T) {
	env := newEnv(t)
	partnerID, bookingID, assignmentID := seedPartnerBooking56(t, env, 12.5)

	// Tenant admin fetches the shareable portal link.
	var link struct {
		URL string `json:"url"`
	}
	env.do("GET", "/v1/partners/"+partnerID+"/portal-link", env.tenantID, nil, http.StatusOK, &link)
	if link.URL == "" {
		t.Fatal("empty portal link")
	}
	i := strings.Index(link.URL, "token=")
	if i < 0 {
		t.Fatalf("no token in portal link: %s", link.URL)
	}
	token := link.URL[i+len("token="):]

	// Portal view: assignments + settlement, no tenant header needed.
	var view struct {
		PartnerName string `json:"partner_name"`
		Assignments []struct {
			ID        string `json:"id"`
			BookingID string `json:"booking_id"`
			Status    string `json:"status"`
		} `json:"assignments"`
		Settlement struct {
			Totals map[string]int64 `json:"totals"`
		} `json:"settlement"`
	}
	env.doBearer("GET", "/v1/portal/"+token, "", nil, http.StatusOK, &view)
	if view.PartnerName != "Younique Tailor Tours" || len(view.Assignments) != 1 ||
		view.Assignments[0].ID != assignmentID || view.Assignments[0].BookingID != bookingID {
		t.Fatalf("portal view: %+v", view)
	}
	if view.Settlement.Totals["PENDING"] != 5000 {
		t.Fatalf("portal settlement: %+v", view.Settlement.Totals)
	}

	// Respond via the portal token.
	env.doBearer("POST", "/v1/portal/"+token+"/respond", "",
		map[string]string{"assignment_id": assignmentID, "action": "accept"},
		http.StatusOK, nil)
	env.doBearer("POST", "/v1/portal/"+token+"/respond", "",
		map[string]string{"assignment_id": assignmentID, "action": "decline"},
		http.StatusConflict, nil)

	// Bogus token: 403.
	env.doBearer("GET", "/v1/portal/aaaa.bbbb", "", nil, http.StatusForbidden, nil)
	// A chauffeur trip token must not open the portal.
	tripToken := env.svc.Trips.TokenFor(env.tenantID, assignmentID)
	env.doBearer("GET", "/v1/portal/"+tripToken, "", nil, http.StatusForbidden, nil)

	// WhatsApp notify: partner has no phone → honest skip, link present.
	var notify struct {
		PortalURL string `json:"portal_url"`
		WhatsApp  string `json:"whatsapp"`
	}
	env.do("POST", "/v1/partners/"+partnerID+"/notify", env.tenantID, nil, http.StatusOK, &notify)
	if notify.WhatsApp != "skipped:no-phone" || notify.PortalURL == "" {
		t.Fatalf("notify: %+v", notify)
	}
}
