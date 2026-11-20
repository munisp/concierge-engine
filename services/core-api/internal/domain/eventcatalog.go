package domain

// Phase 15: event catalog — the integrator discovery surface. Third parties
// subscribe by event kind; this registry is the contract of what exists and
// what each payload carries. Keep entries in sync with Emit call sites.

// EventSpec documents one emittable event kind.
type EventSpec struct {
	Kind        string   `json:"kind"`
	Aggregate   string   `json:"aggregate"`
	Description string   `json:"description"`
	PayloadKeys []string `json:"payload_keys"`
}

// EventCatalog lists every event kind the platform emits, grouped by
// aggregate. Webhook subscriptions match on kind (exact, "prefix.*", "*").
var EventCatalog = []EventSpec{
	{"client.created", "client", "New client resolved on any channel (progressive registration)", []string{"source"}},
	{"client.updated", "client", "Profile fields changed", []string{"fields"}},
	{"client.consent_updated", "client", "Consent scope granted or revoked", []string{"scope", "granted"}},
	{"client.merged", "client", "Duplicate client folded into this one", []string{"merged_client_id"}},
	{"preference.saved", "client", "Consent-scoped preference recorded", []string{"key", "encrypted"}},
	{"dsar.preferences_deleted", "client", "Preference memory deleted (DSAR cascade)", nil},
	{"dsar.exported", "client", "Data-subject access export assembled (GDPR Art. 15 / CCPA)", nil},
	{"client.erased", "client", "Right to erasure executed — identity scrubbed to pseudonymous tombstone", nil},
	{"referral.reward_accrued", "client", "Reward credit accrued on a referral conversion — referrer reward or referee welcome credit (Phase 18/19)", []string{"amount_minor", "kind", "source_booking_id"}},
	{"referral.reward_redeemed", "client", "Referral credit applied to a captured payment", []string{"intent_id", "amount_minor"}},
	{"promo.created", "promo", "Promo campaign created (Phase 20)", []string{"code", "credit_minor", "currency"}},
	{"promo.redeemed", "client", "Client redeemed a promo code for reward credit", []string{"code", "amount_minor", "currency"}},

	{"conversation.started", "conversation", "Inbound message opened a conversation", []string{"channel", "client_id"}},
	{"message.appended", "conversation", "Message added to a conversation timeline", []string{"author"}},

	{"request.created", "service_request", "Service request captured", []string{"state", "items"}},
	{"request.amended", "service_request", "Request amended (versioned)", []string{"version"}},
	{"request.state_changed", "service_request", "Request state machine transition", []string{"from", "to"}},
	{"quote.created", "quote", "Quote issued (instant or human)", []string{"total_minor", "currency", "origin", "tax_minor", "tax_engine"}},
	{"quote.human_required", "service_request", "Request exceeds instant-quote rules — human quoting with SLA", []string{"sla_seconds"}},
	{"booking.confirmed", "booking", "Quote converted to booking", []string{"quote_id", "total_minor"}},

	{"payment.link_created", "payment", "Hosted payment link minted", []string{"quote_id", "amount_minor", "mode", "psp"}},
	{"payment.captured", "payment", "PSP confirmed capture; ledger splits posted", []string{"amount_minor", "psp_ref"}},
	{"payment.failed", "payment", "PSP reported failure", nil},
	{"payment.refund_initiated", "payment", "Refund initiated at the PSP (async confirm)", []string{"amount_minor", "psp"}},
	{"payment.refunded", "payment", "Refund confirmed; ledger reversal posted", nil},
	{"ledger.posting_pending", "ledger", "Ledger unreachable — posting queued for reconciliation", []string{"debit", "credit", "amount_minor", "idempotency_key"}},

	{"dispatch.assignment_created", "assignment", "Resource assigned to a booking", []string{"booking_id", "fleet_resource_id"}},
	{"chauffeur.assigned", "assignment", "Chauffeur magic link issued", []string{"booking_id"}},
	// Phase 56: partner integration events. partner_id in the payload scopes
	// delivery to the partner's own webhook subscriptions.
	{"assignment.partner_offered", "assignment", "Partner-routed assignment offered", []string{"booking_id", "partner_id"}},
	{"assignment.partner_responded", "assignment", "Partner accepted or declined an assignment", []string{"partner_id", "booking_id", "action"}},
	{"partner.commission_accrued", "payout", "Partner commission accrued as pending payout", []string{"partner_id", "booking_id", "amount_minor", "currency"}},
	{"channel.request_sourced", "service_request", "Partner sourced a guest request (book-on-behalf)", []string{"partner_id", "source_kind", "client_id", "new_client"}},
	{"channel.sourcing_commission_accrued", "payout", "Sourcing commission accrued to the demand channel", []string{"partner_id", "booking_id", "source_kind", "amount_minor", "currency", "pct"}},
	{"channel.source_attributed", "service_request", "Operator reattributed demand to a channel partner", []string{"from_kind", "from_partner", "to_kind", "to_partner"}},
	{"channel.rate_card_set", "partner", "Partner rate card upserted (negotiated channel pricing)", []string{"partner_id", "vertical", "kind", "value", "currency"}},
	{"channel.rate_card_applied", "service_request", "Partner rate card adjusted a sourced quote", []string{"partner_id", "vertical", "kind", "value", "adjustment_minor", "currency"}},
	{"channel.rate_card_skipped", "service_request", "Partner rate card skipped at quoting (reason in payload)", []string{"partner_id", "vertical", "reason"}},
	{"channel.bill_to_desk_set", "partner", "Partner bill-to-desk toggled", []string{"partner_id", "enabled"}},
	{"channel.folio_line_accrued", "booking", "Sourced booking accrued onto the partner folio", []string{"partner_id", "period", "currency", "amount_minor", "folio_created"}},
	{"channel.folio_accrual_blocked", "booking", "Folio accrual blocked — period already issued (bill manually)", []string{"partner_id", "period", "currency", "reason"}},
	{"channel.folio_issued", "partner", "Partner folio issued for payment", []string{"folio_id", "period", "currency", "total_minor", "lines"}},
	{"channel.folio_settled", "partner", "Partner folio settled — ledger posted", []string{"folio_id", "period", "currency", "total_minor", "adjusted_total_minor", "net_minor", "fee_minor"}},
	{"channel.folio_reminder_sent", "partner", "Folio payment reminder delivered to the partner", []string{"folio_id", "n", "channel", "status", "period", "currency", "total_minor", "overdue_days"}},
	{"channel.folio_reminder_skipped", "partner", "Folio reminder not deliverable — reason recorded verbatim on the folio", []string{"folio_id", "n", "channel", "status", "period", "currency", "total_minor", "overdue_days"}},
	{"channel.folio_payment_recorded", "partner", "Partial payment recorded against an issued folio — proportional net/fee split posted", []string{"folio_id", "payment_id", "amount_minor", "method", "currency", "outstanding_minor", "settled"}},
	{"channel.folio_statement_sent", "partner", "Statement document delivered to the partner over their best channel", []string{"folio_id", "delivery_id", "channel", "status", "period", "currency", "outstanding_minor"}},
	{"channel.folio_statement_skipped", "partner", "Statement delivery not deliverable — reason recorded verbatim on the folio", []string{"folio_id", "delivery_id", "channel", "status", "period", "currency", "outstanding_minor"}},
	{"channel.folio_credit_recorded", "partner", "Credit note (issued) or refund (settled) recorded against a folio — refunds post reversal ledger entries", []string{"folio_id", "credit_id", "kind", "amount_minor", "reason", "currency", "outstanding_minor", "settled"}},
	{"partner.commission_cancelled", "payout", "Pending commission voided on partner decline", []string{"partner_id", "booking_id", "amount_minor", "currency"}},
	{"trip.status_changed", "trip", "Chauffeur trip advanced", []string{"status"}},

	{"case.opened", "case", "Case opened (intake or manual)", []string{"kind", "priority", "engine"}},
	{"case.assigned", "case", "Case owner changed", []string{"owner"}},
	{"case.linked", "case", "Case linked to request/booking/conversation", nil},
	{"case.reply_sent", "case", "Operator-approved reply sent to the client", []string{"sent", "engine", "external_ref"}},
	{"case.reply_failed", "case", "Delivery failed on a linked reply — SLA evidence", []string{"external_ref", "state"}},
	{"case.reply_read", "case", "Client read a linked reply — proof of delivery", []string{"external_ref"}},
	{"case.sla_breach", "case", "Case breached its response SLA", []string{"priority"}},
	{"case.intake_skipped", "case", "Intake deduped (idempotency key replay)", nil},

	{"outreach.sent", "outreach", "Proactive outreach recorded (sent/held/failed in payload)", []string{"client_id", "purpose", "status", "external_ref"}},
	{"channel.receipt", "channel", "Delivery/read receipt from the channel adapter", []string{"channel", "external_ref", "status", "at"}},

	{"referral.offered", "referral", "Cross-tenant referral offered", nil},
	{"referral.incoming", "referral", "Referral received from a partner tenant", nil},
	{"referral.accepted", "referral", "Receiving tenant accepted", nil},
	{"referral.declined", "referral", "Receiving tenant declined", []string{"reason"}},
	{"referral.settled", "referral", "Referral commission settled", []string{"commission_minor"}},
	{"referral.commission_earned", "referral", "Referring tenant's commission posted", []string{"commission_minor"}},
	{"referral.converted", "referral", "Client referral code produced a first booking", []string{"referee_client_id", "booking_id", "value_minor", "code", "referrer_client_id"}},

	{"tenant.created", "tenant", "Tenant onboarded", []string{"slug"}},
	{"tenant.config_updated", "tenant", "Tenant configuration changed", nil},
	{"tenant.autonomy_changed", "tenant", "Autonomy dial moved", []string{"level"}},
	{"tenant.instant_on", "tenant", "Instant-on provisioning completed", nil},

	{"partner.registered", "partner", "Supply partner registered", []string{"name"}},
	{"journey.leg_added", "itinerary", "Itinerary leg recorded", []string{"kind", "client_id", "source"}},
	{"journey.flight_updated", "itinerary", "Flight status refreshed", []string{"flight", "status", "delay_minutes", "source"}},
	{"journey.ground_adjusted", "itinerary", "Ground pickup re-timed after flight update", []string{"booking_id", "flight", "old_pickup", "new_pickup", "delay_minutes"}},
	{"journey.rider_notified", "itinerary", "Rider notified of journey change", []string{"channel", "status", "detail"}},
	{"chauffeur.link_set", "dispatch", "Rider favorite/blocked mark recorded", []string{"crew_member_id", "kind"}},
	{"partner.status_changed", "partner", "Partner vetting status changed", []string{"status"}},
	{"payout.created", "payout", "Partner payout created", []string{"amount_minor", "rail"}},
	{"payout.quoted", "payout", "Mojaloop quote received", nil},
	{"payout.in_flight", "payout", "Payout transfer executing", nil},
	{"payout.settled", "payout", "Payout settled", nil},
	{"payout.quote_failed", "payout", "Mojaloop quote failed — saga compensating", nil},
	{"payout.transfer_failed", "payout", "Mojaloop transfer failed — saga compensating", nil},
	{"payout.compensated", "payout", "Payout saga compensated", nil},

	{"corporate.account_created", "corporate", "Corporate account opened", []string{"name"}},
	{"corporate.invoice_issued", "corporate", "Consolidated invoice issued", []string{"total_minor"}},
	{"corporate.invoice_paid", "corporate", "Invoice marked paid", nil},

	{"handoff.opened", "handoff", "AI→human handoff opened", []string{"reason"}},
	{"handoff.resolved", "handoff", "Handoff resolved", nil},

	{"geo.area_created", "geo", "Service area defined", []string{"name"}},
	{"supply.flights_searched", "supply", "GDS flight search executed", nil},
	{"supply.hotels_searched", "supply", "GDS hotel search executed", nil},
	{"supply.charter_searched", "supply", "Charter supply search executed", nil},
	{"supply.attached", "booking", "GDS supply attached to a booking", nil},

	{"eval.rejection_sampled", "eval", "AI output sampled for quality review", nil},
	{"webhook.test", "webhook", "Synthetic test delivery for endpoint verification", []string{"subscription_id"}},
	{"webhook.rotated", "webhook", "Subscription signing secret rotated", []string{"subscription_id"}},
}

// ------------------------------------------------------------ Phase 21:
// schema versioning. The catalog is the integrator contract; webhook
// envelopes stamp "schema_version" so receivers pin parsing to a version
// instead of guessing payload shape from the kind alone. All kinds are v1
// today — a payload-breaking change registers the new version here (and
// keeps emitting the old shape for pinned receivers until they migrate).
var eventVersions = map[string]int{
	// "kind": 2 — example of a future bump; empty map = everything is v1.
}

// CatalogVersion reports the payload schema version for an event kind.
// Kinds without an explicit entry are v1 (and webhook.test is v1).
func CatalogVersion(kind string) int {
	if v, ok := eventVersions[kind]; ok {
		return v
	}
	return 1
}

// EventSpecJSON is the catalog wire shape: the spec plus its schema version.
type EventSpecJSON struct {
	EventSpec
	Version int `json:"version"`
}

// CatalogJSON renders the full catalog with per-kind schema versions.
func CatalogJSON() []EventSpecJSON {
	out := make([]EventSpecJSON, 0, len(EventCatalog))
	for _, e := range EventCatalog {
		out = append(out, EventSpecJSON{e, CatalogVersion(e.Kind)})
	}
	return out
}
