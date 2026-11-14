"""Gold-layer tests: LTV (smoothed retention curve), churn risk (empirical
hazard with rule fallback), pricing (SPC with rule fallback) — deterministic,
model-labeled (Phase 5 ML v1)."""

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
    # thin data (< 5 clients platform-wide) -> flat 0.25 prior curve:
    # expected future bookings for a client at 2 bookings =
    # .25 + .25^2 + .25^3 + .25^4 = 0.332031
    assert c1["expected_future_bookings"] == round(0.25 + 0.25**2 + 0.25**3 + 0.25**4, 3)
    assert c1["ltv_minor"] == 220000 + int(110000 * (0.25 + 0.25**2 + 0.25**3 + 0.25**4))
    assert c1["model"] == "prior:flat-0.25"
    assert rows["c2"]["revenue_minor"] == 0


def test_client_ltv_retention_curve_rewards_loyalty(tmp_path: Path):
    """With enough clients the tenant curve is fitted: in a tenant where
    the first repeat is the hard gate (many one-timers) but veterans keep
    booking, a client at booking 4 must outscore a one-timer."""
    events = []
    # 5 veterans with 6 bookings each -> r_2..r_4 high
    for c in ("a", "b", "c", "d", "e"):
        for i in range(6):
            events.append(_ev(f"{c}{i}", "t1", "booking.confirmed",
                              f"2026-08-{i + 1:02d}T10:00:00Z", client_id=c))
    # 10 one-timers -> r_1 low (first repeat is the hard gate)
    for i in range(10):
        events.append(_ev(f"o{i}", "t1", "booking.confirmed", "2026-09-01T10:00:00Z",
                          client_id=f"o{i}"))
    # c_new at 4 bookings, c_once at 1, same avg booking value
    for i in range(4):
        events.append(_ev(f"n{i}", "t1", "booking.confirmed",
                          f"2026-10-{i + 1:02d}T10:00:00Z", client_id="c_new"))
        events.append(_ev(f"pn{i}", "t1", "payment.captured",
                          f"2026-10-{i + 1:02d}T11:00:00Z", client_id="c_new", amount_minor=100000))
    events.append(_ev("g0", "t1", "booking.confirmed", "2026-10-01T10:00:00Z", client_id="c_once"))
    events.append(_ev("pg0", "t1", "payment.captured", "2026-10-01T11:00:00Z",
                      client_id="c_once", amount_minor=100000))
    _seed_bronze(tmp_path, events)
    rows = {r["client_id"]: r for r in gold.build_client_ltv(tmp_path)["rows"]}
    loyal, once = rows["c_new"], rows["c_once"]
    assert loyal["model"] == "km-retention/v1"
    assert loyal["expected_future_bookings"] > once["expected_future_bookings"]
    # one-timer faces the hard r_1 gate: expected future well under 2
    assert once["expected_future_bookings"] < 1.5


def test_churn_empirical_hazard(tmp_path: Path):
    """Tenant with >= 8 pooled gaps gets the survival model; a client silent
    longer than most pooled gaps scores high."""
    events = []
    # 5 clients each booking every ~10 days, 4 times (16 pooled gaps).
    for c in ("a", "b", "c", "d", "e"):
        for i in range(4):
            events.append(_ev(f"{c}{i}", "t1", "booking.confirmed",
                              f"2026-09-{i * 10 + 1:02d}T10:00:00Z", client_id=c))
    _seed_bronze(tmp_path, events)
    out = gold.build_churn_risk(tmp_path, today=date(2026, 10, 15))
    assert all(r["model"] == "empirical-hazard/v1" for r in out["rows"])
    # everyone silent ~14 days with 10-day pooled gaps -> elevated risk
    assert all(r["churn_risk"] >= 0.9 for r in out["rows"])
    # right after a booking the same population reads low
    out2 = gold.build_churn_risk(tmp_path, today=date(2026, 9, 22))
    assert all(r["churn_risk"] <= 0.2 for r in out2["rows"])


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
    assert row["model"].startswith("rule:")  # < 8 pooled gaps -> v0 fallback
    # one week after the last booking -> low risk
    out2 = gold.build_churn_risk(tmp_path, today=date(2026, 9, 28))
    (row2,) = out2["rows"]
    assert row2["tier"] == "low" and row2["churn_risk"] == 0.35


