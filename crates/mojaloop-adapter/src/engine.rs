//! Mojaloop FSPIOP adapter engine (Master Spec section 11.3, Phase 2).
//!
//! The adapter translates between the platform's payout saga and the scheme:
//! quote -> accept -> fulfil, with idempotent writes everywhere (external
//! event IDs as keys — replays and duplicates are structurally impossible to
//! double-count). The adapter never decides money state; it converts scheme
//! events into core-api payout callbacks and TigerBeetle stays the ledger of
//! record.
//!
//! Transfer states: PENDING -> FULFILLED | ABORTED | TIMED_OUT (absorbing).

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::{HashMap, HashSet};
use std::sync::Mutex;
use std::time::{Duration, Instant};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum TransferState {
    Pending,
    Fulfilled,
    Aborted,
    TimedOut,
}

#[derive(Debug, Clone, Serialize)]
pub struct Quote {
    pub quote_id: String,
    pub payee_ref: String,
    pub amount_minor: i64,
    pub currency: String,
    pub fx_rate: f64,
    pub receive_minor: i64,
    pub fees_minor: i64,
    pub expires_at: String,
}

#[derive(Debug, Clone, Serialize)]
pub struct Transfer {
    pub transfer_id: String,
    pub quote_id: String,
    pub payout_id: String,
    pub tenant_id: String,
    pub callback_url: String,
    pub state: TransferState,
    pub idempotency_key: String,
    #[serde(skip)]
    pub created: Instant,
    pub ttl: Duration,
}

#[derive(Debug)]
pub enum EngineError {
    QuoteNotFound,
    InvalidTransition,
}

pub struct Engine {
    quotes: Mutex<HashMap<String, Quote>>,
    transfers: Mutex<HashMap<String, Transfer>>,
    by_idem: Mutex<HashMap<String, String>>,
    seen_events: Mutex<HashSet<String>>,
    http: reqwest::Client,
    /// Deterministic FX table (scheme-quote stand-in). Production adapters
    /// source this from the scheme's quote response; the shape is identical.
    fx_bps: i64,
    default_ttl: Duration,
}

pub fn deterministic_id(prefix: &str, key: &str) -> String {
    let digest = Sha256::digest(key.as_bytes());
    format!("{}-{}", prefix, hex_lower(&digest[..16]))
}

fn hex_lower(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{:02x}", b)).collect()
}

/// Deterministic mock FX rate per currency (dev/scheme-sim). 1.0 for EUR.
fn fx_rate(currency: &str) -> f64 {
    match currency {
        "EUR" => 1.0,
        "USD" => 1.08,
        "GBP" => 0.86,
        "AED" => 3.97,
        "NGN" => 1720.0,
        "KES" => 139.0,
        _ => 1.0,
    }
}

impl Engine {
    pub fn new(default_ttl: Duration) -> Self {
        Self::with_fx_bps(default_ttl, 50)
    }

    pub fn with_fx_bps(default_ttl: Duration, fx_bps: i64) -> Self {
        Engine {
            quotes: Mutex::new(HashMap::new()),
            transfers: Mutex::new(HashMap::new()),
            by_idem: Mutex::new(HashMap::new()),
            seen_events: Mutex::new(HashSet::new()),
            http: reqwest::Client::new(),
            fx_bps,
            default_ttl,
        }
    }

    /// FSPIOP quote: locks FX, receive amount and scheme fees.
    pub fn quote(&self, payee_ref: &str, amount_minor: i64, currency: &str) -> Quote {
        let rate = fx_rate(currency);
        let gross = (amount_minor as f64 * rate).round() as i64;
        let fees = std::cmp::max(100, gross * self.fx_bps / 10_000);
        let receive = gross - fees;
        let key = format!(
            "quote:{}:{}:{}:{}",
            payee_ref, amount_minor, currency, receive
        );
        let q = Quote {
            quote_id: deterministic_id("q", &key),
            payee_ref: payee_ref.to_string(),
            amount_minor,
            currency: currency.to_string(),
            fx_rate: rate,
            receive_minor: receive,
            fees_minor: fees,
            expires_at: "2030-01-01T00:00:00Z".to_string(), // dev scheme-sim: generous
        };
        self.quotes
            .lock()
            .unwrap()
            .insert(q.quote_id.clone(), q.clone());
        q
    }

    /// Initiate a transfer. Idempotent on `idempotency_key`: replays return
    /// the original transfer (same deterministic ID), never a duplicate.
    pub fn transfer(
        &self,
        quote_id: &str,
        idempotency_key: &str,
        payout_id: &str,
        tenant_id: &str,
        callback_url: &str,
    ) -> Result<Transfer, EngineError> {
        if let Some(existing_id) = self.by_idem.lock().unwrap().get(idempotency_key) {
            let transfers = self.transfers.lock().unwrap();
            if let Some(t) = transfers.get(existing_id) {
                return Ok(t.clone());
            }
        }
        {
            let quotes = self.quotes.lock().unwrap();
            if !quotes.contains_key(quote_id) {
                return Err(EngineError::QuoteNotFound);
            }
        }
        let id = deterministic_id("tr", idempotency_key);
        let t = Transfer {
            transfer_id: id.clone(),
            quote_id: quote_id.to_string(),
            payout_id: payout_id.to_string(),
            tenant_id: tenant_id.to_string(),
            callback_url: callback_url.to_string(),
            state: TransferState::Pending,
            idempotency_key: idempotency_key.to_string(),
            created: Instant::now(),
            ttl: self.default_ttl,
        };
        self.transfers.lock().unwrap().insert(id.clone(), t.clone());
        self.by_idem
            .lock()
            .unwrap()
            .insert(idempotency_key.to_string(), id);
        Ok(t)
    }

