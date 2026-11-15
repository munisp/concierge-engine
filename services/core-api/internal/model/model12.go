package model

import "time"

// Phase 44: consent evidence ledger (GDPR Art. 7 demonstrability).

// ConsentEvent is one evidence row: a consent scope actually changed
// (appeared or flipped) on a client, at a time, via a surface. Append-only
// — no updates, no deletes. Rows survive client erasure (pseudonymous
// client_id linkage only, like the financial ledger).
type ConsentEvent struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	ClientID  string    `json:"client_id"`
	Scope     string    `json:"scope"`   // marketing | service | data_processing | ...
	Granted   bool      `json:"granted"` // the NEW value
	Source    string    `json:"source"`  // ConsentSource* — the surface that changed it
	Detail    string    `json:"detail,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Consent sources — the surface an evidence row attributes the change to.
const (
	ConsentSourceRegistrationDefault = "registration_default" // ResolveV2 jurisdiction defaults at creation
	ConsentSourceConsole             = "console"              // operator toggle / API without a narrower source
	ConsentSourceKeyword             = "keyword"              // STOP/START texted by the client
	ConsentSourceUnsub               = "unsub"                // unsub/ HMAC link
	ConsentSourceReopt               = "reopt"                // reopt/ HMAC link
	ConsentSourceLeadCapture         = "lead_capture"         // consent granted on first capture
	ConsentSourceLeadRepeatTouch     = "lead_repeat_touch"    // consent earned on a repeat touch
	ConsentSourceESPComplaint        = "esp_complaint"        // provider complaint webhook
	ConsentSourceDSARErasure         = "dsar_erasure"         // revoked by the erasure cascade
	ConsentSourceSelfService         = "self_service"         // client flipped it on the consent/ center page
)

// ConsentSourceValid reports whether s is a known evidence source. The
// write API validates against this set so the ledger stays queryable.
func ConsentSourceValid(s string) bool {
	switch s {
	case ConsentSourceRegistrationDefault, ConsentSourceConsole,
		ConsentSourceKeyword, ConsentSourceUnsub, ConsentSourceReopt,
		ConsentSourceLeadCapture, ConsentSourceLeadRepeatTouch,
		ConsentSourceESPComplaint, ConsentSourceDSARErasure,
		ConsentSourceSelfService:
		return true
	}
	return false
}