def test_pricing_suggestions(tmp_path: Path):
    """Thin history (< 10 quote-days) falls back to the absolute floors."""
    events = []
    # tenant t1: 9 instant + 1 human quotes over 5 days (share .9), 8
    # partner of 10 assignments -> raise_price via fallback rule
    for i in range(9):
        events.append(_ev(f"q{i}", "t1", "quote.created", f"2026-10-{i % 4 + 1:02d}T10:00:00Z", origin="INSTANT"))
    events.append(_ev("qh", "t1", "quote.created", "2026-10-05T10:00:00Z", origin="HUMAN"))
    for i in range(8):
        events.append(_ev(f"p{i}", "t1", "chauffeur.assigned", f"2026-10-{i + 1:02d}T10:00:00Z", partner_id=f"pt{i}"))
    for i in range(2):
        events.append(_ev(f"f{i}", "t1", "chauffeur.assigned", f"2026-10-{i + 1:02d}T11:00:00Z"))
    _seed_bronze(tmp_path, events)
    write_silver(tmp_path)

    out = gold.build_pricing_suggestions(tmp_path)
    (row,) = out["rows"]
    assert row["tenant_id"] == "t1"
    assert row["action"] == "raise_price" and row["pct"] == 5
    assert row["model"].startswith("rule:")


def test_pricing_spc_rising_demand(tmp_path: Path):
    """With >= 10 quote-days the SPC model fires: 7-day instant share above
    the tenant's own baseline + sigma with healthy fill -> raise_price."""
    events = []
    # days 1-14: baseline week share 0.6 (3 instant / 2 human per day),
    # recent week share 1.0 (5 instant per day)
    for d in range(1, 8):
        for i in range(3):
            events.append(_ev(f"q{d}i{i}", "t1", "quote.created", f"2026-09-{d:02d}T10:00:00Z", origin="INSTANT"))
        for i in range(2):
            events.append(_ev(f"q{d}h{i}", "t1", "quote.created", f"2026-09-{d:02d}T11:00:00Z", origin="HUMAN"))
    for d in range(8, 15):
        for i in range(5):
            events.append(_ev(f"q{d}i{i}", "t1", "quote.created", f"2026-09-{d:02d}T10:00:00Z", origin="INSTANT"))
    # healthy partner fill: 8 of 10
    for i in range(8):
        events.append(_ev(f"p{i}", "t1", "chauffeur.assigned", f"2026-09-{i + 1:02d}T12:00:00Z", partner_id=f"pt{i}"))
    for i in range(2):
        events.append(_ev(f"f{i}", "t1", "chauffeur.assigned", f"2026-09-{i + 1:02d}T13:00:00Z"))
    _seed_bronze(tmp_path, events)
    write_silver(tmp_path)

    out = gold.build_pricing_suggestions(tmp_path)
    (row,) = out["rows"]
    assert row["action"] == "raise_price" and row["pct"] == 5
    assert row["model"].startswith("spc/v1")
    assert row["recent_7d_share"] == 1.0 and row["baseline_mu"] == 0.6


def test_pricing_spc_steady_demand_no_action(tmp_path: Path):
    """A flat share series must not trigger: SPC needs a real excursion."""
    events = []
    for d in range(1, 15):
        for i in range(4):
            events.append(_ev(f"q{d}i{i}", "t1", "quote.created", f"2026-09-{d:02d}T10:00:00Z", origin="INSTANT"))
        events.append(_ev(f"q{d}h", "t1", "quote.created", f"2026-09-{d:02d}T11:00:00Z", origin="HUMAN"))
    for i in range(8):
        events.append(_ev(f"p{i}", "t1", "chauffeur.assigned", f"2026-09-{i + 1:02d}T12:00:00Z", partner_id=f"pt{i}"))
    _seed_bronze(tmp_path, events)
    write_silver(tmp_path)
    assert gold.build_pricing_suggestions(tmp_path)["rows"] == []


