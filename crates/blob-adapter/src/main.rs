//! blob-adapter binary: OpenDAL operator from env, HTTP surface from app.

mod app;

use std::collections::HashMap;

#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env().unwrap_or_else(|_| "info".into()),
        )
        .init();

    let scheme = std::env::var("CE_BLOB_SCHEME").unwrap_or_else(|_| "fs".to_string());

    // Every OPENDAL_* var is an OpenDAL service config key, lowercased:
    // OPENDAL_ROOT=/data → root=/data, OPENDAL_BUCKET=x → bucket=x, etc.
    let mut map: HashMap<String, String> = std::env::vars()
        .filter(|(k, _)| k.starts_with("OPENDAL_"))
        .map(|(k, v)| (k["OPENDAL_".len()..].to_lowercase(), v))
        .collect();
    if let Ok(root) = std::env::var("CE_BLOB_ROOT") {
        map.entry("root".to_string()).or_insert(root);
    }
    if scheme == "fs" {
        map.entry("root".to_string())
            .or_insert_with(|| "/var/lib/ce-blobs".to_string());
    }

    let op = match app::build_operator(&scheme, map) {
        Ok(op) => op,
        Err(e) => {
            eprintln!("blob-adapter: {e}");
            std::process::exit(1);
        }
    };

    let listen = std::env::var("CE_BLOB_LISTEN").unwrap_or_else(|_| "0.0.0.0:8230".to_string());
    let listener = tokio::net::TcpListener::bind(&listen)
        .await
        .unwrap_or_else(|e| panic!("bind {listen}: {e}"));
    tracing::info!(%listen, %scheme, "blob-adapter listening (engine: apache-opendal)");
    axum::serve(listener, app::app(op, &scheme))
        .await
        .unwrap_or_else(|e| panic!("serve: {e}"));
}
