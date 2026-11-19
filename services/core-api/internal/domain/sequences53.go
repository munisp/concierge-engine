// Phase 53: visual campaign builder — the domain half (Mautic feature
// borrow A1; see docs/phase53.md for the assessment and the honesty
// rules). The builder edits the SAME Phase 35 drip engine — steps gain
// typed-node fields (template/channel/subject), a decision node
// (RequireEngagement + timeout), and a live per-step progress read —
// all DERIVED from the outreach_sends ledger, still zero extra state:
//
//   - Gate waiting writes nothing: a stalled client simply re-evaluates
//     on the next hourly sweep. Nothing is sent, nothing is recorded.
//   - Gate timeout writes a terminal halt marker ON THE LEDGER (status
//     held, Detail prefixed seqHaltPrefix). Downstream steps see the
//     marker and stop — a halt is visible, never a silent drop.
//   - Progress counts group the ledger by step index; "waiting" counts
//     clients whose furthest record sits exactly one step behind.
//
// CRUD mutates Tenant.OutreachConfig.Sequences through UpdateConfig —
// the same persistence path as PUT /v1/tenant, no migration.
package domain

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

// seqHaltPrefix marks a ledger record as a terminal engagement-gate halt.
// Progression checks the prefix, so the marker IS the halt state.
const seqHaltPrefix = "sequence halted: "

const (
	seqMaxSteps      = 24
	seqMaxAfterHours = 8760 // one year
	seqMaxNameLength = 80
	seqMaxTimeoutHrs = 8760
)

func isSeqHalt(o *model.OutreachSend) bool {
	return o != nil && o.Status == "held" && strings.HasPrefix(o.Detail, seqHaltPrefix)
}

// seqStepChannel defaults a step's channel the way the outreach engine
// does — whatsapp unless the step declares email.
func seqStepChannel(step model.SequenceStep) string {
	if step.Channel == model.OutreachChannelEmail {
		return model.OutreachChannelEmail
	}
	return model.OutreachChannelWhatsApp
}

// engagedSince reports whether the attribution spine holds a real
// engagement event (opens/clicks/replies — the Phase 52 evidence kinds)
// for this client at or after the anchor time. Sends themselves never
// count (the Phase 52 denominator-honesty rule applies here too).
func (svc *SequenceService) engagedSince(ctx context.Context, tenantID, clientID string, since time.Time) (bool, error) {
	events, err := svc.s.Store.Attribution().List(ctx, tenantID, clientID, 1000)
	if err != nil {
		return false, err
	}
	for _, e := range events {
		if !stoEngagementKinds[e.Kind] {
			continue
		}
		// At is stamped by the store at write time when zero, so a zero
		// value here means "no timestamp" — treat it as old, never as
		// fresh engagement.
		if e.At.IsZero() {
			continue
		}
		if !e.At.Before(since) {
			return true, nil
		}
	}
	return false, nil
}

// ValidateSequence enforces the builder's contract. The rules exist so a
// sequence saved from the canvas can never wedge the hourly sweep:
// email steps carry a subject, every step carries content (body or
// template), and step 0 can never gate on a previous step that does not
// exist.
func ValidateSequence(seq model.Sequence) error {
	seq.Name = strings.TrimSpace(seq.Name)
	if seq.Name == "" || len(seq.Name) > seqMaxNameLength || strings.ContainsAny(seq.Name, ":#") {
		return fmt.Errorf("%w: sequence name required (no ':' or '#')", httpx.ErrValidation)
	}
	if seq.Purpose != "" && seq.Purpose != model.OutreachMarketing && seq.Purpose != model.OutreachService {
		return fmt.Errorf("%w: purpose must be marketing or service", httpx.ErrValidation)
	}
	if len(seq.Steps) == 0 || len(seq.Steps) > seqMaxSteps {
		return fmt.Errorf("%w: 1..%d steps required", httpx.ErrValidation, seqMaxSteps)
	}
	for i, st := range seq.Steps {
		if st.AfterHours < 0 || st.AfterHours > seqMaxAfterHours {
			return fmt.Errorf("%w: step %d after_hours out of range", httpx.ErrValidation, i)
		}
		if st.Body == "" && st.TemplateID == "" {
			return fmt.Errorf("%w: step %d needs a body or a template", httpx.ErrValidation, i)
		}
		switch st.Channel {
		case "", model.OutreachChannelWhatsApp:
		case model.OutreachChannelEmail:
			if strings.TrimSpace(st.Subject) == "" {
				return fmt.Errorf("%w: step %d is email and needs a subject", httpx.ErrValidation, i)
			}
		default:
			return fmt.Errorf("%w: step %d channel must be whatsapp or email", httpx.ErrValidation, i)
		}
		if st.RequireEngagement && i == 0 {
			return fmt.Errorf("%w: step 0 has no previous step to gate on", httpx.ErrValidation)
		}
		if st.EngagementTimeoutHours < 0 || st.EngagementTimeoutHours > seqMaxTimeoutHrs {
			return fmt.Errorf("%w: step %d engagement timeout out of range", httpx.ErrValidation, i)
		}
	}
	return nil
}

