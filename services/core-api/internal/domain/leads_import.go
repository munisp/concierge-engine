// Lead import (Phase 37): greenfield tenants bootstrap their pipeline from
// an existing spreadsheet or CRM export instead of starting empty.
//
// Design: import is a batch wrapper over Capture, never a parallel path —
// every row gets the same normalization, phone/email dedup, scoring,
// ResolveV2 messaging-identity link, grant-only consent mirror and
// lead_captured funnel event as a live capture. Rows are advisory: one
// invalid row never aborts the batch; it is reported with its index and
// reason so the operator can fix and re-paste. Keyed rows (IdempotencyKey
// set, e.g. "crm-export-2027-01:{row}") make a re-import replay-safe —
// replays count as deduped, not duplicated.
package domain

import (
	"context"
	"errors"
	"fmt"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// MaxImportBatch caps one import call; bigger exports are chunked client-side.
const MaxImportBatch = 500

// ImportRowError is one rejected row: where in the batch and why.
type ImportRowError struct {
	Index  int    `json:"index"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

// LeadImportResult is the import receipt the console renders.
type LeadImportResult struct {
	Total    int              `json:"total"`
	Created  int              `json:"created"`
	Deduped  int              `json:"deduped"` // merged into an open lead, or keyed replay
	Rejected int              `json:"rejected"`
	LeadIDs  []string         `json:"lead_ids"`
	Errors   []ImportRowError `json:"errors,omitempty"`
}

// Import captures a batch of leads. Rows with no Source default to "import"
// so the funnel separates bootstrapped demand from live traffic.
func (svc *LeadService) Import(ctx context.Context, tenantID string, rows []CaptureLeadInput) (*LeadImportResult, error) {
	if len(rows) == 0 {
		return nil, fmt.Errorf("%w: no leads in batch", httpx.ErrValidation)
	}
	if len(rows) > MaxImportBatch {
		return nil, fmt.Errorf("%w: batch of %d exceeds the %d-row limit", httpx.ErrValidation, len(rows), MaxImportBatch)
	}
	res := &LeadImportResult{Total: len(rows)}
	for i, in := range rows {
		if in.Source == "" {
			in.Source = "import"
		}
		r, err := svc.Capture(ctx, tenantID, in)
		switch {
		case err == nil:
			if r.Deduped {
				res.Deduped++
			} else {
				res.Created++
			}
			res.LeadIDs = append(res.LeadIDs, r.Lead.ID)
		case errors.Is(err, httpx.ErrConflict):
			// Keyed replay of a row already imported — idempotent, not an error.
			res.Deduped++
		case errors.Is(err, httpx.ErrValidation):
			res.Rejected++
			res.Errors = append(res.Errors, ImportRowError{Index: i, Name: in.Name, Reason: err.Error()})
		default:
			return nil, err // store failure: abort, the caller retries the batch
		}
	}
	svc.s.Events.Track(ctx, &model.AttributionEvent{
		TenantID: tenantID, Kind: "lead_imported", Channel: "import",
	})
	svc.s.Events.Emit(ctx, tenantID, "lead", "", "lead.imported", map[string]any{
		"created": res.Created, "deduped": res.Deduped, "rejected": res.Rejected,
	})
	return res, nil
}
