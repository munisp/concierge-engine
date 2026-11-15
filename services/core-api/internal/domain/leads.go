// Lead engine (Phase 36): first-class pre-client prospects in four kinds —
// person, corporate, partner, event — with a pipeline, deterministic
// scoring, speed-to-lead SLA alerts, and conversion into the platform's
// real records (Client, CorporateAccount, Partner).
//
// The engine is deliberately a thin CRM layer ON TOP of the existing
// acquisition and outreach spine, never a parallel one:
//
//   - Capture resolves (or creates) the messaging-identity Client through
//     Clients.ResolveV2 — the same resolver every other surface uses — so
//     nurture inherits consent gates, frequency caps, quiet hours, STOP/
//     START and drip sequences (Phases 14/35) unchanged.
//   - Dedup is a domain decision: a repeat inquiry from an already-open
//     lead backfills and re-scores the existing record instead of
//     spawning a duplicate row. Unkeyed creates still insert — the store
//     layer never swallows signal.
//   - Conversion routes per kind into the record the business actually
//     works: person/event -> the linked client (ready to book),
//     corporate -> a CorporateAccount (+ OrgID link, mirroring the
//     self-serve capture), partner -> a PENDING Partner in the vetting
//     queue.
//   - lead_captured / lead_converted attribution events feed the Phase 14
//     funnel, so the growth board reads lead generation next to every
//     other motion.
package domain

