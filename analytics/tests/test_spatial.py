"""Spatial analytics tests (Phase 6): zone ping counts with the stdlib
ray-casting engine; Sedona path is exercised only where a JVM+Spark exists."""

from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path

import pytest

from analytics.lakehouse import spatial


@pytest.fixture(autouse=True)
def _force_fallback(monkeypatch):
    """Deterministic engine selection: existing tests pin the stdlib path;
    the Sedona parity test opts back in explicitly."""
    monkeypatch.setenv("CE_SPATIAL_ENGINE", "fallback")


def _seed_bronze(root: Path, events: list[dict]) -> None:
    d = root / "bronze" / "core"
    d.mkdir(parents=True, exist_ok=True)
    (d / "events.jsonl").write_text("\n".join(json.dumps(e) for e in events) + "\n")


def _ping(eid: str, tenant: str, lat: float, lng: float) -> dict:
    return {"id": eid, "tenant_id": tenant, "aggregate": "x", "aggregate_id": eid,
            "event_type": "chauffeur.location", "created_at": "2026-11-01T10:00:00Z",
            "payload": {"lat": lat, "lng": lng}}


VI = {"tenant_id": "t1", "name": "Victoria Island",
      "polygon": [[6.41, 3.39], [6.41, 3.44], [6.45, 3.44], [6.45, 3.39]]}


def test_zone_pings_fallback(tmp_path: Path):
    _seed_bronze(tmp_path, [
        _ping("1", "t1", 6.4281, 3.4219),   # inside VI
        _ping("2", "t1", 6.4300, 3.4300),   # inside VI
        _ping("3", "t1", 6.60, 3.35),       # mainland — outside
        _ping("4", "t2", 6.4281, 3.4219),   # other tenant: no areas -> outside
    ])
    out = spatial.build_zone_pings(tmp_path, [VI])
    assert out["engine"] == "stdlib-raycast"
    rows = {(r["tenant_id"], r["zone"]): r["pings"] for r in out["rows"]}
    assert rows[("t1", "Victoria Island")] == 2
    assert rows[("t1", "outside")] == 1
    assert rows[("t2", "outside")] == 1  # tenant isolation: t2 never matches t1 areas


def test_point_in_polygon_parity():
    # square (0,0)-(10,10)
    ring = [[0, 0], [0, 10], [10, 10], [10, 0]]
    assert spatial._point_in_polygon(5, 5, ring)
    assert not spatial._point_in_polygon(11, 5, ring)
    assert not spatial._point_in_polygon(-1, -1, ring)


def test_write_spatial(tmp_path: Path):
    _seed_bronze(tmp_path, [_ping("1", "t1", 6.4281, 3.4219)])
    paths = spatial.write_spatial(tmp_path, [VI])
    assert paths[0].name == "zone_pings.json"
    data = json.loads(paths[0].read_text())
    assert data["rows"][0]["zone"] == "Victoria Island"


@pytest.mark.skipif(
    importlib.util.find_spec("sedona") is None
    or importlib.util.find_spec("pyspark") is None
    or not os.environ.get("CE_SEDONA_JAR"),
    reason="needs apache-sedona + pyspark + CE_SEDONA_JAR=<shaded jar> "
           "(validated engine config; engine stays deferred by default)")
def test_zone_pings_sedona_parity(tmp_path: Path, monkeypatch):
    """Real Spark/Sedona ST_Contains join must produce byte-identical rows
    to the stdlib engine — including tenant isolation (a t2 ping inside
    t1's polygon is t2|outside, never t1|VI)."""
    monkeypatch.delenv("CE_SPATIAL_ENGINE", raising=False)
    _seed_bronze(tmp_path, [
        _ping("1", "t1", 6.4281, 3.4219),
        _ping("2", "t1", 6.4300, 3.4300),
        _ping("3", "t1", 6.60, 3.35),
        _ping("4", "t2", 6.4281, 3.4219),
    ])
    out = spatial.build_zone_pings(tmp_path, [VI])
    assert out["engine"] == "apache-sedona"
    rows = {(r["tenant_id"], r["zone"]): r["pings"] for r in out["rows"]}
    assert rows[("t1", "Victoria Island")] == 2
    assert rows[("t1", "outside")] == 1
    assert rows[("t2", "outside")] == 1
