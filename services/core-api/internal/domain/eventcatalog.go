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
	{"referral.reward_accrued", "client", "Referrer credited on referee's first booking (Phase 18)", []string{"amount_minor", "source_booking_id", "referee_client_id"}},
	{"referral.reward_redeemed", "client", "Referral credit applied to a captured payment", []string{"intent_id", "amount_minor"}},

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
