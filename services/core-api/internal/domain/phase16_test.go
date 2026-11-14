package domain

// Phase 21: outreach templates, per-tenant operator rosters, webhook
// dual-secret rotation, event schema versioning.

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

// ------------------------------------------------------------- templates

func TestTemplateCRUDAndRender(t *testing.T) {
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	tmpl, err := s.Outreach.UpsertTemplate(ctx, tenantID, "winback",
		model.OutreachMarketing, "We miss you, {{client_name}} — {{tenant_name}} has a driver waiting.")
	if err != nil {
		t.Fatal(err)
	}
	// Re-saving the same name updates in place (console save button).
	again, err := s.Outreach.UpsertTemplate(ctx, tenantID, "winback",
		model.OutreachMarketing, "New body for {{client_name}}.")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != tmpl.ID {
		t.Fatal("upsert by (tenant, name) must update in place, not duplicate")
	}
	list, err := s.Outreach.ListTemplates(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Body != "New body for {{client_name}}." {
		t.Fatalf("list: %+v", list)
	}
	if list[0].Purpose != model.OutreachMarketing {
		t.Fatalf("purpose must update on re-save: %s", list[0].Purpose)
	}

	// Render substitution.
	cl, err := s.Store.Clients().Get(ctx, tenantID,
		outreachClient(t, s, tenantID, "+971500000090", false))
	if err != nil {
		t.Fatal(err)
	}
	cl.FullName = "Ada Lovelace"
	if err := s.Store.Clients().Upsert(ctx, cl); err != nil {
		t.Fatal(err)
	}
	tenant, _ := s.Store.Tenants().Get(ctx, tenantID)
	rendered := RenderTemplate("Hi {{client_name}}, from {{tenant_name}}. {{unknown}}", cl, tenant)
	if !strings.Contains(rendered, "Ada Lovelace") || !strings.Contains(rendered, "Taylor") {
		t.Fatalf("render: %q", rendered)
	}
	if !strings.Contains(rendered, "{{unknown}}") {
		t.Fatal("unknown placeholders stay visible — never silently dropped")
	}

	// Validation + delete.
	if _, err := s.Outreach.UpsertTemplate(ctx, tenantID, "x", "any", "body"); err == nil {
		t.Fatal("name too short must be rejected")
	}
	if _, err := s.Outreach.UpsertTemplate(ctx, tenantID, "ok-name", "any", ""); err == nil {
		t.Fatal("empty body must be rejected")
	}
	if err := s.Outreach.DeleteTemplate(ctx, tenantID, tmpl.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Outreach.ListTemplates(ctx, tenantID); len(list) != 0 {
		t.Fatal("delete must remove the template")
	}
}

func TestTemplateSendRenderedAndPurposeGated(t *testing.T) {
	var mu sync.Mutex
	var contacts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		contacts = append(contacts, body["contact"].(string))
		mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"mode": "freeform",
			"body": map[string]any{"messages": []map[string]string{{"id": "wamid.tpl-1"}}}})
	}))
	defer srv.Close()

	s := testServices(t, Deps{WhatsAppURL: srv.URL})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	clientID := outreachClient(t, s, tenantID, "+971500000091", false)

	tmpl, err := s.Outreach.UpsertTemplate(ctx, tenantID, "arrival-note",
		model.OutreachService, "Welcome {{client_name}} — your {{tenant_name}} chauffeur is outside.")
	if err != nil {
		t.Fatal(err)
	}
	o, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService,
		TemplateID: tmpl.ID, IdempotencyKey: "tpl-send-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if o.Status != "sent" {
		t.Fatalf("template send must deliver: %s %s", o.Status, o.Detail)
	}
	// Provenance: the recorded body is the RENDERED text, not the template.
	if !strings.Contains(o.Body, "Growth Client") || !strings.Contains(o.Body, "Taylor") ||
		strings.Contains(o.Body, "{{") {
		t.Fatalf("recorded body must be rendered: %q", o.Body)
	}

	// A marketing template cannot ride the ungated service lane.
	mt, err := s.Outreach.UpsertTemplate(ctx, tenantID, "promo-blast",
		model.OutreachMarketing, "50% off, {{client_name}}!")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Outreach.Send(ctx, tenantID, SendOutreachInput{
		ClientID: clientID, Purpose: model.OutreachService,
		TemplateID: mt.ID, IdempotencyKey: "tpl-send-2",
	}); err == nil {
		t.Fatal("marketing template sent as service must be refused")
	}
}

// ------------------------------------------------------------- operator roster

func TestOperatorRosterAlert(t *testing.T) {
	var mu sync.Mutex
	var contacts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if c, ok := body["contact"].(string); ok {
			mu.Lock()
			contacts = append(contacts, c)
			mu.Unlock()
		}
		json.NewEncoder(w).Encode(map[string]any{"mode": "freeform",
			"body": map[string]any{"messages": []map[string]string{{"id": "wamid.reply-1"}}}})
	}))
	defer srv.Close()
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string{}, contacts...)
	}

	// Tenant with a two-phone roster: both get the alert, global does not.
	s := testServices(t, Deps{WhatsAppURL: srv.URL, OperatorPhone: "+971000000000"})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	tenant, err := s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		t.Fatal(err)
	}
	tenant.OperatorConfig = model.OperatorConfig{AlertPhones: []string{"+971111111111", "+971222222222"}}
	if _, err := s.Tenants.UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	sendReplyLinked(t, s, tenantID)
	before := len(got())
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "failed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	newContacts := got()[before:]
	seen := map[string]bool{}
	for _, c := range newContacts {
		seen[c] = true
	}
	if !seen["+971111111111"] || !seen["+971222222222"] {
		t.Fatalf("roster phones must receive the alert: %v", newContacts)
	}
	if seen["+971000000000"] {
		t.Fatal("roster overrides the global operator phone")
	}

	// Empty roster: global fallback still fires.
	tenant.OperatorConfig = model.OperatorConfig{}
	if _, err := s.Tenants.UpdateConfig(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	before = len(got())
	if _, err := s.Receipts.Record(ctx, tenantID, "whatsapp", "wamid.reply-1", "failed", time.Time{}); err != nil {
		t.Fatal(err)
	}
	newContacts = got()[before:]
	if len(newContacts) != 1 || newContacts[0] != "+971000000000" {
		t.Fatalf("empty roster must fall back to the global phone: %v", newContacts)
	}
}