def test_pricing_suggestions_empty(tmp_path: Path):
    assert gold.build_pricing_suggestions(tmp_path)["rows"] == []
    paths = gold.write_gold(tmp_path)
    assert {p.name for p in paths} == {
        "client_ltv.json", "churn_risk.json", "pricing_suggestions.json",
        "channel_sla.json",
    }


def _receipt(eid: str, tenant: str, ref: str, status: str, at: str, channel: str = "whatsapp") -> dict:
    return {
        "id": eid, "tenant_id": tenant, "aggregate": "channel", "aggregate_id": ref,
        "event_type": "channel.receipt", "payload": {"channel": channel, "status": status, "at": at},
        "created_at": at,
    }


def test_channel_sla_latencies_and_failures(tmp_path: Path):
    """Receipt evidence -> per tenant+channel delivery SLA medians."""
    events = [
        # msg1: sent 10:00:00, delivered +30s, read +120s
        _receipt("r1", "t1", "wamid.1", "sent", "2026-11-14T10:00:00Z"),
        _receipt("r2", "t1", "wamid.1", "delivered", "2026-11-14T10:00:30Z"),
        _receipt("r3", "t1", "wamid.1", "read", "2026-11-14T10:02:00Z"),
        # msg2: sent, delivered at +60s, never read
        _receipt("r4", "t1", "wamid.2", "sent", "2026-11-14T11:00:00Z"),
        _receipt("r5", "t1", "wamid.2", "delivered", "2026-11-14T11:01:00Z"),
        # msg3: failed outright
        _receipt("r6", "t1", "wamid.3", "sent", "2026-11-14T12:00:00Z"),
        _receipt("r7", "t1", "wamid.3", "failed", "2026-11-14T12:00:05Z"),
        # tenant isolation: t2's receipts must not leak into t1's row
        _receipt("r8", "t2", "wamid.9", "sent", "2026-11-14T10:00:00Z"),
        _receipt("r9", "t2", "wamid.9", "failed", "2026-11-14T10:00:01Z"),
    ]
    _seed_bronze(tmp_path, events)

    out = gold.build_channel_sla(tmp_path)
    rows = {f"{r['tenant_id']}|{r['channel']}": r for r in out["rows"]}
    t1 = rows["t1|whatsapp"]
    assert t1["messages"] == 3
    assert t1["failure_rate"] == round(1 / 3, 4)
    assert t1["median_sent_to_delivered_s"] == 45.0  # median of 30, 60
    assert t1["median_sent_to_read_s"] == 120.0  # only msg1 was read
    assert t1["model"] == "receipt-latency/v1"
    t2 = rows["t2|whatsapp"]
    assert t2["messages"] == 1 and t2["failure_rate"] == 1.0
    assert t2["median_sent_to_delivered_s"] is None


def test_channel_sla_redelivery_first_wins(tmp_path: Path):
    """Replayed receipts (at-least-once delivery) must not double-count or
    shift latencies — first observation of a status wins."""
    events = [
        _receipt("r1", "t1", "wamid.1", "sent", "2026-11-14T10:00:00Z"),
        _receipt("r2", "t1", "wamid.1", "delivered", "2026-11-14T10:00:30Z"),
        _receipt("r3", "t1", "wamid.1", "delivered", "2026-11-14T10:05:00Z"),  # replay storm
        _receipt("r4", "t1", "wamid.1", "delivered", "2026-11-14T10:09:00Z"),
    ]
    _seed_bronze(tmp_path, events)
    (row,) = gold.build_channel_sla(tmp_path)["rows"]
    assert row["messages"] == 1
    assert row["median_sent_to_delivered_s"] == 30.0


def test_channel_sla_empty(tmp_path: Path):
    assert gold.build_channel_sla(tmp_path)["rows"] == []
