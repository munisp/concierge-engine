"""Silver layer (Master Spec section 12): cleaned, queryable aggregates from
bronze. Batch in Phase 2 (stdlib); Spark takes over at scale per the
deferred-register trigger (>=3 tenants or >=50k trips).

Produces lakehouse/silver/daily_kpis.json:
    per (day, tenant): booked GTV, payments captured, quotes instant vs human,
    assignments partner-filled vs fleet, payout outcomes.
"""

from __future__ import annotations

import json
from collections import defaultdict
from pathlib import Path
from typing import Any


def _load_bronze(root: Path) -> list[dict[str, Any]]:
    events: list[dict[str, Any]] = []
    bronze = root / "bronze"
    if not bronze.exists():
        return events
    seen: set[Any] = set()
    for f in sorted(bronze.rglob("*.jsonl")):
        for line in f.read_text().splitlines():
            if not line.strip():
                continue
            e = json.loads(line)
            eid = e.get("id")
            if eid is not None:
                if eid in seen:
                    continue  # at-least-once landing: dedupe on event id
                seen.add(eid)
            events.append(e)
    return events


def _day(e: dict[str, Any]) -> str:
    return str(e.get("created_at", "unknown"))[:10]


def build_daily_kpis(root: Path) -> dict[str, Any]:
    events = _load_bronze(root)
    cells: dict[str, dict[str, Any]] = defaultdict(lambda: {
        "bookings_confirmed": 0, "gtv_minor": 0,
        "payments_captured": 0, "captured_minor": 0,
        "quotes_instant": 0, "quotes_human": 0,
        "assignments_partner": 0, "assignments_fleet": 0,
        "payouts_settled": 0, "payouts_compensated": 0,
    })
    for e in events:
        key = f"{_day(e)}|{e.get('tenant_id', 'platform')}"
        c = cells[key]
        et = e.get("event_type", "")
        payload = e.get("payload") or {}
        if et == "booking.confirmed":
            c["bookings_confirmed"] += 1
        elif et == "payment.captured":
            c["payments_captured"] += 1
            c["captured_minor"] = c.get("captured_minor", 0) + int(payload.get("amount_minor", 0))
        elif et == "quote.created":
            if payload.get("origin") == "HUMAN":
                c["quotes_human"] += 1
            else:
                c["quotes_instant"] += 1
        elif et == "chauffeur.assigned":
            if payload.get("partner_id"):
                c["assignments_partner"] += 1
            else:
                c["assignments_fleet"] += 1
        elif et == "payout.settled":
            c["payouts_settled"] += 1
        elif et == "payout.compensated":
            c["payouts_compensated"] += 1

    rows = []
    for key, c in sorted(cells.items()):
        day, tenant = key.split("|", 1)
        total_assign = c["assignments_partner"] + c["assignments_fleet"]
        rows.append({
            "day": day, "tenant_id": tenant, **c,
            "partner_fill_rate": (c["assignments_partner"] / total_assign) if total_assign else None,
        })
    return {"grain": "day|tenant", "rows": rows}


def write_silver(root: Path) -> Path:
    out = root / "silver" / "daily_kpis.json"
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(build_daily_kpis(root), indent=2))
    return out
