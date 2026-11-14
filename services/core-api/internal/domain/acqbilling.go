package domain

// Acquisition monetization (Phase 26): the platform's pay-for-performance
// revenue line. When a client's FIRST captured booking traces back — via
// first-touch attribution — to a platform acquisition channel (seo,
// referral, outreach, ads, partner), FeePct of the captured amount moves
// from TENANT_REVENUE to PLATFORM_ACQUISITION_FEES. Refunds claw the fee
// back: the platform only earns on money the tenant actually keeps.
//
// The cohort tracker paces each tenant against its new-client goal
// (default 100 in 90 days) — the count is derived from first-touch
// attribution, so both sides see the same indisputable evidence.

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/model"
)

const (
	acqFeeIdemPrefix      = "acq:fee:"
	acqClawbackIdemPrefix = "acq:refund-clawback:"
)

// defaultAcquisitionChannels is the platform-sourced taxonomy. Tenants can
// narrow it in acquisition_config.channels. Phase 27 adds the innovation
// pack surfaces: qr (printable booking codes), widget (embedded booking on
// external sites), gift (voucher-handed clients).
func defaultAcquisitionChannels() []string {
	return []string{"seo", "referral", "outreach", "ads", "partner", "qr", "widget", "gift"}
}

// classifyAcquisition maps a first-touch (channel, campaign) onto the
// acquisition taxonomy. "ref:<code>" campaigns are referrals regardless of
// the raw channel string (the capture page stamps them that way); "gift:"
// campaigns are gift-voucher acquisitions.
func classifyAcquisition(channel, campaign string) string {
	if strings.HasPrefix(campaign, "ref:") {
		return "referral"
	}
	if strings.HasPrefix(campaign, "gift:") {
		return "gift"
	}
	return strings.ToLower(strings.TrimSpace(channel))
}

func (svc *Services) acquisitionConfig(ctx context.Context, tenantID string) (model.AcquisitionConfig, error) {
	t, err := svc.Tenants.Get(ctx, tenantID)
	if err != nil {
		return model.AcquisitionConfig{}, err
	}
	cfg := t.AcquisitionConfig
	if cfg.FeePct <= 0 {
		cfg.FeePct = 0.10
	}
	if len(cfg.Channels) == 0 {
		cfg.Channels = defaultAcquisitionChannels()
	}
	if cfg.GoalClients <= 0 {
		cfg.GoalClients = 100
	}
	if cfg.GoalDays <= 0 {
		cfg.GoalDays = 90
	}
	return cfg, nil
}

// countClientBookings returns how many bookings the client holds with this
// tenant. The request join is memoized — bookings outnumber requests.
func (svc *Services) countClientBookings(ctx context.Context, tenantID, clientID string) (int, error) {
	bookings, err := svc.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return 0, err
	}
	n := 0
	reqClient := map[string]string{}
	for _, b := range bookings {
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		if cid == clientID {
			n++
		}
	}
	return n, nil
}

// assessAcquisitionFee runs at settlement. Errors propagate like any
// settlement posting — the posting is idempotent, so a failed capture can
// be retried without double-charging.
func (svc *PaymentService) assessAcquisitionFee(ctx context.Context, intent *model.PaymentIntent) error {
	cfg, err := svc.s.acquisitionConfig(ctx, intent.TenantID)
	if err != nil || cfg.Disabled {
		return err
	}
	if intent.BookingID == "" {
		return nil
	}
	b, err := svc.s.Store.Bookings().Get(ctx, intent.TenantID, intent.BookingID)
	if err != nil {
		return nil // no booking anchor — nothing to attribute
	}
	sr, err := svc.s.Store.Requests().Get(ctx, intent.TenantID, b.RequestID)
	if err != nil || sr.ClientID == "" {
		return nil
	}
	// First booking only: the fee prices ACQUISITION, not retention.
	n, err := svc.s.countClientBookings(ctx, intent.TenantID, sr.ClientID)
	if err != nil {
		return err
	}
	if n != 1 {
		return nil
	}
	channel, campaign := svc.s.FirstTouch(ctx, intent.TenantID, sr.ClientID)
	acq := classifyAcquisition(channel, campaign)
	eligible := false
	for _, c := range cfg.Channels {
		if c == acq {
			eligible = true
			break
		}
	}
	if !eligible {
		return nil
	}
	fee := int64(math.Round(float64(intent.AmountMinor) * cfg.FeePct))
	if fee <= 0 {
		return nil
	}
	if err := svc.post(ctx, intent.TenantID, intent.BookingID,
		"TENANT_REVENUE", "PLATFORM_ACQUISITION_FEES", fee, intent.Currency,
		acqFeeIdemPrefix+intent.ID); err != nil {
		return err
	}
	svc.s.Events.Emit(ctx, intent.TenantID, "booking", intent.BookingID, "acquisition_fee.assessed",
		map[string]any{"client_id": sr.ClientID, "channel": acq, "campaign": campaign,
			"amount_minor": intent.AmountMinor, "fee_minor": fee, "fee_pct": cfg.FeePct})
	return nil
}

