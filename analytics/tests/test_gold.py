"""Gold-layer tests: LTV, churn risk, pricing suggestions — all
deterministic heuristics with traceable rules (Phase 3 ML v0)."""

from __future__ import annotations

import json
from datetime import date
from pathlib import Path

from analytics.lakehouse import gold
from analytics.lakehouse.silver import write_silver


def _seed_bronze(root: Path, events: list[dict]) -> None:
    d = root / "bronze" / "core"
    d.mkdir(parents=True, exist_ok=True)
    (d / "events.jsonl").write_text(
        "\n".join(json.dumps(e) for e in events) + "\n"
    )


def _ev(eid: str, tenant: str, etype: str, ts: str, **payload) -> dict:
    return {
        "id": eid, "tenant_id": tenant, "aggregate": "x", "aggregate_id": eid,
        "event_type": etype, "payload": payload, "created_at": ts,
    }


def test_client_ltv(tmp_path: Path):
    _seed_bronze(tmp_path, [
        _ev("1", "t1", "booking.confirmed", "2026-10-01T10:00:00Z", client_id="c1"),
        _ev("2", "t1", "payment.captured", "2026-10-02T10:00:00Z", client_id="c1", amount_minor=100000),
        _ev("3", "t1", "booking.confirmed", "2026-10-05T10:00:00Z", client_id="c1"),
        _ev("4", "t1", "payment.captured", "2026-10-06T10:00:00Z", client_id="c1", amount_minor=120000),
        _ev("5", "t1", "booking.confirmed", "2026-11-01T10:00:00Z", client_id="c2"),
    ])
    out = gold.build_client_ltv(tmp_path)
    rows = {r["client_id"]: r for r in out["rows"]}
    c1 = rows["c1"]
    assert c1["revenue_minor"] == 220000
    assert c1["bookings"] == 2
    assert c1["avg_booking_minor"] == 110000
    # tenant repeat probability: 1 of 2 clients has >1 booking -> 0.5
    assert c1["repeat_probability"] == 0.5
    assert c1["ltv_minor"] == 220000 + int(110000 * 0.5 * 4)
    assert rows["c2"]["revenue_minor"] == 0


def test_churn_risk(tmp_path: Path):
    _seed_bronze(tmp_path, [
        _ev("1", "t1", "booking.confirmed", "2026-09-01T10:00:00Z", client_id="c1"),
        _ev("2", "t1", "booking.confirmed", "2026-09-11T10:00:00Z", client_id="c1"),
        _ev("3", "t1", "booking.confirmed", "2026-09-21T10:00:00Z", client_id="c1"),
    ])
    # client books every 10 days; today is 40 days after the last -> high risk
    out = gold.build_churn_risk(tmp_path, today=date(2026, 10, 31))
    (row,) = out["rows"]
    assert row["typical_gap_days"] == 10
    assert row["days_since_last_booking"] == 40
    assert row["churn_risk"] == 1.0 and row["tier"] == "high"
    # one week after the last booking -> low risk
    out2 = gold.build_churn_risk(tmp_path, today=date(2026, 9, 28))
    (row2,) = out2["rows"]
    assert row2["tier"] == "low" and row2["churn_risk"] == 0.35


def test_pricing_suggestions(tmp_path: Path):
    events = []
    # tenant t1: 9 instant + 1 human quotes (instant share .9), 8 partner of
    # 10 assignments -> raise_price
    for i in range(9):
        events.append(_ev(f"q{i}", "t1", "quote.created", f"2026-10-{i + 1:02d}T10:00:00Z", origin="INSTANT"))
    events.append(_ev("qh", "t1", "quote.created", "2026-10-10T10:00:00Z", origin="HUMAN"))
    for i in range(8):
        events.append(_ev(f"p{i}", "t1", "chauffeur.assigned", f"2026-10-{i + 11}T10:00:00Z", partner_id=f"pt{i}"))
    for i in range(2):
        events.append(_ev(f"f{i}", "t1", "chauffeur.assigned", f"2026-10-{i + 21}T10:00:00Z"))
    _seed_bronze(tmp_path, events)
    write_silver(tmp_path)

    out = gold.build_pricing_suggestions(tmp_path)
    (row,) = out["rows"]
    assert row["tenant_id"] == "t1"
    assert row["action"] == "raise_price" and row["pct"] == 5


def test_pricing_suggestions_empty(tmp_path: Path):
    assert gold.build_pricing_suggestions(tmp_path)["rows"] == []
    paths = gold.write_gold(tmp_path)
    assert {p.name for p in paths} == {
        "client_ltv.json", "churn_risk.json", "pricing_suggestions.json",
    }
