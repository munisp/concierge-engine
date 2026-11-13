"""Temporal activities for the payout saga (Master Spec §11.3).

Each activity is a thin, idempotent call into core-api. Idempotency lives in
the domain layer (unique keys, absorbing terminal states), so Temporal retries
are always safe to replay.
"""

from __future__ import annotations

import os

import httpx
from temporalio import activity

CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")


class CoreAPIError(RuntimeError):
    """Non-retryable core-api failure (4xx domain rejection)."""


async def _call(method: str, path: str, tenant_id: str, payload: dict | None = None) -> dict:
    async with httpx.AsyncClient(base_url=CORE_API_URL, timeout=15.0,
                                 headers={"X-Tenant-ID": tenant_id}) as client:
        r = await client.request(method, path, json=payload)
        if 400 <= r.status_code < 500:
            # Domain rejections (validation, conflict) are not transient —
            # retrying cannot help; surface as non-retryable.
            raise CoreAPIError(f"{method} {path}: {r.status_code} {r.text[:300]}")
        r.raise_for_status()
        return r.json() if r.content else {}


@activity.defn
async def quote_payout(tenant_id: str, payout_id: str) -> dict:
    """FX quote via the Mojaloop rail (or manual-rail passthrough)."""
    activity.logger.info("quote_payout %s", payout_id)
    return await _call("POST", f"/v1/payouts/{payout_id}/quote", tenant_id)


@activity.defn
async def execute_payout(tenant_id: str, payout_id: str) -> dict:
    """Reserve funds (PARTNER_PAYABLE -> IN_FLIGHT_SETTLEMENT) and dispatch the
    scheme transfer."""
    activity.logger.info("execute_payout %s", payout_id)
    return await _call("POST", f"/v1/payouts/{payout_id}/execute", tenant_id)


@activity.defn
async def compensate_payout(tenant_id: str, payout_id: str, reason: str) -> dict:
    """Reverse the reservation and open the HITL ops page (§11.3 abort)."""
    activity.logger.info("compensate_payout %s (%s)", payout_id, reason)
    return await _call("POST", f"/v1/payouts/{payout_id}/compensate", tenant_id,
                       {"reason": reason})


@activity.defn
async def get_payout(tenant_id: str, payout_id: str) -> dict:
    return await _call("GET", f"/v1/payouts/{payout_id}", tenant_id)
