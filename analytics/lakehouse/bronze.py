"""Bronze landing (Master Spec section 12, Phase 2): Kafka/outbox events land
as partitioned JSONL on object storage — the open lakehouse starts here.

Phase-2 shape: poll the core-api outbox feed (platform scope), append events
to bronze partitions, then mark them published. Write-then-mark keeps the
pipeline at-least-once; silver dedupes on event id. Spark/Flink/Sedona take
over streaming + spatial at the register's un-defer triggers; this module is
deliberately dependency-free stdlib so it runs anywhere core-api runs.

Layout:
    lakehouse/bronze/dt=YYYY-MM-DD/tenant=<id>/events-<unix_ms>.jsonl
"""

from __future__ import annotations

import json
import os
import time
import urllib.request
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

CORE_API_URL = os.getenv("CE_CORE_API_URL", "http://localhost:8000")
PLATFORM_TOKEN = os.getenv("CE_PLATFORM_TOKEN", "")  # edge-injected in prod


def _http(method: str, url: str, body: Any = None) -> Any:
    req = urllib.request.Request(url, method=method)
    req.add_header("Content-Type", "application/json")
    if PLATFORM_TOKEN:
        req.add_header("X-Platform-Token", PLATFORM_TOKEN)
    data = json.dumps(body).encode() if body is not None else None
    with urllib.request.urlopen(req, data) as resp:
        raw = resp.read().decode()
        return json.loads(raw) if raw else None


def fetch_unpublished(limit: int = 500, core_url: str = CORE_API_URL) -> list[dict[str, Any]]:
    return _http("GET", f"{core_url}/v1/platform/outbox/unpublished?limit={limit}") or []


def mark_published(ids: list[int], core_url: str = CORE_API_URL) -> None:
    if ids:
        _http("POST", f"{core_url}/v1/platform/outbox/published", {"ids": ids})


def partition_path(root: Path, event: dict[str, Any], now: datetime) -> Path:
    dt = now.astimezone(timezone.utc).strftime("%Y-%m-%d")
    tenant = event.get("tenant_id") or "platform"
    return root / "bronze" / f"dt={dt}" / f"tenant={tenant}"


def land_batch(events: list[dict[str, Any]], root: Path,
               now: datetime | None = None) -> Path | None:
    """Append one batch to bronze. Returns the written file (or None)."""
    if not events:
        return None
    now = now or datetime.now(timezone.utc)
    ts_ms = int(time.time() * 1000)
    # Events may span tenants; group by partition.
    by_partition: dict[Path, list[dict[str, Any]]] = {}
    for e in events:
        by_partition.setdefault(partition_path(root, e, now), []).append(e)
    last: Path | None = None
    for part, batch in by_partition.items():
        part.mkdir(parents=True, exist_ok=True)
        last = part / f"events-{ts_ms}.jsonl"
        with last.open("a") as fh:
            for e in batch:
                fh.write(json.dumps(e, separators=(",", ":")) + "\n")
    return last


def run_once(root: Path, limit: int = 500, core_url: str = CORE_API_URL) -> int:
    """One poll cycle: land bronze, then mark published. Returns count."""
    events = fetch_unpublished(limit, core_url)
    if not events:
        return 0
    land_batch(events, root)
    mark_published([e["id"] for e in events if "id" in e], core_url)
    return len(events)
