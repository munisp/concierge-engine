//! Policy engine (AI Concierge Spec §4) — deterministic, non-LLM guardrails
//! for every proposed AI action and every outbound reply. Implemented in Rust
//! for auditability and latency. The LLM never has the final word on money,
//! identity, or outbound text.

mod engine;
mod http_api;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "policy_engine=info".into()),
        )
        .init();

    let app = http_api::router(engine::PolicyEngine::default());
    let addr = std::env::var("POLICY_ENGINE_ADDR").unwrap_or_else(|_| "0.0.0.0:8200".into());
    let listener = tokio::net::TcpListener::bind(&addr).await.expect("bind");
    tracing::info!(%addr, "policy-engine listening");
    axum::serve(listener, app).await.expect("serve");
}