import (
	"context"
	"fmt"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type LeadService struct{ s *Services }

var leadKinds = map[string]bool{
	model.LeadKindPerson:    true,
	model.LeadKindCorporate: true,
	model.LeadKindPartner:   true,
	model.LeadKindEvent:     true,
}

var leadStatuses = map[string]bool{
	model.LeadStatusNew:       true,
	model.LeadStatusContacted: true,
	model.LeadStatusQualified: true,
	model.LeadStatusConverted: true,
	model.LeadStatusLost:      true,
}

// contactBases (Phase 38) are the lawful bases a lead may carry. The basis
// gates marketing consent: self_reported and partner_consent can hold a
// grant; b2b_legitimate_interest is human-outreach-only.
var contactBases = map[string]bool{
	model.ContactBasisSelfReported:   true,
	model.ContactBasisPartnerConsent: true,
	model.ContactBasisB2B:            true,
}

// leadTransitions is the legal pipeline graph. CONVERTED is reachable only
// through Convert (it mints real records), so it is absent here; terminal
// states are locked.
var leadTransitions = map[string]map[string]bool{
	model.LeadStatusNew:       {model.LeadStatusContacted: true, model.LeadStatusQualified: true, model.LeadStatusLost: true},
	model.LeadStatusContacted: {model.LeadStatusQualified: true, model.LeadStatusLost: true},
	model.LeadStatusQualified: {model.LeadStatusLost: true},
}

// CaptureLeadInput is the public/tenant capture payload. Kind-specific
// fields are validated against the kind (a corporate lead without a
// company is a form bug, not a prospect).
type CaptureLeadInput struct {
	Kind      string     `json:"kind"`
	Name      string     `json:"name"`
	Phone     string     `json:"phone,omitempty"`
	Email     string     `json:"email,omitempty"`
	Company   string     `json:"company,omitempty"`
	EventType string     `json:"event_type,omitempty"`
	EventDate *time.Time `json:"event_date,omitempty"`
	Headcount int        `json:"headcount,omitempty"`
	Venue     string     `json:"venue,omitempty"`
	Verticals []string   `json:"verticals,omitempty"`
	Notes     string     `json:"notes,omitempty"`

	Source       string `json:"source"` // capture surface: site | api | widget | import
	Channel      string `json:"channel,omitempty"`
	Campaign     string `json:"campaign,omitempty"`
	ReferralCode string `json:"referral_code,omitempty"`

	// Phase 38 provenance. ListSource names the vendor/partner the row
	// came from; ContactBasis states the lawful basis for contact.
	// Empty basis defaults to self_reported (organic inbound); Import
	// defaults it to b2b_legitimate_interest instead (a bought list must
	// say so, or be treated as one).
	ListSource   string `json:"list_source,omitempty"`
	ContactBasis string `json:"contact_basis,omitempty"`

	ConsentMarketing bool   `json:"consent_marketing"`
	IdempotencyKey   string `json:"idempotency_key,omitempty"`
}

// CaptureResult reports whether the returned lead is a brand-new record or
// an existing open lead that absorbed a repeat inquiry.
type CaptureResult struct {
	Lead    *model.Lead `json:"lead"`
	Deduped bool        `json:"deduped"`
}

// Capture validates the brief, dedups against open leads by phone/email,
// resolves the messaging-identity client, mirrors consent grant-only,
// scores, persists (idempotent on IdempotencyKey) and tracks attribution.
func (svc *LeadService) Capture(ctx context.Context, tenantID string, in CaptureLeadInput) (*CaptureResult, error) {
	in.Kind = strings.ToLower(strings.TrimSpace(in.Kind))
	if !leadKinds[in.Kind] {
		return nil, fmt.Errorf("%w: kind must be person, corporate, partner or event", httpx.ErrValidation)
	}
	in.Name = strings.TrimSpace(in.Name)
	in.Phone = normLeadPhone(in.Phone)
	in.Email = normLeadEmail(in.Email)
	in.Company = strings.TrimSpace(in.Company)
	if in.Name == "" || (in.Phone == "" && in.Email == "") {
		// A lead we cannot contact is not a lead.
		return nil, fmt.Errorf("%w: name and a phone or email are required", httpx.ErrValidation)
	}
	if in.Source == "" {
		in.Source = "api"
	}
	// Phase 38 provenance: the basis must be one of the known values, and
	// organic inbound defaults to self_reported. Marketing consent is
	// refused on a b2b_legitimate_interest basis regardless of what the
	// caller claims — a bought list earns consent at first reply, never
	// at import time.
	in.ContactBasis = strings.ToLower(strings.TrimSpace(in.ContactBasis))
	if in.ContactBasis == "" {
		in.ContactBasis = model.ContactBasisSelfReported
	}
	if !contactBases[in.ContactBasis] {
		return nil, fmt.Errorf("%w: contact basis must be self_reported, partner_consent or b2b_legitimate_interest", httpx.ErrValidation)
	}
	in.ListSource = strings.TrimSpace(in.ListSource)
	if in.ContactBasis == model.ContactBasisB2B && in.ConsentMarketing {
		return nil, fmt.Errorf("%w: marketing consent requires a self_reported or partner_consent contact basis", httpx.ErrValidation)
	}

	// Dedup: an open lead with the same phone or email absorbs the repeat
	// inquiry (backfill + re-score). A repeat inquiry is buying signal,
	// not a new row — and it must not have to re-state the whole brief,
	// so this runs before kind-specific validation.
	if existing := svc.findOpenDuplicate(ctx, tenantID, in.Phone, in.Email); existing != nil {
		// Same idempotency key on the same lead = the same submission
		// retried, not new signal: conflict out without mutating state.
		if in.IdempotencyKey != "" && existing.IdempotencyKey == in.IdempotencyKey {
			return nil, httpx.ErrConflict
		}
		backfillLead(existing, in)
		svc.score(ctx, tenantID, existing)
		existing.UpdatedAt = svc.s.Now()
		if in.ConsentMarketing && !existing.ConsentMarketing && existing.ClientID != "" {
			// Phase 38: consent earned on a repeat touch (the reply to
			// human outreach, a booth conversation) must reach the
			// messaging identity — grant-only, never a revocation. This
			// is the moment a bought B2B list legally becomes marketable.
			if _, err := svc.s.Clients.SetConsentSourced(ctx, tenantID, existing.ClientID, "marketing", true, model.ConsentSourceLeadRepeatTouch, "repeat touch on "+string(existing.Kind)+" lead "+existing.ID); err != nil {
				return nil, err
			}
			existing.ConsentMarketing = true
			svc.score(ctx, tenantID, existing) // re-score with the grant
		}
		if err := svc.s.Store.Leads().Update(ctx, existing); err != nil {
			return nil, err
		}
		return &CaptureResult{Lead: existing, Deduped: true}, nil
	}

	// Kind-specific brief validation applies to NEW leads only.
	switch in.Kind {
	case model.LeadKindCorporate, model.LeadKindPartner:
		if in.Company == "" {
			return nil, fmt.Errorf("%w: %s leads require a company", httpx.ErrValidation, in.Kind)
		}
	case model.LeadKindEvent:
		if in.EventType == "" && in.EventDate == nil && in.Venue == "" {
			// An event brief needs at least one anchor.
			return nil, fmt.Errorf("%w: event leads require an event type, date or venue", httpx.ErrValidation)
		}
	}

	// Messaging identity: the linked client carries consent, quiet hours
	// and drip progress for every future touch.
	res, err := svc.s.Clients.ResolveV2(ctx, tenantID, ResolveInput{
		Phone: in.Phone, Email: in.Email, Name: in.Name,
	})
	if err != nil {
		return nil, err
	}
	if in.ConsentMarketing {
		// Grant-only: capture never revokes an existing grant.
		if _, err := svc.s.Clients.SetConsentSourced(ctx, tenantID, res.Client.ID, "marketing", true, model.ConsentSourceLeadCapture, "lead capture ("+in.Kind+")"); err != nil {
			return nil, err
		}
	}

	now := svc.s.Now()
	lead := &model.Lead{
		ID: NewID(), TenantID: tenantID, Kind: in.Kind, Name: in.Name,
		Phone: in.Phone, Email: in.Email, Company: in.Company,
		EventType: in.EventType, EventDate: in.EventDate, Headcount: in.Headcount, Venue: strings.TrimSpace(in.Venue),
		Verticals: in.Verticals, Notes: strings.TrimSpace(in.Notes),
		Source: in.Source, Channel: in.Channel, Campaign: in.Campaign, ReferralCode: in.ReferralCode,
		ListSource: in.ListSource, ContactBasis: in.ContactBasis,
		Status: model.LeadStatusNew, ConsentMarketing: in.ConsentMarketing,
		ClientID:       res.Client.ID,
		IdempotencyKey: strings.TrimSpace(in.IdempotencyKey),
		CreatedAt:      now, UpdatedAt: now,
	}
	svc.score(ctx, tenantID, lead)
	if err := svc.s.Store.Leads().Create(ctx, lead); err != nil {
		return nil, err
	}
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "lead_captured", Channel: in.Channel,
		Campaign: in.Campaign, ClientID: res.Client.ID,
	})
	svc.s.Events.Emit(ctx, tenantID, "lead", lead.ID, "lead.captured", map[string]any{
		"kind": lead.Kind, "source": lead.Source, "score": lead.Score,
		"contact_basis": lead.ContactBasis,
	})
	return &CaptureResult{Lead: lead}, nil
}