// clawbackAcquisitionFee reverses the exact fee posted at capture (looked
// up by idempotency key, never recomputed — config changes between capture
// and refund must not move money). A refund never leaves the platform
// holding an acquisition fee on money the tenant returned.
func (svc *PaymentService) clawbackAcquisitionFee(ctx context.Context, intent *model.PaymentIntent) error {
	postings, err := svc.s.Store.Payments().Postings(ctx, intent.TenantID)
	if err != nil {
		return err
	}
	var fee int64
	var currency string
	for _, p := range postings {
		if p.IdempotencyKey == acqFeeIdemPrefix+intent.ID {
			fee, currency = p.AmountMinor, p.Currency
		}
	}
	if fee == 0 {
		return nil
	}
	return svc.post(ctx, intent.TenantID, intent.BookingID,
		"PLATFORM_ACQUISITION_FEES", "TENANT_REVENUE", fee, currency,
		acqClawbackIdemPrefix+intent.ID)
}

// ------------------------------------------------------------- invoice

type AcquisitionFeeLine struct {
	BookingID     string    `json:"booking_id"`
	ClientID      string    `json:"client_id,omitempty"`
	Channel       string    `json:"channel,omitempty"`
	Campaign      string    `json:"campaign,omitempty"`
	AmountMinor   int64     `json:"amount_minor"` // captured GMV this fee priced
	FeeMinor      int64     `json:"fee_minor"`
	RefundedMinor int64     `json:"refunded_minor,omitempty"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
}

type AcquisitionInvoice struct {
	TenantID           string               `json:"tenant_id"`
	From               time.Time            `json:"from"`
	To                 time.Time            `json:"to"`
	Lines              []AcquisitionFeeLine `json:"lines"`
	AttributedGMVMinor int64                `json:"attributed_gmv_minor"`
	GrossFeeMinor      int64                `json:"gross_fee_minor"`
	ClawbackMinor      int64                `json:"clawback_minor"`
	NetFeeMinor        int64                `json:"net_fee_minor"`
	Currency           string               `json:"currency"`
}

// AcquisitionInvoice builds the per-booking fee statement from the ledger
// mirror: fee postings are the gross, clawback postings net against them.
// Channel evidence is re-derived from first-touch attribution — the same
// source that justified the fee — so every line is auditable end to end.
func (svc *Services) AcquisitionInvoice(ctx context.Context, tenantID string, from, to time.Time) (*AcquisitionInvoice, error) {
	postings, err := svc.Store.Payments().Postings(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	inv := &AcquisitionInvoice{TenantID: tenantID, From: from, To: to, Lines: []AcquisitionFeeLine{}}
	byBooking := map[string]int{} // booking_id -> line index
	for _, p := range postings {
		if p.CreatedAt.Before(from) || !p.CreatedAt.Before(to) {
			continue
		}
		isFee := strings.HasPrefix(p.IdempotencyKey, acqFeeIdemPrefix)
		isClaw := strings.HasPrefix(p.IdempotencyKey, acqClawbackIdemPrefix)
		if !isFee && !isClaw {
			continue
		}
		idx, ok := byBooking[p.BookingID]
		if !ok {
			line := AcquisitionFeeLine{BookingID: p.BookingID, Currency: p.Currency, CreatedAt: p.CreatedAt}
			// Evidence join: booking -> request -> client -> first touch.
			if b, err := svc.Store.Bookings().Get(ctx, tenantID, p.BookingID); err == nil {
				if sr, err := svc.Store.Requests().Get(ctx, tenantID, b.RequestID); err == nil {
					line.ClientID = sr.ClientID
					ch, camp := svc.FirstTouch(ctx, tenantID, sr.ClientID)
					line.Channel, line.Campaign = classifyAcquisition(ch, camp), camp
				}
			}
			inv.Lines = append(inv.Lines, line)
			idx = len(inv.Lines) - 1
			byBooking[p.BookingID] = idx
		}
		if isFee {
			inv.Lines[idx].FeeMinor += p.AmountMinor
			inv.GrossFeeMinor += p.AmountMinor
			if inv.Currency == "" {
				inv.Currency = p.Currency
			}
			// The intent id rides in the idempotency key — recover the
			// captured GMV this fee priced (best-effort evidence join).
			intentID := strings.TrimPrefix(p.IdempotencyKey, acqFeeIdemPrefix)
			if intent, err := svc.Store.Payments().GetIntent(ctx, tenantID, intentID); err == nil {
				inv.Lines[idx].AmountMinor = intent.AmountMinor
				inv.AttributedGMVMinor += intent.AmountMinor
			}
		} else {
			inv.Lines[idx].RefundedMinor += p.AmountMinor
			inv.ClawbackMinor += p.AmountMinor
		}
	}
	inv.NetFeeMinor = inv.GrossFeeMinor - inv.ClawbackMinor
	return inv, nil
}

// ------------------------------------------------------------- cohort tracker

type CohortReport struct {
	GoalClients    int            `json:"goal_clients"`
	GoalDays       int            `json:"goal_days"`
	StartedAt      time.Time      `json:"started_at"`
	DaysElapsed    int            `json:"days_elapsed"`
	NewClients     int            `json:"new_clients"`
	PacingExpected float64        `json:"pacing_expected"`
	Projection     int            `json:"projection"`
	OnTrack        bool           `json:"on_track"`
	ByChannel      map[string]int `json:"by_channel"`
}

// AcquisitionCohort counts new clients acquired through platform channels
// inside the goal window: client created within the window, at least one
// booking, first-touch channel in the configured set. The same attribution
// spine that drives the fee drives the count — the guarantee's scoreboard
// and its invoice can never disagree.
func (svc *Services) AcquisitionCohort(ctx context.Context, tenantID string) (*CohortReport, error) {
	cfg, err := svc.acquisitionConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	t, err := svc.Tenants.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	start := t.CreatedAt
	if cfg.CohortStartedAt != "" {
		if parsed, err := time.Parse(time.RFC3339, cfg.CohortStartedAt); err == nil {
			start = parsed
		}
	}
	now := svc.Now()
	end := start.Add(time.Duration(cfg.GoalDays) * 24 * time.Hour)
	if now.Before(end) {
		end = now
	}
	elapsed := int(math.Ceil(end.Sub(start).Hours() / 24))
	if elapsed < 1 {
		elapsed = 1
	}

	// First booking per client (the acquisition moment that counts).
	clients, err := svc.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	bookings, err := svc.Store.Bookings().List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	reqClient := map[string]string{}
	firstBooking := map[string]time.Time{}
	for _, b := range bookings {
		cid, ok := reqClient[b.RequestID]
		if !ok {
			sr, err := svc.Store.Requests().Get(ctx, tenantID, b.RequestID)
			if err != nil {
				continue
			}
			cid = sr.ClientID
			reqClient[b.RequestID] = cid
		}
		if prev, ok := firstBooking[cid]; !ok || b.CreatedAt.Before(prev) {
			firstBooking[cid] = b.CreatedAt
		}
	}

	rep := &CohortReport{
		GoalClients: cfg.GoalClients, GoalDays: cfg.GoalDays,
		StartedAt: start, DaysElapsed: elapsed, ByChannel: map[string]int{},
	}
	for _, c := range clients {
		if c.CreatedAt.Before(start) || !c.CreatedAt.Before(end) {
			continue // outside the cohort window
		}
		if _, ok := firstBooking[c.ID]; !ok {
			continue // acquired but never booked — not a customer yet
		}
		ch, camp := svc.FirstTouch(ctx, tenantID, c.ID)
		acq := classifyAcquisition(ch, camp)
		eligible := false
		for _, ch2 := range cfg.Channels {
			if ch2 == acq {
				eligible = true
				break
			}
		}
		if !eligible {
			continue // direct/organic-brand traffic — not platform-attributed
		}
		rep.NewClients++
		rep.ByChannel[acq]++
	}
	rep.PacingExpected = math.Round(float64(cfg.GoalClients)*float64(elapsed)/float64(cfg.GoalDays)*10) / 10
	rep.Projection = int(math.Round(float64(rep.NewClients) * float64(cfg.GoalDays) / float64(elapsed)))
	rep.OnTrack = rep.Projection >= cfg.GoalClients
	return rep, nil
}

// ------------------------------------------------------------- pacing alerts

type AcquisitionService struct {
	s             *Services
	waURL         string
	operatorPhone string // CE_OPERATOR_PHONE fallback
}

// PacingAlertIfDue pushes the cohort scoreboard to the tenant's operator
// roster (fallback: the platform operator phone) once a week — Mondays
// 09:00 UTC, aligned with the hourly lifecycle sweeper. Advisory: no
// phones configured or adapter down means no alert, never an error.
func (svc *AcquisitionService) PacingAlertIfDue(ctx context.Context, tenantID string) {
	now := svc.s.Now()
	if now.Weekday() != time.Monday || now.Hour() != 9 {
		return
	}
	cfg, err := svc.s.acquisitionConfig(ctx, tenantID)
	if err != nil || cfg.Disabled {
		return
	}
	rep, err := svc.s.AcquisitionCohort(ctx, tenantID)
	if err != nil || (rep.NewClients == 0 && rep.DaysElapsed < 7) {
		return // nothing to report yet
	}
	status := "ON TRACK"
	if !rep.OnTrack {
		status = "BEHIND — push referrals and partner channels harder"
	}
	body := fmt.Sprintf("Acquisition pacing: %d/%d new clients, day %d/%d (projection %d) — %s.",
		rep.NewClients, rep.GoalClients, rep.DaysElapsed, rep.GoalDays, rep.Projection, status)

	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return
	}
	phones := t.OperatorConfig.AlertPhones
	if len(phones) == 0 && svc.operatorPhone != "" {
		phones = []string{svc.operatorPhone}
	}
	if len(phones) == 0 || svc.waURL == "" {
		return
	}
	for _, phone := range phones {
		// Operator alerts are service traffic to the tenant's own staff —
		// never consent-gated. Fire-and-forget by contract.
		pushWhatsAppAlert(ctx, svc.waURL, tenantID, phone, body)
	}
}
