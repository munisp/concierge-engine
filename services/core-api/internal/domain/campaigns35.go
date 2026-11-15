// Broadcast campaigns (Phase 35: O1 broadcasts, O4 template A/B, O15
// campaign funnel). A broadcast fans one message out to a segment (O2)
// through the outreach engine — every send keeps its own consent gate,
// frequency cap, quiet-hours deferral and idempotency key
// (bcast:{campaign}:{client}[#variant]), so re-running a campaign is safe
// and the funnel (O15) is computed from the same ledger, never from a
// parallel counter that could drift.
package domain

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"

	"concierge-engine/services/core-api/internal/httpx"
	"concierge-engine/services/core-api/internal/model"
)

type CampaignService struct {
	s *Services
}

type BroadcastInput struct {
	Campaign   string             `json:"campaign"` // required — groups sends + funnel
	Purpose    string             `json:"purpose"`  // service | marketing (default marketing)
	Body       string             `json:"body,omitempty"`
	TemplateID string             `json:"template_id,omitempty"`
	Segment    model.SegmentRules `json:"segment"`
	// Channel (Phase 39): whatsapp (default) | email. Email broadcasts
	// require a subject; the per-client sends land on the email lane with
	// its own consent/cap gates (and the b2b-introduction exception for
	// Phase 38 list-sourced leads). The idempotency key gains a channel
	// suffix for email so the same campaign can run on both channels
	// without one replay-swallowing the other.
	Channel string `json:"channel,omitempty"`
	Subject string `json:"subject,omitempty"`
	// ABTest: when the named template has a "name#b" variant in the
	// template store, the audience splits deterministically by client id
	// (FNV hash & 1) — A receives "name", B receives "name#b". The variant
	// rides in the idempotency key suffix, so the funnel compares arms
	// from the ledger and a re-run never flips a client's arm.
	ABTest bool `json:"ab_test,omitempty"`
}

type BroadcastResult struct {
	Campaign  string `json:"campaign"`
	Matched   int    `json:"matched"`
	Sent      int    `json:"sent"`
	Scheduled int    `json:"scheduled"`
	Held      int    `json:"held"`
	Failed    int    `json:"failed"`
	VariantA  int    `json:"variant_a,omitempty"`
	VariantB  int    `json:"variant_b,omitempty"`
}

// Broadcast fans the message out to the segment. Advisory per client: one
// failure never aborts the run, and the per-client send record carries the
// outcome.
func (svc *CampaignService) Broadcast(ctx context.Context, tenantID string, in BroadcastInput) (*BroadcastResult, error) {
	in.Campaign = strings.TrimSpace(in.Campaign)
	if in.Campaign == "" || len(in.Campaign) > 80 || strings.ContainsAny(in.Campaign, ":#") {
		return nil, fmt.Errorf("%w: campaign name required (no ':' or '#')", httpx.ErrValidation)
	}
	if in.Purpose == "" {
		in.Purpose = model.OutreachMarketing
	}
	if in.Body == "" && in.TemplateID == "" {
		return nil, httpx.ErrValidation
	}
	clients, err := svc.s.SegmentClients(ctx, tenantID, in.Segment)
	if err != nil {
		return nil, err
	}
	res := &BroadcastResult{Campaign: in.Campaign, Matched: len(clients)}

	// O4: resolve the B arm once — a missing variant silently degrades the
	// experiment to a single-arm send (recorded as all-A).
	var bTemplate string
	if in.ABTest && in.TemplateID != "" {
		if base, err := svc.s.Store.Templates().Get(ctx, tenantID, in.TemplateID); err == nil {
			if _, err := svc.s.Store.Templates().GetByName(ctx, tenantID, base.Name+"#b"); err == nil {
				bTemplate = base.Name + "#b"
			}
		}
	}

	for _, c := range clients {
		tplID := in.TemplateID
		variant := ""
		if bTemplate != "" {
			if abVariant(c.ID) == 1 {
				tplID = "" // resolve by name below
				variant = "b"
				res.VariantB++
			} else {
				variant = "a"
				res.VariantA++
			}
		}
		if variant == "b" {
			if bt, err := svc.s.Store.Templates().GetByName(ctx, tenantID, bTemplate); err == nil {
				tplID = bt.ID
			}
		}
		key := fmt.Sprintf("bcast:%s:%s", in.Campaign, c.ID)
		if in.Channel != "" && in.Channel != model.OutreachChannelWhatsApp {
			key += ":" + in.Channel
		}
		if variant != "" {
			key += "#" + variant
		}
		o, err := svc.s.Outreach.Send(ctx, tenantID, SendOutreachInput{
			ClientID: c.ID, Purpose: in.Purpose, Body: in.Body,
			TemplateID: tplID, IdempotencyKey: key,
			Channel: in.Channel, Subject: in.Subject,
		})
		if err != nil {
			res.Failed++
			continue
		}
		switch o.Status {
		case "sent":
			res.Sent++
		case "scheduled":
			res.Scheduled++
		case "held":
			res.Held++
		default:
			res.Failed++
		}
	}
	return res, nil
}

