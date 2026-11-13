//! Deterministic TIER classification and verdicts (AI spec §4.1).
//!
//! TIER-0 read-only            -> always allowed
//! TIER-1 reversible           -> auto-executed
//! TIER-2 money-adjacent       -> auto within limits, sampled for review
//! TIER-3 consequential        -> human-only; agent prepares, never executes
//!
//! Value- and VIP-based routing shifts actions up a tier. Rate/budget
//! anomalies degrade the tenant to human-only mode.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize)]
pub enum Tier {
    Tier0 = 0,
    Tier1 = 1,
    Tier2 = 2,
    Tier3 = 3,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Verdict {
    Allow,
    AllowSampled,
    RequireApproval,
    Deny,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ActionRequest {
    pub tenant_id: String,
    #[serde(default)]
    pub conversation_id: String,
    pub actor: String,
    pub tool: String,
    #[serde(default)]
    pub args_json: String,
    #[serde(default)]
    pub request_state: String,
    #[serde(default)]
    pub estimated_value_minor: i64,
    #[serde(default)]
    pub currency: String,
    #[serde(default)]
    pub vip_flagged: bool,
    #[serde(default)]
    pub idempotency_key: String,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PolicyVerdict {
    pub verdict_id: String,
    pub tier: u8,
    pub verdict: Verdict,
    pub reasons: Vec<String>,
    pub evaluated_by: String,
    pub evaluated_at: i64,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct TenantPolicy {
    /// Auto-approve ceiling for TIER-2 actions (minor units).
    pub tier2_limit_minor: i64,
    /// Above this estimated value, any money-adjacent action escalates.
    pub value_escalation_minor: i64,
    /// Per-tenant autonomy dial: 0 = draft-only .. 3 = full auto.
    pub autonomy_dial: u8,
}

impl Default for TenantPolicyDefaults {
    fn default() -> Self {
        Self
    }
}

/// Marker type documenting the default policy values (overridable per tenant).
pub struct TenantPolicyDefaults;

impl TenantPolicyDefaults {
    pub const TIER2_LIMIT_MINOR: i64 = 50_000;
    pub const VALUE_ESCALATION_MINOR: i64 = 500_000; // €5,000 default
    pub const TIER2_SAMPLE_RATE_DENOM: u32 = 10; // sample 1/10 of TIER-2 allows
}

pub struct PolicyEngine {
    version: String,
}

impl Default for PolicyEngine {
    fn default() -> Self {
        Self {
            version: env!("CARGO_PKG_VERSION").to_string(),
        }
    }
}

impl PolicyEngine {
    /// Classify the tool call into a deterministic tier.
    pub fn classify(tool: &str) -> Tier {
        match tool {
            // TIER-0: read-only
            "resolve_client" | "get_preferences" | "search_availability" => Tier::Tier0,
            // TIER-1: reversible drafts
            "create_service_request" | "update_service_request" | "save_preference"
            | "draft_itinerary" => Tier::Tier1,
            // TIER-2: money-adjacent
            "price_quote" | "create_payment_link" | "send_message" | "assign_booking" => {
                Tier::Tier2
            }
            // TIER-3: consequential — refunds, waivers, charge execution,
            // discounts above limit, partner SLA penalties
            "execute_charge" | "refund" | "waive_fee" | "apply_discount" | "apply_sla_penalty"
            | "delete_data" => Tier::Tier3,
            // Escalation itself is always allowed.
            "escalate_to_human" => Tier::Tier0,
            // Unknown tools: fail-closed.
            _ => Tier::Tier3,
        }
    }

    /// Least privilege per conversation state (AI spec §3.2): the state
    /// machine gates the tool set.
    pub fn state_permits(state: &str, tool: &str) -> bool {
        match tool {
            "create_payment_link" => {
                matches!(state, "QUOTING" | "NEGOTIATING" | "AWAITING_PAYMENT")
            }
            "assign_booking" => matches!(state, "CONFIRMED" | "IN_SERVICE" | "ESCALATED"),
            _ => true,
        }
    }

    pub fn evaluate(&self, req: &ActionRequest, policy: &TenantPolicy) -> PolicyVerdict {
        let mut reasons: Vec<String> = Vec::new();
        let mut tier = Self::classify(&req.tool);

        // State gating.
        if !Self::state_permits(&req.request_state, &req.tool) {
            reasons.push(format!(
                "tool {} not permitted in state {}",
                req.tool, req.request_state
            ));
            return self.verdict(tier, Verdict::Deny, reasons);
        }

        // VIP flags shift defaults toward human ownership.
        if req.vip_flagged && tier < Tier::Tier3 && tier > Tier::Tier0 {
            tier = Tier::try_up(tier).unwrap_or(tier);
            reasons.push("VIP-flagged profile: shifted up one tier".into());
        }

        // Value-based routing: estimated value above threshold escalates.
        if tier == Tier::Tier2 && req.estimated_value_minor > policy.value_escalation_minor {
            reasons.push(format!(
                "estimated value {} exceeds escalation threshold {}",
                req.estimated_value_minor, policy.value_escalation_minor
            ));
            return self.verdict(tier, Verdict::RequireApproval, reasons);
        }

        let verdict = match tier {
            Tier::Tier0 => Verdict::Allow,
            Tier::Tier1 => Verdict::Allow,
            Tier::Tier2 => {
                if policy.autonomy_dial == 0 {
                    reasons.push("autonomy dial 0: draft-only".into());
                    Verdict::RequireApproval
                } else if req.estimated_value_minor > policy.tier2_limit_minor {
                    reasons.push(format!(
                        "value {} exceeds TIER-2 limit {}",
                        req.estimated_value_minor, policy.tier2_limit_minor
                    ));
                    Verdict::RequireApproval
                } else {
                    Verdict::AllowSampled
                }
            }
            Tier::Tier3 => {
                reasons.push("consequential action: human-only".into());
                Verdict::RequireApproval
            }
        };
        self.verdict(tier, verdict, reasons)
    }

    fn verdict(&self, tier: Tier, verdict: Verdict, reasons: Vec<String>) -> PolicyVerdict {
        PolicyVerdict {
            verdict_id: uuid_v4(),
            tier: tier as u8,
            verdict,
            reasons,
            evaluated_by: format!("policy-engine@{}", self.version),
            evaluated_at: now_epoch(),
        }
    }
}

impl Tier {
    fn try_up(self) -> Option<Tier> {
        match self {
            Tier::Tier0 => Some(Tier::Tier1),
            Tier::Tier1 => Some(Tier::Tier2),
            Tier::Tier2 => Some(Tier::Tier3),
            Tier::Tier3 => None,
        }
    }
}

fn uuid_v4() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_nanos())
        .unwrap_or(0);
    format!("{:032x}", nanos ^ (std::process::id() as u128) << 64)
}

fn now_epoch() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

#[cfg(test)]
mod tests {
    use super::*;

    fn req(tool: &str) -> ActionRequest {
        ActionRequest {
            tenant_id: "t".into(),
            conversation_id: "c".into(),
            actor: "ai".into(),
            tool: tool.into(),
            args_json: String::new(),
            request_state: "QUOTING".into(),
            estimated_value_minor: 0,
            currency: "EUR".into(),
            vip_flagged: false,
            idempotency_key: "k".into(),
        }
    }

    fn policy() -> TenantPolicy {
        TenantPolicy {
            tier2_limit_minor: 50_000,
            value_escalation_minor: 500_000,
            autonomy_dial: 2,
        }
    }

    #[test]
    fn tier_classification() {
        assert_eq!(PolicyEngine::classify("get_preferences"), Tier::Tier0);
        assert_eq!(PolicyEngine::classify("save_preference"), Tier::Tier1);
        assert_eq!(PolicyEngine::classify("create_payment_link"), Tier::Tier2);
        assert_eq!(PolicyEngine::classify("refund"), Tier::Tier3);
        assert_eq!(PolicyEngine::classify("unknown_tool"), Tier::Tier3); // fail-closed
    }

    #[test]
    fn tier3_always_human() {
        let e = PolicyEngine::default();
        let v = e.evaluate(&req("refund"), &policy());
        assert_eq!(v.verdict, Verdict::RequireApproval);
    }

    #[test]
    fn payment_link_state_gated() {
        let e = PolicyEngine::default();
        let mut r = req("create_payment_link");
        r.request_state = "NEW".into(); // not permitted outside quoting states
        let v = e.evaluate(&r, &policy());
        assert_eq!(v.verdict, Verdict::Deny);
    }

    #[test]
    fn value_routing_escalates() {
        let e = PolicyEngine::default();
        let mut r = req("price_quote");
        r.estimated_value_minor = 600_000;
        let v = e.evaluate(&r, &policy());
        assert_eq!(v.verdict, Verdict::RequireApproval);
    }

    #[test]
    fn vip_shifts_tier() {
        let e = PolicyEngine::default();
        let mut r = req("create_payment_link");
        r.vip_flagged = true;
        let v = e.evaluate(&r, &policy());
        assert_eq!(v.tier, Tier::Tier3 as u8);
        assert_eq!(v.verdict, Verdict::RequireApproval);
    }

    #[test]
    fn autonomy_dial_zero_draft_only() {
        let e = PolicyEngine::default();
        let mut p = policy();
        p.autonomy_dial = 0;
        let v = e.evaluate(&req("price_quote"), &p);
        assert_eq!(v.verdict, Verdict::RequireApproval);
    }
}
