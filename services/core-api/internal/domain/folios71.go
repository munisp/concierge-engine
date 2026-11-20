package domain

// Phase 71: statement delivery. A statement the partner never receives
// is a statement that never gets paid. This phase closes the loop the
// dunning cadence (Phase 67) started: dunning chases, but the chase
// message pointed at the portal — the partner still had to find the
// document. Now the operator can deliver the statement itself:
//
//   - A stmt/ HMAC capability token (same discipline as track/,
//     consent/, unsub/ — the token IS the credential, cross-family
//     tokens rejected) opens a public, printable statement page. The
//     page is the invoice artifact: partner saves it as PDF from the
//     browser (print-to-PDF, honest — no fake "PDF generation").
//   - POST /v1/folios/{id}/send-statement delivers the statement link
//     over the partner's best channel: WhatsApp when a phone is on
//     file, email otherwise (Phase 71 partners.email — the billing
//     contact a hotel desk actually reads). No contact at all is
//     recorded verbatim as skipped:no-contact, never faked as sent.
//   - Every delivery lands on the folio's own deliveries ledger
//     (migration 0047) — the statement carries its own delivery
//     evidence next to the reminder and payment ledgers.
//
// ISSUED-only: an OPEN folio is still accruing (delivering it would
// freeze a moving total in the partner's inbox) and a SETTLED folio
// needs no collection document.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

const statementTokenPrefix = "stmt/"

// StatementTokenFor mints the public statement-page token for a folio.
func (svc *TripService) StatementTokenFor(tenantID, folioID string) string {
	payload := statementTokenPrefix + tenantID + "/" + folioID
	mac := hmac.New(sha256.New, svc.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))
}

// ParseStatementToken verifies and extracts (tenantID, folioID). Tokens
// from other families (track/, consent/, partner/) are rejected — a
// trip link can never open a financial statement.
func (svc *TripService) ParseStatementToken(token string) (string, string, error) {
	_, _, payload, err := svc.parseAny(token)
	if err != nil {
		return "", "", err
	}
	rest, ok := strings.CutPrefix(payload, statementTokenPrefix)
	if !ok {
		return "", "", httpx.ErrForbidden
	}
	return splitPayload(rest)
}

// StatementURL is the public, printable statement address.
func (svc *FolioService) StatementURL(tenantID, folioID string) string {
	return svc.s.PublicBase() + "/v1/statement/" + svc.s.Trips.StatementTokenFor(tenantID, folioID)
}

// StatementView is the folio rendered as the partner-facing document:
// the frozen statement plus derived due/overdue (same derivation as
// aging, so the document can never disagree with the operator's books)
// plus the verbatim payment, reminder and delivery ledgers.
type StatementView struct {
	TenantName       string                `json:"tenant_name"`
	PartnerName      string                `json:"partner_name"`
	FolioID          string                `json:"folio_id"`
	Period           string                `json:"period"`
	Currency         string                `json:"currency"`
	State            string                `json:"state"`
	TotalMinor       int64                 `json:"total_minor"`
	PaidMinor        int64                 `json:"paid_minor"`
	OutstandingMinor int64                 `json:"outstanding_minor"`
	TermsDays        int                   `json:"terms_days"`
	OverdueDays      int                   `json:"overdue_days"` // ISSUED only; <= 0 inside terms
	Lines            []model.FolioLine     `json:"lines,omitempty"`
	Payments         []model.FolioPayment  `json:"payments,omitempty"`
	Reminders        []model.FolioReminder `json:"reminders,omitempty"`
	Deliveries       []model.FolioDelivery `json:"deliveries,omitempty"`
	IssuedAt         *time.Time            `json:"issued_at,omitempty"`
	DueAt            *time.Time            `json:"due_at,omitempty"` // derived, never stored
	SettledAt        *time.Time            `json:"settled_at,omitempty"`
}

// StatementByToken resolves the public statement page's view model.
// Works for any folio state — a partner who saved the link can re-read
// the document after settlement (the page shows it as PAID).
func (svc *FolioService) StatementByToken(ctx context.Context, token string) (*StatementView, error) {
	tenantID, folioID, err := svc.s.Trips.ParseStatementToken(token)
	if err != nil {
		return nil, err
	}
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	partner, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	v := &StatementView{
		TenantName: tenant.Name, PartnerName: partner.Name,
		FolioID: f.ID, Period: f.Period, Currency: f.Currency, State: f.State,
		TotalMinor: f.TotalMinor, PaidMinor: f.PaidMinor(), OutstandingMinor: f.OutstandingMinor(),
		TermsDays: terms, Lines: f.Lines, Payments: f.Payments,
		Reminders: f.Reminders, Deliveries: f.Deliveries,
	}
	if !f.IssuedAt.IsZero() {
		t := f.IssuedAt
		v.IssuedAt = &t
	}
	if !f.SettledAt.IsZero() {
		t := f.SettledAt
		v.SettledAt = &t
	}
	if f.State == model.FolioIssued {
		d := f.DueAt(terms)
		v.DueAt = &d
		v.OverdueDays = f.OverdueDays(svc.s.Now(), terms)
	}
	return v, nil
}