// ------------------------------------------------------------- dual-secret rotation

type sigCapture struct {
	mu   sync.Mutex
	sigs []string
	body []byte
}

func (c *sigCapture) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		c.mu.Lock()
		c.sigs = append(c.sigs, r.Header.Get("X-Concierge-Signature"))
		c.body = b
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}
}

func TestWebhookDualSecretRotation(t *testing.T) {
	cap := &sigCapture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()

	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)

	sub, err := s.Webhooks.CreateSubscription(ctx, tenantID, srv.URL, []string{"booking.*"})
	if err != nil {
		t.Fatal(err)
	}
	oldSecret := sub.Secret

	deliver := func() string {
		s.Webhooks.FanOut(ctx, tenantID, "booking.confirmed", "evt-"+NewID()[:6],
			map[string]any{"quote_id": "q1"})
		if _, err := s.Webhooks.DispatchDue(ctx, s.Now(), 10); err != nil {
			t.Fatal(err)
		}
		cap.mu.Lock()
		defer cap.mu.Unlock()
		return cap.sigs[len(cap.sigs)-1]
	}

	// Before rotation: exactly one signature, from the original secret.
	if sig := deliver(); sig != SignDelivery(oldSecret, cap.body) {
		t.Fatalf("pre-rotation signature must be the original secret: %s", sig)
	}

	// Rotate: during the window BOTH secrets sign — new first, prev second.
	rotated, err := s.Webhooks.RotateSecret(ctx, tenantID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Secret == oldSecret || rotated.PrevSecret != oldSecret {
		t.Fatal("rotation must replace the secret and retain the previous one")
	}
	sig := deliver()
	parts := strings.Split(sig, ",")
	if len(parts) != 2 {
		t.Fatalf("inside the window the header must carry two signatures: %q", sig)
	}
	cap.mu.Lock()
	body := append([]byte{}, cap.body...)
	cap.mu.Unlock()
	if parts[0] != SignDelivery(rotated.Secret, body) || parts[1] != SignDelivery(oldSecret, body) {
		t.Fatal("signatures must be (new, prev) so receivers can cut over")
	}

	// Window closed (rotate timestamp aged past the window): new secret only.
	aged, err := s.Store.Webhooks().GetSubscription(ctx, tenantID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	aged.SecretRotatedAt = s.Now().Add(-WebhookRotationWindow - time.Hour)
	if err := s.Store.Webhooks().UpdateSubscription(ctx, aged); err != nil {
		t.Fatal(err)
	}
	if sig := deliver(); sig != SignDelivery(rotated.Secret, cap.body) {
		t.Fatalf("after the window only the new secret signs: %q", sig)
	}
}

// ------------------------------------------------------------- schema versioning

func TestEventSchemaVersioning(t *testing.T) {
	// Every catalog entry exposes a version >= 1.
	seen := map[string]bool{}
	for _, e := range CatalogJSON() {
		if e.Version < 1 {
			t.Fatalf("%s: version must be >= 1", e.Kind)
		}
		seen[e.Kind] = true
	}
	if !seen["booking.confirmed"] || !seen["promo.redeemed"] {
		t.Fatal("catalog sanity")
	}
	if CatalogVersion("webhook.test") != 1 || CatalogVersion("nonexistent.kind") != 1 {
		t.Fatal("unlisted kinds default to schema v1")
	}

	// The webhook envelope stamps the version so receivers pin parsing.
	cap := &sigCapture{}
	srv := httptest.NewServer(cap.handler())
	defer srv.Close()
	s := testServices(t, Deps{})
	ctx := context.Background()
	tenantID := seedTenant(t, s)
	if _, err := s.Webhooks.CreateSubscription(ctx, tenantID, srv.URL, []string{"*"}); err != nil {
		t.Fatal(err)
	}
	s.Webhooks.FanOut(ctx, tenantID, "booking.confirmed", "evt-v1", map[string]any{"quote_id": "q1"})
	if _, err := s.Webhooks.DispatchDue(ctx, s.Now(), 10); err != nil {
		t.Fatal(err)
	}
	cap.mu.Lock()
	body := append([]byte{}, cap.body...)
	cap.mu.Unlock()
	var env struct {
		Type          string         `json:"type"`
		SchemaVersion int            `json:"schema_version"`
		Data          map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if env.Type != "booking.confirmed" {
		t.Fatalf("envelope type: %s", env.Type)
	}
	if env.SchemaVersion != CatalogVersion("booking.confirmed") {
		t.Fatalf("envelope schema_version %d must match the catalog %d",
			env.SchemaVersion, CatalogVersion("booking.confirmed"))
	}
	if env.Data["quote_id"] != "q1" {
		t.Fatal("payload must survive the envelope unchanged")
	}
}