// abVariant deterministically assigns a client to arm 0 (A) or 1 (B).
func abVariant(clientID string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(clientID))
	return h.Sum32() & 1
}

// CampaignFunnel (O15): the campaign's send outcomes and downstream
// conversion, computed live from the sends ledger + attribution spine.
// Variant cells split bcast keys on the "#a"/"#b" suffix. Converted counts
// clients the attribution spine recorded a booking event for under this
// campaign name (broadcast CTAs carry utm_campaign={campaign}).
type CampaignFunnel struct {
	Campaign  string         `json:"campaign"`
	Sent      int            `json:"sent"`
	Scheduled int            `json:"scheduled"`
	Held      int            `json:"held"`
	Failed    int            `json:"failed"`
	Converted int            `json:"converted"`
	Variants  map[string]int `json:"variants,omitempty"` // "a"/"b" -> sent count
	// Phase 40 economics: what the campaign cost vs. what the attribution
	// spine earned under its name. Ratios are withheld (zero) when spend
	// mixes currencies — mixed_currency flags that honestly.
	SpendMinor             int64   `json:"spend_minor"`
	AttributedValueMinor   int64   `json:"attributed_value_minor"`
	CostPerConversionMinor int64   `json:"cost_per_conversion_minor,omitempty"`
	ROAS                   float64 `json:"roas,omitempty"`
	Currency               string  `json:"currency,omitempty"`
	MixedCurrency          bool    `json:"mixed_currency,omitempty"`
}

func (svc *CampaignService) Funnel(ctx context.Context, tenantID, campaign string) (*CampaignFunnel, error) {
	if campaign == "" {
		return nil, httpx.ErrValidation
	}
	sends, err := svc.s.Store.Outreach().ListSends(ctx, tenantID, 20000)
	if err != nil {
		return nil, err
	}
	prefix := "bcast:" + campaign + ":"
	f := &CampaignFunnel{Campaign: campaign}
	for _, o := range sends {
		if !strings.HasPrefix(o.IdempotencyKey, prefix) {
			continue
		}
		switch o.Status {
		case "sent":
			f.Sent++
		case "scheduled":
			f.Scheduled++
		case "held":
			f.Held++
		case "failed":
			f.Failed++
		}
		if i := strings.LastIndexByte(o.IdempotencyKey, '#'); i >= 0 {
			v := o.IdempotencyKey[i+1:]
			if v == "a" || v == "b" {
				// Variant assignment counts regardless of delivery outcome —
				// the split is an audience property, not a delivery property.
				if f.Variants == nil {
					f.Variants = map[string]int{}
				}
				f.Variants[v]++
			}
		}
	}
	// Downstream conversion from the attribution spine: booking events
	// tagged with this campaign.
	rows, err := svc.s.Funnel(ctx, tenantID)
	if err == nil {
		for _, r := range rows {
			if r.Campaign == campaign && r.Kind == "booking" {
				f.Converted += r.Count
			}
		}
	}
	// Phase 40: spend, attributed value, cost-per-conversion, ROAS.
	svc.economics(ctx, tenantID, campaign, f)
	return f, nil
}