// SendStatement (operator, console) delivers the statement document to
// the partner over their best channel and records the outcome verbatim
// on the deliveries ledger — "sent" only when the channel adapter
// accepted it. ISSUED-only (409 otherwise): the document is a
// collection artifact. A store failure on the ledger write is
// audit-logged and reported, never swallowed (same discipline as
// sendReminder, Phase 67).
func (svc *FolioService) SendStatement(ctx context.Context, tenantID, folioID string) (*model.Folio, error) {
	f, err := svc.s.Store.Folios().Get(ctx, tenantID, folioID)
	if err != nil {
		return nil, err
	}
	if f.State != model.FolioIssued {
		return nil, httpx.ErrConflict // only an issued statement is delivered
	}
	tenant, err := svc.s.Store.Tenants().Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	p, err := svc.s.Store.Partners().Get(ctx, tenantID, f.PartnerID)
	if err != nil {
		return nil, err
	}
	terms := tenant.PricingRules.FolioTerms()
	now := svc.s.Now()
	d := model.FolioDelivery{ID: NewID(), Kind: "statement", CreatedAt: now}
	url := svc.StatementURL(tenantID, f.ID)
	due := f.DueAt(terms).Format("2006-01-02")
	outstanding := f.OutstandingMinor()
	switch {
	case p.Phone != "":
		d.Channel = "whatsapp"
		if svc.s.waURL == "" {
			d.Status = "skipped:no-adapter"
			break
		}
		body := fmt.Sprintf("Statement %s from %s — %d.%02d %s due %s (%d.%02d %s still owed). View and save your statement: %s",
			f.Period, tenant.Name, f.TotalMinor/100, abs64(f.TotalMinor)%100, f.Currency, due,
			outstanding/100, abs64(outstanding)%100, f.Currency, url)
		status, detail, _ := postToAdapter(ctx, svc.s.waURL, []byte(fmt.Sprintf(
			`{"tenant_id":%q,"contact":%q,"body":%q}`, tenantID, p.Phone, body)))
		d.Status = deliveryStatus(status, detail)
	case p.Email != "":
		d.Channel = "email"
		if svc.s.emailURL == "" {
			d.Status = "skipped:no-adapter"
			break
		}
		subject := fmt.Sprintf("Statement %s from %s — %d.%02d %s due %s",
			f.Period, tenant.Name, outstanding/100, abs64(outstanding)%100, f.Currency, due)
		body := fmt.Sprintf("Dear %s,\n\nYour statement for %s was issued on %s and is due %s (terms %d days).\n\nTotal: %d.%02d %s\nPaid: %d.%02d %s\nOutstanding: %d.%02d %s\n\nView, print or save your statement: %s\n\n%s",
			p.Name, f.Period, f.IssuedAt.Format("2006-01-02"), due, terms,
			f.TotalMinor/100, abs64(f.TotalMinor)%100, f.Currency,
			f.PaidMinor()/100, abs64(f.PaidMinor())%100, f.Currency,
			outstanding/100, abs64(outstanding)%100, f.Currency,
			url, tenant.Name)
		status, detail, _ := postToAdapter(ctx, svc.s.emailURL, []byte(fmt.Sprintf(
			`{"tenant_id":%q,"to":%q,"subject":%q,"body":%q}`, tenantID, p.Email, subject, body)))
		d.Status = deliveryStatus(status, detail)
	default:
		d.Channel = "none"
		d.Status = "skipped:no-contact"
	}
	f.Deliveries = append(f.Deliveries, d)
	if err := svc.s.Store.Folios().Update(ctx, f); err != nil {
		svc.s.Audit.Log(ctx, tenantID, "system", "channel.folio_statement_failed", "partner", f.PartnerID,
			map[string]any{"folio_id": f.ID, "error": err.Error()})
		return nil, err
	}
	event := "channel.folio_statement_sent"
	if d.Status != model.FolioReminderSent {
		event = "channel.folio_statement_skipped"
	}
	svc.s.Events.Emit(ctx, tenantID, "partner", f.PartnerID, event,
		map[string]any{"folio_id": f.ID, "delivery_id": d.ID, "channel": d.Channel, "status": d.Status,
			"period": f.Period, "currency": f.Currency, "outstanding_minor": f.OutstandingMinor()})
	svc.s.Audit.Log(ctx, tenantID, "operator", event, "partner", f.PartnerID,
		map[string]any{"folio_id": f.ID, "channel": d.Channel, "status": d.Status})
	return svc.s.Store.Folios().Get(ctx, tenantID, folioID)
}

// deliveryStatus maps the adapter verdict onto the delivery ledger,
// preserving the verbatim failure detail like the reminders ledger.
func deliveryStatus(status, detail string) string {
	if status != model.FolioReminderSent && detail != "" {
		return status + ":" + detail
	}
	return status
}
