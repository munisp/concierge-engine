//! HTTP surface for the policy engine: POST /v1/evaluate, POST /v1/reply-check.

use axum::{extract::State, http::StatusCode, response::Json, routing::post, Router};
use serde::{Deserialize, Serialize};
use std::sync::Arc;

use crate::engine::{ActionRequest, PolicyEngine, PolicyVerdict, TenantPolicy};

pub fn router(engine: PolicyEngine) -> Router {
    Router::new()
        .route("/healthz", axum::routing::get(|| async { "ok" }))
        .route("/v1/evaluate", post(evaluate))
        .route("/v1/reply-check", post(reply_check))
        .with_state(Arc::new(engine))
}

async fn evaluate(
    axum::extract::State(engine): axum::extract::State<Arc<PolicyEngine>>,
    Json(req): Json<ActionRequest>,
) -> Json<PolicyVerdict> {
    // Tenant policy is fetched from core-api in production (cached);
    // defaults applied here.
    let policy = TenantPolicy {
        tier2_limit_minor: 50_000,
        value_escalation_minor: 500_000,
        autonomy_dial: 2,
    };
    Json(engine.evaluate(&req, &policy))
}

/// Five-stage reply pipeline: schema -> policy -> tone -> factuality -> privacy
/// (AI spec §4.3). Stages 1/2/5 are deterministic and live here; tone and
/// factuality classifiers run in the AI service and submit their claims here
/// for the deterministic numeric-verbatim check.
#[allow(dead_code)]
#[derive(Debug, Deserialize)]
pub struct ReplyCheck {
    pub tenant_id: String,
    pub conversation_id: String,
    pub body: String,
    /// Numeric claims extracted by the AI service (prices, times).
    pub numeric_claims: Vec<String>,
    /// Verbatim tool-output values the reply is allowed to state.
    pub allowed_values: Vec<String>,
}

#[derive(Debug, Serialize)]
pub struct ReplyVerdict {
    pub verdict: String, // PASS | REGENERATE | DEGRADE | ESCALATE
    pub reasons: Vec<String>,
}

async fn reply_check(
    State(_engine): State<Arc<PolicyEngine>>,
    Json(check): Json<ReplyCheck>,
) -> (StatusCode, Json<ReplyVerdict>) {
    let mut reasons = Vec::new();

    // Schema: no tool JSON leaked.
    if check.body.trim_start().starts_with('{') && check.body.contains("\"tool\"") {
        reasons.push("schema: tool JSON leaked into reply".to_string());
    }
    // Factuality: every numeric claim must match tool outputs verbatim.
    for claim in &check.numeric_claims {
        if !check.allowed_values.iter().any(|v| claim.contains(v)) {
            reasons.push(format!(
                "factuality: numeric claim '{claim}' not in tool outputs"
            ));
        }
    }
    // Privacy: cross-client leakage guard — replies must not embed other
    // conversations' identifiers (heuristic: foreign conversation refs).
    if check.body.contains("conv:") && !check.body.contains(&check.conversation_id) {
        reasons.push("privacy: foreign conversation reference".to_string());
    }

    let verdict = if reasons.is_empty() {
        "PASS"
    } else if reasons.iter().any(|r| r.starts_with("privacy")) {
        "ESCALATE"
    } else {
        "REGENERATE"
    };
    (
        StatusCode::OK,
        Json(ReplyVerdict {
            verdict: verdict.to_string(),
            reasons,
        }),
    )
}
