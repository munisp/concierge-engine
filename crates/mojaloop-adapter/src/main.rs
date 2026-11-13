//! Mojaloop FSPIOP adapter (Phase 2). Port 8220 by default.
//!
//! Env:
//!   CE_MOJALOOP_LISTEN_ADDR  listen address (default :8220)
//!   MOJALOOP_TRANSFER_TTL_SECS  transfer timeout before auto-timeout
//!                               callback (default 900)
//!   CE_ML_SWEEP_SECS         timeout sweeper interval (default 5)

use std::net::SocketAddr;
use std::sync::Arc;
use std::time::Duration;

mod engine;
mod http_api;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();

    let ttl = std::env::var("MOJALOOP_TRANSFER_TTL_SECS")
        .ok()
        .and_then(|v| v.parse::<u64>().ok())
        .unwrap_or(900);
    let engine = Arc::new(engine::Engine::new(Duration::from_secs(ttl)));

    // Timeout sweeper: expired transfers become TIMED_OUT and the core-api
    // payout saga is compensated via callback (Spec section 11.3).
    {
        let engine = engine.clone();
        let interval = std::env::var("CE_ML_SWEEP_SECS")
            .ok()
            .and_then(|v| v.parse::<u64>().ok())
            .unwrap_or(5);
        tokio::spawn(async move {
            let mut tick = tokio::time::interval(Duration::from_secs(interval));
            loop {
                tick.tick().await;
                for t in engine.sweep() {
                    engine
                        .notify_core(
                            &t,
                            &format!("timeout:{}", t.transfer_id),
                            "transfer.timeout",
                        )
                        .await;
                }
            }
        });
    }

    let addr: SocketAddr = std::env::var("CE_MOJALOOP_LISTEN_ADDR")
        .unwrap_or_else(|_| ":8220".to_string())
        .parse()
        .expect("invalid listen address");
    tracing::info!(%addr, "mojaloop-adapter listening");
    let listener = tokio::net::TcpListener::bind(addr).await.expect("bind");
    axum::serve(listener, http_api::router(engine))
        .await
        .expect("serve");
}