// findOpenDuplicate scans open leads for a phone or email match. Lead
// volume per tenant is operator-scale (hundreds, not millions), so a scan
// bounded by the list limit is the honest implementation.
func (svc *LeadService) findOpenDuplicate(ctx context.Context, tenantID, phone, email string) *model.Lead {
	leads, err := svc.s.Store.Leads().List(ctx, tenantID, 5000)
	if err != nil {
		return nil
	}
	for i := range leads {
		l := &leads[i]
		if !l.Open() {
			continue
		}
		if phone != "" && l.Phone == phone {
			return l
		}
		if email != "" && l.Email == email {
			return l
		}
	}
	return nil
}

// backfillLead merges a repeat inquiry into the existing lead: empty
// fields are filled, non-empty brief fields that grew (headcount,
// verticals) take the richer value. Existing facts are never overwritten
// with empties.
func backfillLead(l *model.Lead, in CaptureLeadInput) {
	if l.Phone == "" {
		l.Phone = in.Phone
	}
	if l.Email == "" {
		l.Email = in.Email
	}
	if l.Name == "" {
		l.Name = in.Name
	}
	if l.Company == "" {
		l.Company = in.Company
	}
	if l.EventType == "" {
		l.EventType = in.EventType
	}
	if l.EventDate == nil {
		l.EventDate = in.EventDate
	}
	if in.Headcount > l.Headcount {
		l.Headcount = in.Headcount
	}
	if l.Venue == "" {
		l.Venue = strings.TrimSpace(in.Venue)
	}
	if len(in.Verticals) > len(l.Verticals) {
		l.Verticals = in.Verticals
	}
	if l.Notes == "" {
		l.Notes = strings.TrimSpace(in.Notes)
	}
	if l.ReferralCode == "" {
		l.ReferralCode = in.ReferralCode
	}
	if l.ListSource == "" {
		l.ListSource = in.ListSource
	}
	// A repeat inquiry never upgrades the contact basis: provenance is set
	// at first capture and only fills in when previously unknown.
	if l.ContactBasis == "" {
		l.ContactBasis = in.ContactBasis
	}
	if l.Campaign == "" {
		l.Campaign = in.Campaign
	}
	if l.Channel == "" {
		l.Channel = in.Channel
	}
}