    pub fn get(&self, transfer_id: &str) -> Option<Transfer> {
        self.transfers.lock().unwrap().get(transfer_id).cloned()
    }

    /// Apply a scheme callback. Idempotent on `event_id`; absorbing terminal
    /// states make replays no-ops. Returns (transfer, is_new_transition).
    pub fn apply_callback(
        &self,
        transfer_id: &str,
        event_id: &str,
        kind: &str,
    ) -> Result<(Transfer, bool), EngineError> {
        if !self
            .seen_events
            .lock()
            .unwrap()
            .insert(event_id.to_string())
        {
            if let Some(t) = self.get(transfer_id) {
                return Ok((t, false)); // duplicate event: replay-safe
            }
        }
        let mut transfers = self.transfers.lock().unwrap();
        let t = transfers
            .get_mut(transfer_id)
            .ok_or(EngineError::QuoteNotFound)?; // reuse: unknown id
        if t.state != TransferState::Pending {
            return Ok((t.clone(), false)); // absorbing
        }
        let next = match kind {
            "transfer.fulfilled" => TransferState::Fulfilled,
            "transfer.aborted" => TransferState::Aborted,
            "transfer.timeout" => TransferState::TimedOut,
            _ => return Err(EngineError::InvalidTransition),
        };
        t.state = next;
        Ok((t.clone(), true))
    }

    /// Expire PENDING transfers past TTL. Returns newly timed-out transfers.
    pub fn sweep(&self) -> Vec<Transfer> {
        let mut timed_out = Vec::new();
        let mut transfers = self.transfers.lock().unwrap();
        for t in transfers.values_mut() {
            if t.state == TransferState::Pending && t.created.elapsed() > t.ttl {
                t.state = TransferState::TimedOut;
                timed_out.push(t.clone());
            }
        }
        timed_out
    }

    /// Forward a terminal callback to core-api's payout saga. Fire-and-forget
    /// with retry left to the caller's sweep: core replays are idempotent, so
    /// re-forwarding is always safe.
    pub async fn notify_core(&self, t: &Transfer, event_id: &str, kind: &str) {
        if t.callback_url.is_empty() {
            return;
        }
        let body = serde_json::json!({
            "transfer_id": t.transfer_id,
            "event_id": event_id,
            "kind": kind,
            "payout_id": t.payout_id,
        });
        let res = self
            .http
            .post(&t.callback_url)
            .header("X-Tenant-ID", &t.tenant_id)
            .json(&body)
            .send()
            .await;
        if let Err(e) = res {
            tracing::warn!(transfer_id = %t.transfer_id, error = %e, "core callback failed; saga sweep will retry");
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn quote_is_deterministic_and_net_of_fees() {
        let e = Engine::new(Duration::from_secs(900));
        let q1 = e.quote("fsiop-safari", 10000, "EUR");
        let q2 = e.quote("fsiop-safari", 10000, "EUR");
        assert_eq!(q1.quote_id, q2.quote_id);
        assert_eq!(q1.receive_minor, 10000 - 100); // 50bps = 50, min fee 100
        assert_eq!(q1.fees_minor, 100);
    }

    #[test]
    fn transfer_idempotent_on_key() {
        let e = Engine::new(Duration::from_secs(900));
        let q = e.quote("fsiop-safari", 5000, "NGN");
        let t1 = e
            .transfer(&q.quote_id, "payout:p1", "p1", "tenant", "http://core/cb")
            .unwrap();
        let t2 = e
            .transfer(&q.quote_id, "payout:p1", "p1", "tenant", "http://core/cb")
            .unwrap();
        assert_eq!(t1.transfer_id, t2.transfer_id);
        assert!(t1.transfer_id.starts_with("tr-"));
    }

    #[test]
    fn callbacks_are_idempotent_and_absorbing() {
        let e = Engine::new(Duration::from_secs(900));
        let q = e.quote("fsiop-safari", 5000, "KES");
        let t = e
            .transfer(&q.quote_id, "payout:p2", "p2", "tenant", "http://core/cb")
            .unwrap();

        let (t, is_new) = e
            .apply_callback(&t.transfer_id, "evt-1", "transfer.fulfilled")
            .unwrap();
        assert!(is_new);
        assert_eq!(t.state, TransferState::Fulfilled);

        // Same event replayed: no-op.
        let (_, is_new) = e
            .apply_callback(&t.transfer_id, "evt-1", "transfer.fulfilled")
            .unwrap();
        assert!(!is_new);
        // Different event on terminal state: absorbing.
        let (t, is_new) = e
            .apply_callback(&t.transfer_id, "evt-2", "transfer.aborted")
            .unwrap();
        assert!(!is_new);
        assert_eq!(t.state, TransferState::Fulfilled);
    }

    #[test]
    fn sweep_times_out_stale_transfers() {
        let e = Engine::new(Duration::from_millis(0));
        let q = e.quote("fsiop-safari", 5000, "EUR");
        let t = e
            .transfer(&q.quote_id, "payout:p3", "p3", "tenant", "http://core/cb")
            .unwrap();
        std::thread::sleep(Duration::from_millis(2));
        let timed_out = e.sweep();
        assert_eq!(timed_out.len(), 1);
        assert_eq!(timed_out[0].transfer_id, t.transfer_id);
        assert_eq!(
            e.get(&t.transfer_id).unwrap().state,
            TransferState::TimedOut
        );
        // Second sweep: nothing new.
        assert!(e.sweep().is_empty());
    }
}
