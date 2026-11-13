//! TigerBeetle ledger adapter (Master Spec §11): the money-truth boundary.
//! Double-entry, deterministic balances, idempotent postings — retries never
//! double-post. The adapter speaks HTTP/JSON to core-api and the TigerBeetle
//! wire protocol (or the dev journal) to the ledger of record.
//!
//! Accounts (per tenant, per currency): CLIENT_CLEARING, TENANT_REVENUE,
//! PLATFORM_FEES, PARTNER_PAYABLE, DEPOSITS_LIABILITY, IN_FLIGHT_SETTLEMENT.

mod journal;
mod ledger;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "ledger_adapter=info".into()),
        )
        .init();

    let backend: Box<dyn ledger::Ledger> = match std::env::var("TB_ADDRESSES") {
        Ok(addrs) => {
            let cluster: u128 = std::env::var("TB_CLUSTER")
                .ok()
                .and_then(|c| c.parse().ok())
                .unwrap_or(0);
            match ledger::TigerBeetleLedger::connect(cluster, &addrs).await {
                Ok(tb) => {
                    tracing::info!(%addrs, cluster, "connected to TigerBeetle");
                    Box::new(tb)
                }
                Err(e) => {
                    tracing::warn!(error = %e, "TigerBeetle unreachable; falling back to dev journal (NOT for production)");
                    Box::new(journal::DevJournal::new())
                }
            }
        }
        Err(_) => {
            tracing::warn!("TB_ADDRESSES unset; dev journal mode (NOT for production)");
            Box::new(journal::DevJournal::new())
        }
    };

    let backend: std::sync::Arc<dyn ledger::Ledger> = std::sync::Arc::from(backend);
    let app = ledger::router(backend);
    let addr = std::env::var("LEDGER_ADAPTER_ADDR").unwrap_or_else(|_| "0.0.0.0:8210".into());
    let listener = tokio::net::TcpListener::bind(&addr).await.expect("bind");
    tracing::info!(%addr, "ledger-adapter listening");
    axum::serve(listener, app).await.expect("serve");
}
