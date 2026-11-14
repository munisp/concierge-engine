"""Gold layer (Master Spec §14 — "ML pricing/churn/LTV").

v1 replaces the Phase-3 flat heuristics with probabilistic models fitted
from the event stream, stdlib-only and fully deterministic (no RNG — model
parameters are closed-form estimates from the data, so CI needs no GPU and
results are reproducible byte-for-byte):

- **LTV** — smoothed empirical retention curve (Kaplan-Meier style): the
  per-tenant continuation probabilities r_k = P(client reaches booking
  k+1 | reached k), Laplace-smoothed and shrunk toward the platform curve
  for thin tenants. Expected future bookings per client = sum over the
  4-booking horizon of the chained continuation probabilities. This is
  strictly richer than v0's flat repeat_prob: a client at booking 4 in a
  tenant where veterans keep booking scores higher than a one-timer.
- **Churn** — empirical-hazard survival model: pool observed inter-booking
  gaps per tenant, build the survival curve S(d) = P(gap > d), then score
  each client at their cadence-adjusted silence: clients who book 2x
  faster than the tenant median get penalized 2x faster. Thin tenants fall
  back to the v0 rule, labeled.
- **Pricing** — statistical process control on the tenant's own daily
  instant-share series (current 7-day mean vs trailing mean + 1 sigma)
  instead of fixed 0.8/0.2 cutoffs; still bounded at ±5% per cycle and
  never self-executing. Thin history falls back to the v0 absolute floors.

Every row carries `model` (or `rule` when on a fallback) — the audit trail
always knows what produced a number. The autonomy-dial contract is
unchanged: these outputs advise humans (or the gated agent).

Consumes: lakehouse/bronze (raw events), lakehouse/silver/daily_kpis.json
Produces: lakehouse/gold/client_ltv.json, churn_risk.json,
          pricing_suggestions.json, channel_sla.json
"""

from __future__ import annotations

import json
import statistics
from collections import defaultdict
from datetime import date, datetime
from pathlib import Path
from typing import Any

from .silver import _load_bronze


def _ts(e: dict[str, Any]) -> str:
    return str(e.get("created_at", ""))


def _client_events(events: list[dict[str, Any]]) -> dict[str, list[dict[str, Any]]]:
    """booking.confirmed + payment.captured grouped by client."""
    by_client: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for e in events:
        payload = e.get("payload") or {}
        cid = payload.get("client_id")
        if cid and e.get("event_type") in ("booking.confirmed", "payment.captured"):
            by_client[f"{e.get('tenant_id', 'platform')}|{cid}"].append(e)
    for v in by_client.values():
        v.sort(key=_ts)
    return by_client


def _retention_curve(counts: list[int], horizon: int) -> list[float]:
    """Smoothed empirical continuation probabilities r_1..r_horizon from a
    list of per-client booking counts (Kaplan-Meier style, Laplace
    smoothing). r_k = (clients with > k bookings + 1) / (clients with >= k
    bookings + 2)."""
    curve = []
    for k in range(1, horizon + 1):
        ge = sum(1 for n in counts if n >= k)
        gt = sum(1 for n in counts if n > k)
        curve.append((gt + 1) / (ge + 2) if ge else 0.5)
    return curve


def _expected_future_bookings(n: int, curve: list[float], horizon: int) -> float:
    """E[future bookings in horizon] for a client at n bookings: chained
    continuation probabilities from their current position on the curve.
    Beyond the fitted range the curve's last value persists."""
    total, cont = 0.0, 1.0
    for j in range(horizon):
        k = n + j  # index of the continuation prob needed (r_k: reach k+1 | reached k)
        r = curve[min(k, len(curve)) - 1] if k >= 1 else curve[0]
        cont *= r
        total += cont
    return total


