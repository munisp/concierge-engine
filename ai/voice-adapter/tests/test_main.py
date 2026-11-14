"""HTTP surface: honest gating, tenant scoping, signed webhooks."""

import json

import httpx
import pytest
from fastapi.testclient import TestClient

from voice_adapter import config, main, pipeline
from voice_adapter.bridge import CoreBridge
from voice_adapter.sessions import ENDED, SessionStore
from voice_adapter.tokens import sign_webhook_body


class FakeBridge(CoreBridge):
    """Core/agent stand-in: resolve + conversation only."""

    def __init__(self):
        super().__init__("http://core", "http://agent")
        self.resolved = []

    async def resolve_client(self, tenant_id, phone, name=""):
        self.resolved.append((tenant_id, phone))
        return "client-1"

    async def open_conversation(self, tenant_id, client_id, session_id):
        return {"id": "conv-1", "state": "CAPTURING"}


def client_with(store=None, bridge=None, ready=True):
    store = store or SessionStore()
    bridge = bridge or FakeBridge()
    main.app.dependency_overrides[main.get_store] = lambda: store
    main.app.dependency_overrides[main.get_bridge] = lambda: bridge
    return TestClient(main.app), store, bridge


@pytest.fixture(autouse=True)
def _clean():
    yield
    main.app.dependency_overrides.clear()


def test_health_reports_degraded_truth():
    c, _, _ = client_with()
    body = c.get("/health").json()
    assert body["service"] == "voice-adapter"
    # Sandbox/CI: no pipecat, no livekit creds, disabled by default —
    # health must say exactly that, never claim readiness.
    assert body["config"]["enabled"] is False
    assert "CE_VOICE_ENABLED=false" in body["config"]["missing"]
    assert body["pipeline"]["ready"] is False


def test_session_refused_when_not_ready(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": False, "ready": False,
                                 "missing": ["pipecat-ai", "livekit credentials"]})
    c, _, _ = client_with()
    r = c.post("/v1/voice/sessions", json={"phone": "+971500000000"},
               headers={"X-Tenant-ID": "t1"})
    assert r.status_code == 503
    detail = r.json()["detail"]
    assert detail["missing"] == ["pipecat-ai", "livekit credentials"]


def test_session_created_when_ready(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "APIkey")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s3cret")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk.example")
    c, store, bridge = client_with()
    r = c.post("/v1/voice/sessions",
               json={"phone": "+971500000000", "display_name": "Ada"},
               headers={"X-Tenant-ID": "tenant-1"})
    assert r.status_code == 201
    body = r.json()
    assert bridge.resolved == [("tenant-1", "+971500000000")]
    assert body["livekit_url"] == "wss://lk.example"
    assert body["room"].startswith("ce-voice-tenant-1-client-1-")
    assert body["session"]["conversation_id"] == "conv-1"
    # Token is a real LiveKit JWT verifiable with the configured secret.
    from voice_adapter.tokens import verify_token
    claims = verify_token(body["token"], "s3cret")
    assert claims["sub"] == "client-client-1"
    assert claims["video"]["room"] == body["room"]


