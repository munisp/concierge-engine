//! Dev journal: an in-memory double-entry ledger with the same idempotency
//! contract as TigerBeetle. Used for local dev and tests ONLY — production
//! money truth is TigerBeetle (Master Spec §11).

use axum::async_trait;
use std::collections::HashMap;
use std::sync::Mutex;

use crate::ledger::{deterministic_id, Ledger, LedgerCode, LedgerError, Posting, PostingResult};

pub struct DevJournal {
    /// idempotency key -> transfer id
    seen: Mutex<HashMap<String, String>>,
    /// (tenant, code, currency) -> balance in minor units
    balances: Mutex<HashMap<(String, u16, String), i64>>,
}

impl DevJournal {
    pub fn new() -> Self {
        Self {
            seen: Mutex::new(HashMap::new()),
            balances: Mutex::new(HashMap::new()),
        }
    }
}

impl Default for DevJournal {
    fn default() -> Self {
        Self::new()
    }
}

#[async_trait]
impl Ledger for DevJournal {
    async fn post(&self, p: &Posting) -> Result<PostingResult, LedgerError> {
        if p.amount_minor <= 0 {
            return Err(LedgerError::Invalid("amount must be positive".into()));
        }
        if p.debit_code == p.credit_code {
            return Err(LedgerError::Invalid("debit and credit must differ".into()));
        }
        let transfer_id = deterministic_id(&p.idempotency_key);
        {
            let seen = self
                .seen
                .lock()
                .map_err(|e| LedgerError::Backend(e.to_string()))?;
            if seen.contains_key(&p.idempotency_key) {
                return Ok(PostingResult {
                    transfer_id,
                    duplicate: true,
                });
            }
        }
        let mut seen = self
            .seen
            .lock()
            .map_err(|e| LedgerError::Backend(e.to_string()))?;
        seen.insert(p.idempotency_key.clone(), transfer_id.clone());
        let mut balances = self
            .balances
            .lock()
            .map_err(|e| LedgerError::Backend(e.to_string()))?;
        let debit_key = (p.tenant_id.clone(), p.debit_code.code(), p.currency.clone());
        let credit_key = (
            p.tenant_id.clone(),
            p.credit_code.code(),
            p.currency.clone(),
        );
        *balances.entry(debit_key).or_insert(0) -= p.amount_minor;
        *balances.entry(credit_key).or_insert(0) += p.amount_minor;
        Ok(PostingResult {
            transfer_id,
            duplicate: false,
        })
    }

    async fn balance(
        &self,
        tenant_id: &str,
        code: LedgerCode,
        currency: &str,
    ) -> Result<i64, LedgerError> {
        let balances = self
            .balances
            .lock()
            .map_err(|e| LedgerError::Backend(e.to_string()))?;
        Ok(*balances
            .get(&(tenant_id.to_string(), code.code(), currency.to_string()))
            .unwrap_or(&0))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn posting(key: &str) -> Posting {
        Posting {
            debit_code: LedgerCode::ClientClearing,
            credit_code: LedgerCode::TenantRevenue,
            amount_minor: 9700,
            currency: "EUR".into(),
            tenant_id: "taylor".into(),
            booking_id: "b1".into(),
            idempotency_key: key.into(),
        }
    }

    #[tokio::test]
    async fn idempotent_postings_never_double_count() {
        let j = DevJournal::new();
        let r1 = j.post(&posting("k1")).await.unwrap();
        let r2 = j.post(&posting("k1")).await.unwrap();
        assert_eq!(r1.transfer_id, r2.transfer_id);
        assert!(!r1.duplicate);
        assert!(r2.duplicate);
        assert_eq!(
            j.balance("taylor", LedgerCode::TenantRevenue, "EUR")
                .await
                .unwrap(),
            9700
        );
        assert_eq!(
            j.balance("taylor", LedgerCode::ClientClearing, "EUR")
                .await
                .unwrap(),
            -9700
        );
    }

    #[test]
    fn deterministic_ids_are_stable() {
        assert_eq!(deterministic_id("abc"), deterministic_id("abc"));
        assert_ne!(deterministic_id("abc"), deterministic_id("abd"));
    }
}
