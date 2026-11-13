//! HTTP surface for the Mojaloop FSPIOP adapter. FSPIOP-shaped subset:
//! quotes, transfers, and scheme callback ingress; dev endpoints to simulate
//! fulfilment so the payout saga is exercisable end-to-end without a scheme.

use axum::{
    extract::{Path, State},
    http::StatusCode,
    response::Json,
    routing::{get, post},
    Router,
};
use serde::Deserialize;
use std::sync::Arc;

use crate::engine::{Engine, EngineError};

pub fn router(engine: Arc<Engine>) -> Router {
    Router::new()
        .route("/healthz", get(|| async { "ok" }))
        .route("/v1/quotes", post(create_quote))
        .route("/v1/transfers", post(create_transfer))
        .route("/v1/transfers/{id}", get(get_transfer))
        .route("/v1/callbacks", post(scheme_callback))
        .with_state(engine)
}

#[derive(Debug, Deserialize)]
struct QuoteIn {
    payee_ref: String,
    amount_minor: i64,
    currency: String,
}

async fn create_quote(
    State(engine): State<Arc<Engine>>,
    Json(input): Json<QuoteIn>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    if input.amount_minor <= 0 {
        return Err((StatusCode::UNPROCESSABLE_ENTITY, "amount must be positive".into()));
    }
    let q = engine.quote(&input.payee_ref, input.amount_minor, &input.currency);
    Ok(Json(serde_json::to_value(q).map_err(err500)?))
}

#[derive(Debug, Deserialize)]
struct TransferIn {
    quote_id: String,
    idempotency_key: String,
    payout_id: String,
    tenant_id: String,
    callback_url: String,
}

async fn create_transfer(
    State(engine): State<Arc<Engine>>,
    Json(input): Json<TransferIn>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let t = engine
        .transfer(
            &input.quote_id,
            &input.idempotency_key,
            &input.payout_id,
            &input.tenant_id,
            &input.callback_url,
        )
        .map_err(|e| match e {
            EngineError::QuoteNotFound => (StatusCode::NOT_FOUND, "quote not found".into()),
            _ => (StatusCode::UNPROCESSABLE_ENTITY, "invalid transfer".into()),
        })?;
    Ok(Json(serde_json::json!({
        "transfer_id": t.transfer_id,
        "state": t.state,
    })))
}

async fn get_transfer(
    State(engine): State<Arc<Engine>>,
    Path(id): Path<String>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let t = engine
        .get(&id)
        .ok_or((StatusCode::NOT_FOUND, "transfer not found".to_string()))?;
    Ok(Json(serde_json::json!({
        "transfer_id": t.transfer_id,
        "quote_id": t.quote_id,
        "payout_id": t.payout_id,
        "state": t.state,
    })))
}

#[derive(Debug, Deserialize)]
struct CallbackIn {
    transfer_id: String,
    event_id: String,
    kind: String, // transfer.fulfilled | transfer.aborted | transfer.timeout
}

/// Scheme callback ingress (and dev fulfilment simulator). Every callback is
/// idempotent on event_id; new terminal transitions are forwarded to the
/// core-api payout saga.
async fn scheme_callback(
    State(engine): State<Arc<Engine>>,
    Json(input): Json<CallbackIn>,
) -> Result<Json<serde_json::Value>, (StatusCode, String)> {
    let (t, is_new) = engine
        .apply_callback(&input.transfer_id, &input.event_id, &input.kind)
        .map_err(|e| match e {
            EngineError::QuoteNotFound => (StatusCode::NOT_FOUND, "transfer not found".into()),
            EngineError::InvalidTransition => {
                (StatusCode::UNPROCESSABLE_ENTITY, "unknown callback kind".into())
            }
            _ => (StatusCode::INTERNAL_SERVER_ERROR, "engine error".into()),
        })?;
    if is_new {
        let engine = engine.clone();
        let event_id = input.event_id.clone();
        let kind = input.kind.clone();
        tokio::spawn(async move { engine.notify_core(&t, &event_id, &kind).await });
    }
    Ok(Json(serde_json::json!({
        "transfer_id": t.transfer_id,
        "state": t.state,
        "forwarded": is_new,
    })))
}

fn err500(e: serde_json::Error) -> (StatusCode, String) {
    (StatusCode::INTERNAL_SERVER_ERROR, e.to_string())
}
