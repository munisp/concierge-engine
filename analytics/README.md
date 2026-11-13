# Lakehouse (Master Spec section 12)

Open lakehouse, no proprietary warehouse. Phase 2 ships the **bronze + silver
batch core** in dependency-free Python so it runs anywhere:

- `bronze.py` — polls the core-api outbox feed (`/v1/platform/outbox/...`),
  lands events as partitioned JSONL (`lakehouse/bronze/dt=…/tenant=…/`),
  marks published after write (at-least-once; silver dedupes on event id).
- `silver.py` — batch aggregates: daily per-tenant GTV, capture totals,
  instant-vs-human quote mix, partner fill rate, payout outcomes
  (`lakehouse/silver/daily_kpis.json`).

Un-defer triggers (see docs/deferred-register.md): at >=3 tenants or >=50k
trips, Spark/Sedona/Ray/DataFusion take over stream + spatial compute — the
bronze layout is already partitioned for them, and the event schema is the
same outbox contract Kafka carries.

Run:

```bash
CE_CORE_API_URL=http://localhost:8000 python -m analytics.lakehouse.bronze  # (library; called by a cron/worker)
python -m pytest analytics/tests -q
```