// score assigns the deterministic 0-100 lead score with human-readable
// reasons, so the console can show WHY a lead ranks where it does.
// Deterministic and capped: no model, no drift, fully unit-testable.
func (svc *LeadService) score(ctx context.Context, tenantID string, l *model.Lead) {
	score := 0
	reasons := []string{}
	add := func(points int, reason string) {
		score += points
		reasons = append(reasons, fmt.Sprintf("%+d %s", points, reason))
	}
	switch l.Kind {
	case model.LeadKindEvent:
		add(20, "event brief")
	case model.LeadKindCorporate:
		add(15, "corporate inquiry")
	case model.LeadKindPartner:
		add(10, "partner inquiry")
	default:
		add(5, "person inquiry")
	}
	if l.Phone != "" {
		add(10, "phone on file")
	}
	if l.Email != "" {
		add(5, "email on file")
	}
	if l.Company != "" {
		add(5, "company named")
	}
	if l.Kind == model.LeadKindEvent {
		if l.Headcount >= 20 {
			add(10, "party of 20+")
		}
		if l.Headcount >= 50 {
			add(5, "party of 50+")
		}
		if l.EventDate != nil && !l.EventDate.After(svc.s.Now().Add(45*24*time.Hour)) {
			add(10, "event within 45 days")
		}
		if l.Venue != "" {
			add(5, "venue named")
		}
	}
	if l.Kind == model.LeadKindPartner && len(l.Verticals) > 0 {
		if svc.partnerVerticalOverlap(ctx, tenantID, l.Verticals) {
			add(10, "verticals match catalog")
		}
	}
	if l.ReferralCode != "" {
		add(10, "came via referral")
	}
	if l.ConsentMarketing {
		add(5, "marketing consent")
	}
	if score > 100 {
		score = 100
	}
	l.Score = score
	l.ScoreReasons = reasons
}

// partnerVerticalOverlap reports whether any partner-brief vertical appears
// in the tenant's service catalog — supply we can actually use.
func (svc *LeadService) partnerVerticalOverlap(ctx context.Context, tenantID string, verticals []string) bool {
	services, err := svc.s.Catalog.List(ctx, tenantID)
	if err != nil {
		return false
	}
	have := map[string]bool{}
	for _, cs := range services {
		if cs.Vertical != "" {
			have[strings.ToLower(cs.Vertical)] = true
		}
	}
	for _, v := range verticals {
		if have[strings.ToLower(strings.TrimSpace(v))] {
			return true
		}
	}
	return false
}

