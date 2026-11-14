"""Location-message tests: dropped pins fold into the intake text with an
advisory serviceability verdict from the Phase 6 geo service; Meta webhook
location routing; failure honesty."""

import asyncio
from unittest.mock import patch

from whatsapp_adapter.main import WebhookMessage, inbound


class _Resp:
    def __init__(self, payload):
        self._payload = payload

    def raise_for_status(self):
        pass

    def json(self):
        return self._payload


class _FakeCoreGeo:
    """resolve/conversation/message/intake + geo check."""

    def __init__(self, serviceable=True, geo_fails=False):
        self.calls = []
        self.serviceable = serviceable
        self.geo_fails = geo_fails

    async def post(self, path, json=None):
        self.calls.append((path, json))
        if path.endswith("/clients/resolve"):
            return _Resp({"client": {"id": "cl-1"}})
        if path.endswith("/conversations"):
            return _Resp({"id": "conv-1", "state": "CAPTURING"})
        if path.endswith("/cases/intake"):
            return _Resp({"opened": False, "kind": "inquiry", "priority": "P4"})
        return _Resp({})

    async def get(self, path, params=None):
        self.calls.append((path, params))
        if self.geo_fails:
            raise RuntimeError("geo down")
        if self.serviceable:
            return _Resp({"serviceable": True, "areas": [{"name": "Victoria Island"}]})
        return _Resp({"serviceable": False, "areas": []})


class _FakeAgent:
    async def post(self, url, json=None):
        return _Resp({"reply": "Noted.", "state": "CAPTURING"})


class _Ctx:
    def __init__(self, impl):
        self._impl = impl

    async def __aenter__(self):
        return self._impl

    async def __aexit__(self, *a):
        return False


def _run(msg, core):
    agent = _FakeAgent()
    calls = {"n": 0}

    def pick(**kw):
        calls["n"] += 1
        return _Ctx(core if calls["n"] == 1 else agent)

    with patch("httpx.AsyncClient", side_effect=pick):
        return asyncio.run(inbound(msg))


def test_location_serviceable_folded_into_intake():
    core = _FakeCoreGeo(serviceable=True)
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678", text="",
                         external_ref="wamid.loc1", latitude=6.4281,
                         longitude=3.4219, location_name="Eko Hotel",
                         location_address="Adetokunbo Ademola St")
    out = _run(msg, core)
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "[location shared: Eko Hotel, Adetokunbo Ademola St (6.42810,3.42190)]" \
        in intake_body["text"]
    assert "serviceable: yes (Victoria Island)" in intake_body["text"]
    geo_calls = [c for c in core.calls if c[0].endswith("/geo/check")]
    assert geo_calls and geo_calls[0][1] == {"lat": 6.4281, "lng": 3.4219}
    assert out["geo"]["serviceable"] is True


def test_location_outside_service_area():
    core = _FakeCoreGeo(serviceable=False)
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678", text="",
                         external_ref="wamid.loc2", latitude=6.60, longitude=3.35)
    out = _run(msg, core)
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "serviceable: no" in intake_body["text"]
    assert out["geo"]["serviceable"] is False


def test_geo_failure_is_advisory():
    core = _FakeCoreGeo(geo_fails=True)
    msg = WebhookMessage(tenant_id="t1", contact="+31612345678", text="",
                         external_ref="wamid.loc3", latitude=6.4281, longitude=3.4219)
    out = _run(msg, core)
    intake_body = [c for c in core.calls if c[0].endswith("/cases/intake")][0][1]
    assert "[location shared:" in intake_body["text"]
    assert "serviceable" not in intake_body["text"]  # no invented verdict
    assert out["geo"]["serviceable"] is None
    assert out["agent"]["reply"] == "Noted."  # flow unbroken


def test_meta_inbound_routes_location_messages():
    from whatsapp_adapter import main as adapter_main

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.loc9", "type": "location",
                      "location": {"latitude": 25.2048, "longitude": 55.2708,
                                   "name": "DXB Terminal 3", "address": "Dubai"}}],
    }}]}]}
    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    class _Req:
        async def body(self):
            import json as _json
            return _json.dumps(payload).encode()

        headers = {"x-hub-signature-256": "sha256=ok"}

    with patch.object(adapter_main, "META_APP_SECRET", "s"), \
            patch("whatsapp_adapter.meta.verify_meta_signature", return_value=True), \
            patch.object(adapter_main, "inbound", fake_inbound), \
            patch.dict("os.environ", {"CE_META_TENANT_MAP": "pn-1:t1"}):
        out = asyncio.run(adapter_main.meta_inbound(_Req()))

    assert out["processed"] == 1
    assert seen[0].latitude == 25.2048 and seen[0].longitude == 55.2708
    assert seen[0].location_name == "DXB Terminal 3"