def build_client_ltv(root: Path) -> dict[str, Any]:
    """LTV per client = observed revenue + expected repeat revenue, where the
    expectation comes from the tenant's smoothed retention curve shrunk
    toward the platform curve (thin tenants) or a 0.25 flat prior (no
    repeat data anywhere). Model label on every row."""
    horizon = 4
    events = _load_bronze(root)
    by_client = _client_events(events)
    per_tenant_counts: dict[str, list[int]] = defaultdict(list)
    for key, evs in by_client.items():
        tenant = key.split("|", 1)[0]
        n = sum(1 for e in evs if e.get("event_type") == "booking.confirmed")
        per_tenant_counts[tenant].append(n)

    platform_counts = [n for ns in per_tenant_counts.values() for n in ns]
    platform_curve = _retention_curve(platform_counts, horizon)
    curves: dict[str, list[float]] = {}
    labels: dict[str, str] = {}
    for tenant, ns in per_tenant_counts.items():
        tc = _retention_curve(ns, horizon)
        if len(ns) >= 5:
            curves[tenant], labels[tenant] = tc, "km-retention/v1"
        elif len(platform_counts) >= 5:
            # Empirical-Bayes style shrinkage toward the platform curve.
            w = len(ns) / (len(ns) + 5.0)
            curves[tenant] = [w * a + (1 - w) * b for a, b in zip(tc, platform_curve)]
            labels[tenant] = "km-retention/v1 (shrunk to platform)"
        else:
            curves[tenant], labels[tenant] = [0.25] * horizon, "prior:flat-0.25"

    rows = []
    for key, evs in sorted(by_client.items()):
        tenant, cid = key.split("|", 1)
        revenue = sum(
            int((e.get("payload") or {}).get("amount_minor", 0))
            for e in evs if e.get("event_type") == "payment.captured"
        )
        bookings = sum(1 for e in evs if e.get("event_type") == "booking.confirmed")
        avg = revenue // bookings if bookings else revenue
        efb = _expected_future_bookings(bookings, curves[tenant], horizon)
        expected = int(avg * efb)
        rows.append({
            "tenant_id": tenant, "client_id": cid,
            "bookings": bookings, "revenue_minor": revenue,
            "avg_booking_minor": avg,
            "expected_future_bookings": round(efb, 3),
            "ltv_minor": revenue + expected,
            "model": labels[tenant],
        })
    return {"grain": "tenant|client", "rows": rows}


def build_churn_risk(root: Path, today: date | None = None) -> dict[str, Any]:
    """Churn risk from an empirical-hazard survival model over the tenant's
    pooled inter-booking gaps.

    S(d) = fraction of the tenant's observed gaps longer than d. A client's
    silence is cadence-adjusted (silence * tenant_median / client_median)
    so habitual fast bookers score risky sooner; risk = 1 - S(adjusted).
    Tenants with < 8 pooled gaps fall back to the v0 recency rule
    (labeled). Tiers drive concierge outreach playbooks: high >= 0.75,
    medium >= 0.4."""
    today = today or date.today()
    by_client = _client_events(_load_bronze(root))

    per_client: dict[str, dict[str, Any]] = {}
    tenant_gaps: dict[str, list[int]] = defaultdict(list)
    for key, evs in by_client.items():
        tenant, cid = key.split("|", 1)
        days = []
        for e in evs:
            if e.get("event_type") != "booking.confirmed":
                continue
            try:
                days.append(datetime.fromisoformat(_ts(e).replace("Z", "+00:00")).date())
            except ValueError:
                continue
        if not days:
            continue
        gaps = [max((b - a).days, 1) for a, b in zip(days, days[1:])]
        tenant_gaps[tenant].extend(gaps)
        per_client[key] = {
            "tenant": tenant, "cid": cid, "days": days,
            "typical": statistics.median(gaps) if gaps else None,
        }

    tenant_medians = {t: statistics.median(g) for t, g in tenant_gaps.items() if g}

    rows = []
    for key, info in sorted(per_client.items()):
        tenant, cid, days = info["tenant"], info["cid"], info["days"]
        gaps = tenant_gaps[tenant]
        since = max((today - days[-1]).days, 0)
        typical = info["typical"] if info["typical"] is not None else tenant_medians.get(tenant, 45)
        if len(gaps) >= 8:
            median_gap = tenant_medians[tenant]
            adjusted = since * (median_gap / typical) if typical else since
            # 1 - S(adjusted): fraction of pooled gaps the silence has outlasted
            risk = sum(1 for g in gaps if g <= adjusted) / len(gaps)
            model = "empirical-hazard/v1"
        else:
            risk = min(1.0, since / (2 * typical))
            model = "rule:min(1, days_since / (2*median_gap))"
        risk = round(min(1.0, risk), 3)
        tier = "high" if risk >= 0.75 else "medium" if risk >= 0.4 else "low"
        rows.append({
            "tenant_id": tenant, "client_id": cid,
            "days_since_last_booking": since, "typical_gap_days": typical,
            "churn_risk": risk, "tier": tier,
            "model": model,
        })
    return {"grain": "tenant|client", "rows": rows}


