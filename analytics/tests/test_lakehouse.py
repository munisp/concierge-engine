"""Lakehouse tests: bronze landing partitions + dedupe, silver KPIs."""

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

from analytics.lakehouse import bronze, silver


class FakeCore(BaseHTTPRequestHandler):
    events = [
        {"id": 1, "tenant_id": "t1", "event_type": "booking.confirmed", "payload": {}, "created_at": "2026-11-14T10:00:00Z"},
        {"id": 2, "tenant_id": "t1", "event_type": "payment.captured", "payload": {"amount_minor": 15000}, "created_at": "2026-11-14T10:05:00Z"},
        {"id": 3, "tenant_id": "t1", "event_type": "payout.settled", "payload": {}, "created_at": "2026-11-14T11:00:00Z"},
    ]
    published: list[int] = []

    def do_GET(self):
        body = json.dumps(self.events).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers["Content-Length"])
        body = json.loads(self.rfile.read(length))
        FakeCore.published.extend(body.get("ids", []))
        self.send_response(200)
        self.send_header("Content-Length", "2")
        self.end_headers()
        self.wfile.write(b"{}")

    def log_message(self, *args):  # quiet
        pass


def _serve():
    srv = HTTPServer(("127.0.0.1", 0), FakeCore)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    return srv


def test_bronze_landing_and_publish(tmp_path: Path):
    FakeCore.published.clear()
    srv = _serve()
    url = f"http://127.0.0.1:{srv.server_address[1]}"
    n = bronze.run_once(tmp_path, core_url=url)
    assert n == 3
    assert sorted(FakeCore.published) == [1, 2, 3]
    files = list((tmp_path / "bronze").rglob("*.jsonl"))
    assert files, "no bronze files landed"
    lines = [ln for f in files for ln in f.read_text().splitlines() if ln.strip()]
    assert len(lines) == 3
    srv.shutdown()


def test_silver_kpis_and_dedupe(tmp_path: Path):
    # Two overlapping batches: id 2 duplicated -> must not double-count.
    batch = [
        {"id": 1, "tenant_id": "t1", "event_type": "booking.confirmed", "payload": {}, "created_at": "2026-11-14T10:00:00Z"},
        {"id": 2, "tenant_id": "t1", "event_type": "payment.captured", "payload": {"amount_minor": 15000}, "created_at": "2026-11-14T10:05:00Z"},
    ]
    bronze.land_batch(batch, tmp_path)
    bronze.land_batch(batch[1:], tmp_path)
    out = silver.write_silver(tmp_path)
    report = json.loads(out.read_text())
    row = report["rows"][0]
    assert row["payments_captured"] == 1
    assert row["captured_minor"] == 15000
    assert row["bookings_confirmed"] == 1