// Get returns one lead.
func (svc *LeadService) Get(ctx context.Context, tenantID, id string) (*model.Lead, error) {
	return svc.s.Store.Leads().Get(ctx, tenantID, id)
}

// List returns recent leads (created_at DESC), optionally filtered by kind
// and/or status in memory — filters are presentation-layer, not storage.
func (svc *LeadService) List(ctx context.Context, tenantID, kind, status string, limit int) ([]model.Lead, error) {
	if kind != "" && !leadKinds[kind] {
		return nil, httpx.ErrValidation
	}
	if status != "" && !leadStatuses[strings.ToUpper(status)] {
		return nil, httpx.ErrValidation
	}
	leads, err := svc.s.Store.Leads().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	out := make([]model.Lead, 0, len(leads))
	for _, l := range leads {
		if kind != "" && l.Kind != kind {
			continue
		}
		if status != "" && l.Status != strings.ToUpper(status) {
			continue
		}
		out = append(out, l)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Transition moves a lead along the pipeline graph. Same-state transitions
// are idempotent no-ops (safe retries from the console); CONVERTED is only
// reachable via Convert, and terminal states are locked.
func (svc *LeadService) Transition(ctx context.Context, tenantID, id, to, lostReason string) (*model.Lead, error) {
	to = strings.ToUpper(strings.TrimSpace(to))
	if !leadStatuses[to] {
		return nil, httpx.ErrValidation
	}
	if to == model.LeadStatusConverted {
		return nil, httpx.ErrValidation // conversion mints records — use Convert
	}
	lead, err := svc.s.Store.Leads().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if lead.Status == to {
		return lead, nil // idempotent
	}
	if !lead.Open() {
		return nil, httpx.ErrConflict // terminal states are locked
	}
	if !leadTransitions[lead.Status][to] {
		return nil, httpx.ErrValidation
	}
	if to == model.LeadStatusLost {
		lostReason = strings.TrimSpace(lostReason)
		if lostReason == "" {
			return nil, httpx.ErrValidation // a lost lead without a reason teaches nothing
		}
		lead.LostReason = lostReason
	}
	lead.Status = to
	lead.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Leads().Update(ctx, lead); err != nil {
		return nil, err
	}
	svc.s.Events.Emit(ctx, tenantID, "lead", lead.ID, "lead.transitioned", map[string]any{
		"to": to, "kind": lead.Kind,
	})
	return lead, nil
}

// ConvertResult carries the lead plus the record the conversion minted.
type ConvertResult struct {
	Lead    *model.Lead             `json:"lead"`
	Account *model.CorporateAccount `json:"account,omitempty"`
	Partner *model.Partner          `json:"partner,omitempty"`
}

// Convert turns a qualified lead into the platform's real record for its
// kind and closes the pipeline loop with a lead_converted attribution
// event. Idempotent: re-converting returns the existing links.
func (svc *LeadService) Convert(ctx context.Context, tenantID, id string) (*ConvertResult, error) {
	lead, err := svc.s.Store.Leads().Get(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if lead.Status == model.LeadStatusConverted {
		return &ConvertResult{Lead: lead}, nil // idempotent
	}
	if !lead.Open() {
		return nil, httpx.ErrConflict // a lost lead stays lost
	}
	res := &ConvertResult{Lead: lead}
	switch lead.Kind {
	case model.LeadKindCorporate:
		acct, err := svc.s.Corporate.CreateAccount(ctx, &model.CorporateAccount{
			TenantID: tenantID, Name: lead.Company, BillingEmail: lead.Email,
		})
		if err != nil {
			return nil, err
		}
		lead.ConvertedAccountID = acct.ID
		res.Account = acct
		// Mirror the self-serve corporate capture: link the contact's
		// client record to the account so trips bill to it.
		if lead.ClientID != "" {
			if c, err := svc.s.Store.Clients().Get(ctx, tenantID, lead.ClientID); err == nil && c.OrgID != acct.ID {
				c.OrgID = acct.ID
				_ = svc.s.Store.Clients().Upsert(ctx, c)
			}
		}
	case model.LeadKindPartner:
		name := lead.Company
		if name == "" {
			name = lead.Name
		}
		p, err := svc.s.Partners.Create(ctx, &model.Partner{
			TenantID: tenantID, Name: name, Verticals: lead.Verticals,
		})
		if err != nil {
			return nil, err
		}
		lead.ConvertedPartnerID = p.ID
		res.Partner = p
	default: // person, event
		lead.ConvertedClientID = lead.ClientID
	}
	lead.Status = model.LeadStatusConverted
	lead.UpdatedAt = svc.s.Now()
	if err := svc.s.Store.Leads().Update(ctx, lead); err != nil {
		return nil, err
	}
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "lead_converted", Channel: lead.Channel,
		Campaign: lead.Campaign, ClientID: lead.ClientID,
	})
	svc.s.Events.Emit(ctx, tenantID, "lead", lead.ID, "lead.converted", map[string]any{
		"kind": lead.Kind, "score": lead.Score,
	})
	return res, nil
}

// PipelineSummary is the console board: counts by status and kind plus the
// open total, computed from the same List the API serves.
type PipelineSummary struct {
	Total    int            `json:"total"`
	Open     int            `json:"open"`
	ByStatus map[string]int `json:"by_status"`
	ByKind   map[string]int `json:"by_kind"`
}

func (svc *LeadService) Pipeline(ctx context.Context, tenantID string) (*PipelineSummary, error) {
	leads, err := svc.s.Store.Leads().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	sum := &PipelineSummary{ByStatus: map[string]int{}, ByKind: map[string]int{}}
	for _, l := range leads {
		sum.Total++
		sum.ByStatus[l.Status]++
		sum.ByKind[l.Kind]++
		if l.Open() {
			sum.Open++
		}
	}
	return sum, nil
}

// SweepSLA is the speed-to-lead watch: any NEW lead older than the
// tenant's LeadSLAMinutes (default 60, negative disables) that has not
// been alerted yet pages the operator roster — once. Stamping
// SLAAlertedAt makes the sweep replay-safe.
func (svc *LeadService) SweepSLA(ctx context.Context, tenantID string) (int, error) {
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	slaMin := t.LifecycleConfig.LeadSLAMinutes
	if slaMin < 0 {
		return 0, nil // disabled
	}
	if slaMin == 0 {
		slaMin = 60
	}
	leads, err := svc.s.Store.Leads().List(ctx, tenantID, 5000)
	if err != nil {
		return 0, err
	}
	now := svc.s.Now()
	cutoff := now.Add(-time.Duration(slaMin) * time.Minute)
	phones := t.OperatorConfig.AlertPhones
	if len(phones) == 0 && svc.s.operatorPhone != "" {
		phones = []string{svc.s.operatorPhone}
	}
	alerted := 0
	for i := range leads {
		l := &leads[i]
		if l.Status != model.LeadStatusNew || l.SLAAlertedAt != nil || !l.CreatedAt.Before(cutoff) {
			continue
		}
		body := fmt.Sprintf("Lead SLA breach: %s (%s lead, score %d) has waited %d+ minutes without contact.",
			l.Name, l.Kind, l.Score, slaMin)
		for _, phone := range phones {
			pushWhatsAppAlert(ctx, svc.s.waURL, tenantID, phone, body)
		}
		l.SLAAlertedAt = &now
		l.UpdatedAt = now
		if err := svc.s.Store.Leads().Update(ctx, l); err != nil {
			return alerted, err
		}
		alerted++
	}
	return alerted, nil
}

func normLeadPhone(p string) string {
	p = strings.TrimSpace(p)
	var b strings.Builder
	for i, r := range p {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == '+' && i == 0 {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normLeadEmail(e string) string {
	return strings.ToLower(strings.TrimSpace(e))
}