// UpsertSequence creates or replaces the named sequence (keyed by name —
// the console save button is naturally idempotent). Validation runs
// BEFORE the tenant row is touched.
func (svc *SequenceService) UpsertSequence(ctx context.Context, tenantID string, seq model.Sequence) error {
	if err := ValidateSequence(seq); err != nil {
		return err
	}
	seq.Name = strings.TrimSpace(seq.Name)
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return err
	}
	seqs := t.OutreachConfig.Sequences
	replaced := false
	for i := range seqs {
		if seqs[i].Name == seq.Name {
			seqs[i] = seq
			replaced = true
			break
		}
	}
	if !replaced {
		seqs = append(seqs, seq)
	}
	t.OutreachConfig.Sequences = seqs
	_, err = svc.s.Tenants.UpdateConfig(ctx, t)
	return err
}

// DeleteSequence removes the named sequence from the config. Ledger
// records keep their seq:{name}:... keys — history is never rewritten,
// and re-creating the same name RESUMES the cadence (documented in the
// builder UI, not hidden).
func (svc *SequenceService) DeleteSequence(ctx context.Context, tenantID, name string) error {
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return err
	}
	seqs := t.OutreachConfig.Sequences
	out := seqs[:0]
	found := false
	for _, s := range seqs {
		if s.Name == name {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return httpx.ErrNotFound
	}
	t.OutreachConfig.Sequences = out
	_, err = svc.s.Tenants.UpdateConfig(ctx, t)
	return err
}

// ------------------------------------------------------------- progress

// SequenceStepStats is one node's live counters on the builder canvas.
type SequenceStepStats struct {
	Index     int    `json:"index"`
	Label     string `json:"label,omitempty"`
	Sent      int    `json:"sent"`
	Scheduled int    `json:"scheduled"`
	Held      int    `json:"held"`
	Failed    int    `json:"failed"`
	Halted    int    `json:"halted"`
	// Waiting: clients whose furthest ledger record is the previous step
	// (and that step did not halt) — the audience this node fires on next.
	Waiting int `json:"waiting"`
}

// SequenceProgress pairs the definition with its derived live state.
type SequenceProgress struct {
	Sequence model.Sequence      `json:"sequence"`
	Enrolled int                 `json:"enrolled"`
	Steps    []SequenceStepStats `json:"steps"`
}

// Progress derives every sequence's per-node counters from the ledger.
// The numbers the canvas shows are the same numbers the sweep acts on —
// no parallel counters that could drift.
func (svc *SequenceService) Progress(ctx context.Context, tenantID string) ([]SequenceProgress, error) {
	t, err := svc.s.Tenants.Get(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if len(t.OutreachConfig.Sequences) == 0 {
		return []SequenceProgress{}, nil
	}
	clients, err := svc.s.Store.Clients().List(ctx, tenantID, 5000)
	if err != nil {
		return nil, err
	}
	sends, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 20000)
	if err != nil {
		return nil, err
	}
	// furthest[keyless client][step] — per sequence prefix.
	out := make([]SequenceProgress, 0, len(t.OutreachConfig.Sequences))
	for _, seq := range t.OutreachConfig.Sequences {
		if seq.Name == "" || len(seq.Steps) == 0 {
			continue
		}
		sp := SequenceProgress{Sequence: seq, Steps: make([]SequenceStepStats, len(seq.Steps))}
		for i, st := range seq.Steps {
			sp.Steps[i].Index = i
			sp.Steps[i].Label = st.Label
		}
		prefix := "seq:" + seq.Name + ":"
		// per-client furthest step + per-step tallies
		type furthest struct {
			step   int
			halted bool
		}
		far := map[string]furthest{}
		for _, o := range sends {
			if !strings.HasPrefix(o.IdempotencyKey, prefix) {
				continue
			}
			rest := strings.TrimPrefix(o.IdempotencyKey, prefix)
			iStr, _, ok := strings.Cut(rest, ":")
			if !ok {
				continue
			}
			idx, err := strconv.Atoi(iStr)
			if err != nil || idx < 0 || idx >= len(seq.Steps) {
				continue
			}
			st := &sp.Steps[idx]
			switch o.Status {
			case "sent":
				st.Sent++
			case "scheduled":
				st.Scheduled++
			case "held":
				st.Held++
			case "failed":
				st.Failed++
			}
			halt := isSeqHalt(&o)
			if halt {
				st.Halted++
			}
			f, ok := far[o.ClientID]
			if !ok || idx > f.step {
				far[o.ClientID] = furthest{step: idx, halted: halt}
			} else if idx == f.step && halt {
				f.halted = true
				far[o.ClientID] = f
			}
		}
		for _, f := range far {
			next := f.step + 1
			if !f.halted && next < len(seq.Steps) {
				sp.Steps[next].Waiting++
			}
		}
		// Enrolled: non-erased clients matching the segment (nil = all).
		if seq.Segment != nil {
			matched, err := svc.s.SegmentClients(ctx, tenantID, *seq.Segment)
			if err != nil {
				return nil, err
			}
			sp.Enrolled = len(matched)
		} else {
			for _, c := range clients {
				if !c.Erased() {
					sp.Enrolled++
				}
			}
		}
		out = append(out, sp)
	}
	return out, nil
}
