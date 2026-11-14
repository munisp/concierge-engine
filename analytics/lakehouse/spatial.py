"""Spatial analytics (Phase 6): zone-level aggregation over GPS pings.

Two execution paths with identical semantics:

- **Apache Sedona** (optional): when `apache-sedona` is installed and a
  Spark session is available, the ping-to-zone join runs as a real
  ST_Contains spatial SQL query — the scale-out path for fleet-wide
  tracking volume.
- **stdlib fallback** (default): ray-casting point-in-polygon in pure
  Python. Deterministic, CI-friendly, correct at Phase-6 volume.

This mirrors the deferred-register contract: the interface ships now, the
engine un-defers when the spatial-SQL trigger fires (>=3 tenants or >=50k
trips — same trigger as Spark silver→gold).
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

from .silver import _load_bronze


def _point_in_polygon(lat: float, lng: float, ring: list[list[float]]) -> bool:
    """Ray casting on [lat, lng] rings — parity with the Go core-api
    implementation (domain/geo.go)."""
    inside = False
    j = len(ring) - 1
    for i in range(len(ring)):
        yi, xi = ring[i]
        yj, xj = ring[j]
        if (xi > lng) != (xj > lng) and lat < (yj - yi) * (lng - xi) / (xj - xi) + yi:
            inside = not inside
        j = i
    return inside


def _ping_events(events: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Events carrying a position: chauffeur.location / gps.ping payloads
    with lat/lng."""
    out = []
    for e in events:
        if e.get("event_type") not in ("chauffeur.location", "gps.ping"):
            continue
        p = e.get("payload") or {}
        if isinstance(p.get("lat"), (int, float)) and isinstance(p.get("lng"), (int, float)):
            out.append(e)
    return out


def zone_ping_counts_fallback(pings: list[dict[str, Any]],
                              areas: list[dict[str, Any]]) -> dict[str, Any]:
    """stdlib path: count pings per (tenant, zone)."""
    counts: dict[str, int] = {}
    for e in pings:
        p = e["payload"]
        tenant = e.get("tenant_id", "platform")
        zone = "outside"
        for a in areas:
            if a.get("tenant_id") != tenant:
                continue
            if _point_in_polygon(p["lat"], p["lng"], a["polygon"]):
                zone = a["name"]
                break
        counts[f"{tenant}|{zone}"] = counts.get(f"{tenant}|{zone}", 0) + 1
    rows = [
        {"tenant_id": k.split("|", 1)[0], "zone": k.split("|", 1)[1], "pings": v}
        for k, v in sorted(counts.items())
    ]
    return {"grain": "tenant|zone", "engine": "stdlib-raycast", "rows": rows}


def zone_ping_counts_sedona(pings: list[dict[str, Any]],
                            areas: list[dict[str, Any]]) -> dict[str, Any]:
    """Apache Sedona path: ST_Contains spatial join on Spark. Only reached
    when sedona is importable; raises ImportError otherwise so the caller
    falls back."""
    from sedona.spark import SedonaContext  # noqa: PLC0415 — optional dep

    import os

    builder = SedonaContext.builder()  # type: ignore[attr-defined]
    jar = os.getenv("CE_SEDONA_JAR")  # shaded sedona-spark jar, when not on the classpath
    if jar:
        builder = builder.config("spark.jars", jar)
    master = os.getenv("CE_SPARK_MASTER")
    if master:
        builder = builder.master(master)
    # SedonaContext.create, not bare getOrCreate: registers the Sedona catalog
    # and ST_* SQL functions — without it ST_Contains is unresolved.
    spark = SedonaContext.create(builder.getOrCreate())
    try:
        import json as _json

        pings_rdd = spark.sparkContext.parallelize([
            _json.dumps({"tenant_id": e.get("tenant_id", "platform"),
                         "lng": e["payload"]["lng"], "lat": e["payload"]["lat"]})
            for e in pings
        ])
        areas_rdd = spark.sparkContext.parallelize([
            _json.dumps({"tenant_id": a.get("tenant_id"), "name": a["name"],
                         "wkt": "POLYGON((" + ",".join(
                             f"{pt[1]} {pt[0]}" for pt in a["polygon"] + [a["polygon"][0]]
                         ) + "))"})
            for a in areas
        ])
        spark.read.json(pings_rdd).createOrReplaceTempView("pings")
        spark.read.json(areas_rdd).createOrReplaceTempView("areas")
        spark.sql("SELECT 1").collect()  # force session warm
        result = spark.sql(
            """
            SELECT p.tenant_id AS tenant_id, COALESCE(a.name, 'outside') AS zone,
                   COUNT(*) AS pings
            FROM pings p
            LEFT JOIN areas a
              ON a.tenant_id = p.tenant_id
             AND ST_Contains(ST_GeomFromWKT(a.wkt), ST_Point(p.lng, p.lat))
            GROUP BY p.tenant_id, zone
            ORDER BY tenant_id, zone
            """
        ).collect()
        rows = [{"tenant_id": r["tenant_id"], "zone": r["zone"], "pings": r["pings"]}
                for r in result]
        return {"grain": "tenant|zone", "engine": "apache-sedona", "rows": rows}
    finally:
        spark.stop()


def build_zone_pings(root: Path, areas: list[dict[str, Any]] | None = None) -> dict[str, Any]:
    """Zone ping counts from bronze. `areas` are service-area exports
    (list of {tenant_id, name, polygon}). Sedona when available, stdlib
    otherwise; the engine is labeled on the output either way.
    CE_SPATIAL_ENGINE=fallback forces the stdlib path (tests, CI)."""
    import os

    pings = _ping_events(_load_bronze(root))
    areas = areas or []
    if os.getenv("CE_SPATIAL_ENGINE", "auto") == "fallback":
        return zone_ping_counts_fallback(pings, areas)
    try:
        return zone_ping_counts_sedona(pings, areas)
    except ImportError:
        return zone_ping_counts_fallback(pings, areas)
    except Exception:
        # Sedona present but Spark failed to start (no JVM, etc.) — degrade.
        return zone_ping_counts_fallback(pings, areas)


def write_spatial(root: Path, areas: list[dict[str, Any]] | None = None) -> list[Path]:
    out = root / "spatial"
    out.mkdir(parents=True, exist_ok=True)
    p = out / "zone_pings.json"
    p.write_text(json.dumps(build_zone_pings(root, areas), indent=2))
    return [p]
