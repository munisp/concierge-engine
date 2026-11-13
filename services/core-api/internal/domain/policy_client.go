// PolicyClient calls the Rust policy engine. The LLM never has the final word:
// every action proposed by the AI concierge — and every consequential staff
// action — passes through here (AI Concierge Spec §4.1).
package domain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type PolicyVerdict struct {
	VerdictID string   `json:"verdict_id"`
	Tier      int      `json:"tier"`    // 0..3
	Verdict   string   `json:"verdict"` // ALLOW | ALLOW_SAMPLED | REQUIRE_APPROVAL | DENY
	Reasons   []string `json:"reasons"`
}

type PolicyClient struct {
	base string
	hc   *http.Client
}

func NewPolicyClient(base string) *PolicyClient {
	return &PolicyClient{base: base, hc: &http.Client{Timeout: 3 * time.Second}}
}

type actionRequest struct {
	TenantID            string `json:"tenant_id"`
	ConversationID      string `json:"conversation_id,omitempty"`
	Actor               string `json:"actor"`
	Tool                string `json:"tool"`
	ArgsJSON            string `json:"args_json,omitempty"`
	RequestState        string `json:"request_state,omitempty"`
	EstimatedValueMinor int64  `json:"estimated_value_minor,omitempty"`
	Currency            string `json:"currency,omitempty"`
	VIPFlagged          bool   `json:"vip_flagged"`
	IdempotencyKey      string `json:"idempotency_key,omitempty"`
}

// Evaluate asks the policy engine for a verdict. Fail-closed for anything
// above TIER-1: if the policy engine is unreachable, consequential actions
// REQUIRE_APPROVAL rather than pass silently.
func (c *PolicyClient) Evaluate(ctx context.Context, tenantID, conversationID, actor, tool, argsJSON, state string, estValue int64, currency string, vip bool, idem string) (*PolicyVerdict, error) {
	body, _ := json.Marshal(actionRequest{
		TenantID: tenantID, ConversationID: conversationID, Actor: actor, Tool: tool,
		ArgsJSON: argsJSON, RequestState: state, EstimatedValueMinor: estValue,
		Currency: currency, VIPFlagged: vip, IdempotencyKey: idem,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/evaluate", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("policy engine unreachable (fail-closed): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("policy engine: status %d", resp.StatusCode)
	}
	var v PolicyVerdict
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		return nil, err
	}
	return &v, nil
}

// FallbackVerdict is used when the engine is unreachable: safe-by-default.
func FallbackVerdict(tool string) *PolicyVerdict {
	return &PolicyVerdict{VerdictID: "fallback", Tier: 3, Verdict: "REQUIRE_APPROVAL",
		Reasons: []string{"policy engine unreachable — defaulting to human approval"}}
}
