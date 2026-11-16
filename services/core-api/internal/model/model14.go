package model

// Phase 48: settlement batches — netting for the Mojaloop payout lane.
//
// A batch collects one partner's PENDING payouts (same currency) into ONE
// scheme transfer: the partner's dozen completed trips cost one scheme fee
// instead of twelve. The batch carries its own saga state machine,
// deliberately identical in shape to the payout saga; the member payouts
// move PENDING → IN_FLIGHT → SETTLED/COMPENSATED in lockstep with it.

import "time"

// Settlement batch states (mirror the payout saga).
const (
	BatchOpen        = "OPEN"
	BatchQuoted      = "QUOTED"
	BatchInFlight    = "IN_FLIGHT"
	BatchSettled     = "SETTLED"
	BatchCompensated = "COMPENSATED"
	BatchFailed      = "FAILED"
)

// SettlementBatch is one netted partner settlement. AmountMinor is the sum
// of member payout amounts; ReceiveMinor/FeesMinor come from the scheme
// quote on the NETTED amount — the fee saving is the point.
type SettlementBatch struct {
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	PartnerID    string `json:"partner_id"`
	Currency     string `json:"currency"`
	AmountMinor  int64  `json:"amount_minor"`
	ReceiveMinor int64  `json:"receive_minor,omitempty"`
	FeesMinor    int64  `json:"fees_minor,omitempty"`
	State        string `json:"state"`
	QuoteID      string `json:"quote_id,omitempty"`
	TransferID   string `json:"transfer_id,omitempty"`
	// QuoteExpiresAt enforces the scheme's FX lock, same rule as single
	// payouts: executing against an expired quote is a 409.
	QuoteExpiresAt time.Time `json:"quote_expires_at,omitempty"`
	IdempotencyKey string    `json:"-"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// BatchTerminal reports whether the batch is in an absorbing state.
func (b SettlementBatch) BatchTerminal() bool {
	return b.State == BatchSettled || b.State == BatchCompensated || b.State == BatchFailed
}