def build_pricing_suggestions(root: Path) -> dict[str, Any]:
    """Pricing suggestions from silver daily KPIs — bounded nudges only
    (±5% max per cycle, never self-executing; a human or the
    autonomy-dial-gated agent applies them).

    Demand signal is statistical process control on the tenant's own daily
    instant-share series: the trailing 7-day mean is compared against the
    prior history's mean + 1 sigma, so each tenant is judged against its
    own baseline rather than a one-size-fits-all 0.8 cutoff. Tenants with
    < 10 days of quote history fall back to the v0 absolute floors
    (labeled). Supply-side partner-fill checks are unchanged (thin supply
    is a structural fact, not a trend)."""
    silver_path = root / "silver" / "daily_kpis.json"
    if not silver_path.exists():
        return {"grain": "tenant", "rows": []}
    silver = json.loads(silver_path.read_text())

    days_by_tenant: dict[str, list[dict[str, Any]]] = defaultdict(list)
    for r in silver.get("rows", []):
        days_by_tenant[r["tenant_id"]].append(r)

    rows = []
    for tenant, ds in sorted(days_by_tenant.items()):
        ds.sort(key=lambda r: r["day"])
        quotes = sum(r["quotes_instant"] + r["quotes_human"] for r in ds)
        assigns = sum(r["assignments_partner"] + r["assignments_fleet"] for r in ds)
        partner = sum(r["assignments_partner"] for r in ds)
        instant = sum(r["quotes_instant"] for r in ds)
        fill = partner / assigns if assigns else 0.0
        instant_share = instant / quotes if quotes else 0.0
        ctx = {"instant_share": round(instant_share, 3), "partner_fill": round(fill, 3)}

        # Daily instant-share series for days that had any quotes.
        series = [
            r["quotes_instant"] / (r["quotes_instant"] + r["quotes_human"])
            for r in ds if r["quotes_instant"] + r["quotes_human"] > 0
        ]
        action = None
        model = ""
        if len(series) >= 10:
            recent, hist = series[-7:], series[:-7]
            if hist:
                mu = statistics.mean(hist)
                sigma = statistics.stdev(hist) if len(hist) > 1 else 0.0
                recent_mu = statistics.mean(recent)
                ctx["recent_7d_share"] = round(recent_mu, 3)
                ctx["baseline_mu"] = round(mu, 3)
                if recent_mu >= mu + max(sigma, 0.05) and fill >= 0.5:
                    action, pct = "raise_price", 5
                    model = "spc/v1: 7d share >= baseline + max(1sigma, 0.05) & fill>=0.5"
                elif recent_mu <= mu - max(sigma, 0.05):
                    action, pct = "review_catalog", 0
                    model = "spc/v1: 7d share <= baseline - max(1sigma, 0.05)"
        else:
            if quotes >= 10 and instant_share >= 0.8 and fill >= 0.5:
                action, pct = "raise_price", 5
                model = "rule:instant_share>=0.8 & partner_fill>=0.5 (thin history)"
            elif quotes >= 10 and instant_share <= 0.2:
                action, pct = "review_catalog", 0
                model = "rule:instant_share<=0.2 (thin history)"
        if action is None and assigns >= 10 and fill <= 0.2:
            action, pct = "raise_partner_margin", 5
            model = "rule:partner_fill<=0.2"
        if action:
            rows.append({"tenant_id": tenant, "action": action, "pct": pct,
                         "model": model, **ctx})
    return {"grain": "tenant", "rows": rows}