def test_session_get_tenant_scoped(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")
    c, store, _ = client_with()
    sid = c.post("/v1/voice/sessions", json={"phone": "+1"},
                 headers={"X-Tenant-ID": "t1"}).json()["session"]["id"]
    assert c.get(f"/v1/voice/sessions/{sid}",
                 headers={"X-Tenant-ID": "t1"}).status_code == 200
    assert c.get(f"/v1/voice/sessions/{sid}",
                 headers={"X-Tenant-ID": "t2"}).status_code == 404


def test_hangup_ends_session(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")
    c, store, _ = client_with()
    sid = c.post("/v1/voice/sessions", json={"phone": "+1"},
                 headers={"X-Tenant-ID": "t1"}).json()["session"]["id"]
    r = c.delete(f"/v1/voice/sessions/{sid}", headers={"X-Tenant-ID": "t1"})
    assert r.status_code == 200
    assert r.json()["session"]["state"] == ENDED
    assert r.json()["session"]["end_reason"] == "hangup"


def test_webhook_requires_valid_signature(monkeypatch):
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "hook-secret")
    c, store, _ = client_with()
    sess = store.create("t1", "c1", "+1", "room-x", "conv-1")

    body = json.dumps({"event": "room_finished",
                       "room": {"name": "room-x"}}).encode()
    r = c.post("/webhooks/livekit", content=body,
               headers={"Authorization": "Bearer invalid"})
    assert r.status_code == 401

    auth = "Bearer " + sign_webhook_body("key", "hook-secret", body)
    r = c.post("/webhooks/livekit", content=body,
               headers={"Authorization": auth})
    assert r.status_code == 200
    assert store.get(sess.id).state == ENDED


def test_sessions_list_tenant_scoped(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")
    c, store, _ = client_with()
    for tenant, phone in [("t1", "+1"), ("t1", "+2"), ("t2", "+3")]:
        c.post("/v1/voice/sessions", json={"phone": phone},
               headers={"X-Tenant-ID": tenant})
    all_t1 = c.get("/v1/voice/sessions", headers={"X-Tenant-ID": "t1"}).json()["sessions"]
    active_t1 = c.get("/v1/voice/sessions?active=1",
                      headers={"X-Tenant-ID": "t1"}).json()["sessions"]
    all_t2 = c.get("/v1/voice/sessions", headers={"X-Tenant-ID": "t2"}).json()["sessions"]
    assert len(all_t1) == 2 and len(active_t1) == 2 and len(all_t2) == 1


def test_takeover_mints_staff_token_and_records_provenance(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s3cret")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")

    posted: list[dict] = []

    class TakeoverBridge(FakeBridge):
        async def post_message(self, tenant_id, conversation_id, author, body):
            posted.append({"author": author, "body": body})
            return True

    c, store, _ = client_with(bridge=TakeoverBridge())
    sid = c.post("/v1/voice/sessions", json={"phone": "+1"},
                 headers={"X-Tenant-ID": "t1"}).json()["session"]["id"]
    r = c.post(f"/v1/voice/sessions/{sid}/takeover",
               json={"staff_name": "Muna S"}, headers={"X-Tenant-ID": "t1"})
    assert r.status_code == 200
    body = r.json()
    assert body["identity"] == "staff-munas"
    from voice_adapter.tokens import verify_token
    claims = verify_token(body["token"], "s3cret")
    assert claims["sub"] == "staff-munas"
    assert claims["video"]["room"] == body["room"]
    assert posted == [{"author": "SYSTEM",
                       "body": "[human concierge joining: Muna S]"}]


def test_takeover_ended_session_conflict(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")
    c, store, _ = client_with()
    sid = c.post("/v1/voice/sessions", json={"phone": "+1"},
                 headers={"X-Tenant-ID": "t1"}).json()["session"]["id"]
    c.delete(f"/v1/voice/sessions/{sid}", headers={"X-Tenant-ID": "t1"})
    r = c.post(f"/v1/voice/sessions/{sid}/takeover",
               json={"staff_name": "Muna"}, headers={"X-Tenant-ID": "t1"})
    assert r.status_code == 409


def test_takeover_tenant_scoped_and_validated(monkeypatch):
    monkeypatch.setattr(pipeline, "pipeline_readiness",
                        lambda: {"pipecat": True, "ready": True, "missing": []})
    monkeypatch.setattr(config, "LIVEKIT_API_KEY", "k")
    monkeypatch.setattr(config, "LIVEKIT_API_SECRET", "s")
    monkeypatch.setattr(config, "LIVEKIT_URL", "wss://lk")
    c, store, _ = client_with()
    sid = c.post("/v1/voice/sessions", json={"phone": "+1"},
                 headers={"X-Tenant-ID": "t1"}).json()["session"]["id"]
    assert c.post(f"/v1/voice/sessions/{sid}/takeover",
                  json={"staff_name": "Muna"},
                  headers={"X-Tenant-ID": "t2"}).status_code == 404
    assert c.post(f"/v1/voice/sessions/{sid}/takeover",
                  json={"staff_name": "  "},
                  headers={"X-Tenant-ID": "t1"}).status_code == 422