def build_channel_sla(root: Path) -> dict[str, Any]:
    """Channel delivery SLA per tenant+channel from channel.receipt events
    (Phase 13). For every channel message (aggregate_id = external_ref),
    pair the provider timestamps: sent->delivered and sent->read latencies,
    and a failure flag. Aggregate as medians (robust to the long tail of
    WhatsApp delivery) + failure rate over distinct messages.

    This is descriptive statistics over evidence, not a model — rows are
    labeled receipt-latency/v1 so downstream consumers never confuse a
    measured median with a prediction."""
    events = _load_bronze(root)
    by_msg: dict[str, dict[str, Any]] = defaultdict(dict)
    for e in events:
        if e.get("event_type") != "channel.receipt":
            continue
        payload = e.get("payload") or {}
        status = payload.get("status")
        if status not in ("sent", "delivered", "read", "failed"):
            continue
        tenant = e.get("tenant_id", "platform")
        channel = payload.get("channel") or "whatsapp"
        ref = e.get("aggregate_id", "")
        at = payload.get("at") or _ts(e)
        slot = by_msg[f"{tenant}|{channel}|{ref}"]
        # First observation of a status wins — redelivery replays are noise.
        slot.setdefault(status, at)
        slot["tenant"], slot["channel"] = tenant, channel

    per_pair: dict[str, dict[str, list]] = defaultdict(
        lambda: {"messages": 0, "failed": 0, "delivered_s": [], "read_s": []})
    for msg in by_msg.values():
        key = f"{msg['tenant']}|{msg['channel']}"
        agg = per_pair[key]
        agg["messages"] += 1
        if "failed" in msg:
            agg["failed"] += 1
        sent = _parse_ts(msg.get("sent", ""))
        if sent:
            d = _parse_ts(msg.get("delivered", ""))
            if d and d >= sent:
                agg["delivered_s"].append((d - sent).total_seconds())
            r = _parse_ts(msg.get("read", ""))
            if r and r >= sent:
                agg["read_s"].append((r - sent).total_seconds())

    rows = []
    for key, agg in sorted(per_pair.items()):
        tenant, channel = key.split("|", 1)
        rows.append({
            "tenant_id": tenant, "channel": channel,
            "messages": agg["messages"],
            "failure_rate": round(agg["failed"] / agg["messages"], 4),
            "median_sent_to_delivered_s": (
                round(statistics.median(agg["delivered_s"]), 1) if agg["delivered_s"] else None),
            "median_sent_to_read_s": (
                round(statistics.median(agg["read_s"]), 1) if agg["read_s"] else None),
            "model": "receipt-latency/v1",
        })
    return {"grain": "tenant|channel", "rows": rows}


def _parse_ts(s: str) -> datetime | None:
    if not s:
        return None
    try:
        return datetime.fromisoformat(str(s).replace("Z", "+00:00"))
    except ValueError:
        return None


def write_gold(root: Path) -> list[Path]:
    out = root / "gold"
    out.mkdir(parents=True, exist_ok=True)
    paths = []
    for name, data in (
        ("client_ltv", build_client_ltv(root)),
        ("churn_risk", build_churn_risk(root)),
        ("pricing_suggestions", build_pricing_suggestions(root)),
        ("channel_sla", build_channel_sla(root)),
    ):
        p = out / f"{name}.json"
        p.write_text(json.dumps(data, indent=2))
        paths.append(p)
    return paths
