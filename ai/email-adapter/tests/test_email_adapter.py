"""Phase 39 email-adapter tests — hermetic: SMTP is faked, HTTP to
core-api/agent is monkeypatched, tracking tokens are real HMAC."""

import asyncio
import base64

import pytest
from fastapi.testclient import TestClient

from email_adapter import mailer, tokens
from email_adapter.mailer import SMTPConfig, build_message, sandbox
from email_adapter import main
from email_adapter.main import app

SECRET = "test-secret"
CFG = SMTPConfig(host="smtp.test", user="concierge@taylor.example",
                 password="x", sender="concierge@taylor.example")


# ------------------------------------------------------------------ tokens

def test_token_roundtrip():
    tok = tokens.mint(SECRET, "o", "t1", "c1", "ref1")
    got = tokens.verify(SECRET, tok, "o")
    assert got == {"kind": "o", "tenant_id": "t1", "client_id": "c1",
                   "send_ref": "ref1"}


def test_token_tamper_and_family():
    tok = tokens.mint(SECRET, "o", "t1", "c1", "ref1")
    with pytest.raises(ValueError):
        tokens.verify(SECRET, tok[:-2] + "xx", "o")
    with pytest.raises(ValueError):
        tokens.verify(SECRET, tok, "c")  # open token must not validate as click
    with pytest.raises(ValueError):
        tokens.verify("other-secret", tok, "o")


# ------------------------------------------------------------------ mailer

def test_build_message_headers_and_footer():
    msg, mid = build_message(
        CFG, to="vp@acme.example", subject="F1 transfers",
        body="Transfers open.\n\n—\nTaylor\nUnsubscribe from these messages: https://core/v1/unsub/TOK\n",
        unsubscribe_url="https://core/v1/unsub/TOK")
    assert msg["List-Unsubscribe"] == "<https://core/v1/unsub/TOK>"
    assert msg["List-Unsubscribe-Post"] == "List-Unsubscribe=One-Click"
    assert mid.startswith("<") and "@taylor.example>" in mid
    assert msg["Message-ID"] == mid
    assert "Unsubscribe from these messages" in msg.get_content()


def test_build_message_tracking_rewrites_links():
    msg, _ = build_message(
        CFG, to="vp@acme.example", subject="S",
        body="See https://taylor.example/tours now",
        tracking_base="https://mail.example", tracking_secret=SECRET,
        tenant_id="t1", client_id="c1", send_ref="r1")
    text = msg.get_content()
    assert "https://mail.example/t/o/" in text  # pixel line
    assert "https://mail.example/t/c/" in text
    assert "?u=https://taylor.example/tours" in text
    # The wrapped click token verifies.
    marker = text.split("https://mail.example/t/c/", 1)[1]
    tok = marker.split("?", 1)[0]
    assert tokens.verify(SECRET, tok, "c")["send_ref"] == "r1"


def test_build_message_no_tracking_without_base():
    msg, _ = build_message(CFG, to="a@b.c", subject="S", body="go https://x.example",
                           tenant_id="t1", client_id="c1", send_ref="r1")
    text = msg.get_content()
    assert "/t/c/" not in text and "open-tracking-pixel" not in text


# --------------------------------------------------------------- send path

def test_send_sandbox_mode(monkeypatch):
    monkeypatch.setattr(main, "SMTP_CFG", SMTPConfig())  # unconfigured
    sandbox.sent.clear()
    client = TestClient(app)
    r = client.post("/v1/send", json={
        "tenant_id": "t1", "to": "vp@acme.example", "subject": "Hello",
        "body": "Body", "unsubscribe_url": "https://core/v1/unsub/TOK"})
    assert r.status_code == 200
    assert r.json()["mode"] == "sandbox"
    assert sandbox.sent[-1]["to"] == "vp@acme.example"
    assert r.json()["message_id"]


def test_send_smtp_mode(monkeypatch):
    sent = []

    class FakeSMTP:
        def __init__(self, host, port, timeout=0):
            assert host == "smtp.test"

        def __enter__(self):
            return self

        def __exit__(self, *a):
            return False

        def starttls(self):
            pass

        def login(self, u, p):
            assert u == "concierge@taylor.example"

        def send_message(self, msg, to_addrs=None):
            sent.append((msg, to_addrs))

    monkeypatch.setattr(main, "SMTP_CFG", CFG)
    monkeypatch.setattr(mailer.smtplib, "SMTP", FakeSMTP)
    client = TestClient(app)
    r = client.post("/v1/send", json={
        "tenant_id": "t1", "to": "vp@acme.example", "subject": "Hello",
        "body": "Body", "client_id": "c1"})
    assert r.json()["mode"] == "smtp"
    assert sent and sent[0][1] == ["vp@acme.example"]


def test_send_smtp_failure_is_held(monkeypatch):
    class BoomSMTP:
        def __init__(self, *a, **k):
            raise ConnectionRefusedError("down")

    monkeypatch.setattr(main, "SMTP_CFG", CFG)
    monkeypatch.setattr(mailer.smtplib, "SMTP", BoomSMTP)
    client = TestClient(app)
    r = client.post("/v1/send", json={
        "tenant_id": "t1", "to": "vp@acme.example", "subject": "S", "body": "B"})
    assert r.json()["mode"] == "held"
    assert "ConnectionRefusedError" in r.json()["reason"]


# ---------------------------------------------------------------- tracking

def test_open_pixel(monkeypatch):
    tracked = []

    async def fake_track(tenant_id, kind, client_id, send_ref):
        tracked.append((tenant_id, kind, client_id, send_ref))

    monkeypatch.setattr(main, "_track", fake_track)
    monkeypatch.setattr(main, "TRACKING_SECRET", SECRET)
    client = TestClient(app)
    tok = tokens.mint(SECRET, "o", "t1", "c1", "r1")
    r = client.get(f"/t/o/{tok}")
    assert r.status_code == 200
    assert r.headers["content-type"] == "image/gif"
    assert r.content == main.PIXEL_GIF
    assert tracked == [("t1", "email_opened", "c1", "r1")]
    # Tampered token: 403, no tracking.
    assert client.get(f"/t/o/{tok[:-2]}xx").status_code == 403
    assert len(tracked) == 1


def test_click_redirect(monkeypatch):
    tracked = []

    async def fake_track(tenant_id, kind, client_id, send_ref):
        tracked.append(kind)

    monkeypatch.setattr(main, "_track", fake_track)
    monkeypatch.setattr(main, "TRACKING_SECRET", SECRET)
    client = TestClient(app, follow_redirects=False)
    tok = tokens.mint(SECRET, "c", "t1", "c1", "r1")
    r = client.get(f"/t/c/{tok}?u=https://taylor.example/tours")
    assert r.status_code == 302
    assert r.headers["location"] == "https://taylor.example/tours"
    assert tracked == ["email_clicked"]
    # Non-http targets refused.
    assert client.get(f"/t/c/{tok}?u=javascript:alert(1)").status_code == 400


# ------------------------------------------------------------- ESP events

class FakeAsyncClient:
    """Records calls; routes resolve/consent like core-api would."""

    calls: list = []

    def __init__(self, *a, **k):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *a):
        return False

    async def post(self, path, json=None):
        FakeAsyncClient.calls.append(("POST", path, json))
        if path == "/v1/clients/resolve":
            return _Resp({"client": {"id": "c9"}, "is_new": False})
        return _Resp({})

    async def put(self, path, json=None):
        FakeAsyncClient.calls.append(("PUT", path, json))
        return _Resp({})


class _Resp:
    def __init__(self, data):
        self._d = data

    def json(self):
        return self._d

    def raise_for_status(self):
        pass


def test_complaint_suppresses_consent(monkeypatch):
    FakeAsyncClient.calls = []
    monkeypatch.setattr(main.httpx, "AsyncClient", FakeAsyncClient)
    client = TestClient(app)
    r = client.post("/webhooks/email/events", json={
        "type": "complained", "recipient": "vp@acme.example",
        "message_ref": "<m1@test>", "tenant_id": "t1"})
    assert r.json()["consent"] == "marketing=false"
    # Phase 44: the flip is tagged esp_complaint so the consent evidence
    # ledger attributes the revocation to the provider complaint.
    assert ("PUT", "/v1/clients/c9/consent",
            {"scope": "marketing", "granted": False,
             "source": "esp_complaint",
             "detail": "ESP complaint (vp@acme.example)"}) in FakeAsyncClient.calls
    # Phase 40: complaints also weigh on the deliverability guard.
    assert r.json()["attribution"] == "email_complained"
    assert ("POST", "/v1/attribution",
            {"kind": "email_complained", "channel": "email",
             "client_id": "c9", "campaign": "<m1@test>"}) in FakeAsyncClient.calls


def test_bounce_receipt_forwarded(monkeypatch):
    FakeAsyncClient.calls = []
    monkeypatch.setattr(main.httpx, "AsyncClient", FakeAsyncClient)
    client = TestClient(app)
    r = client.post("/webhooks/email/events", json={
        "type": "bounced", "message_ref": "<m2@x>", "tenant_id": "t1",
        "recipient": "dead@acme.example"})
    assert r.json()["status"] == "recorded"
    assert ("POST", "/v1/channels/receipts",
            {"channel": "email", "external_ref": "<m2@x>",
             "status": "failed", "detail": "bounced"}) in FakeAsyncClient.calls
    # Phase 40: bounces feed the deliverability guard's 7-day window.
    assert r.json()["attribution"] == "email_bounced"
    assert ("POST", "/v1/attribution",
            {"kind": "email_bounced", "channel": "email",
             "campaign": "<m2@x>"}) in FakeAsyncClient.calls
    # Phase 41: a hard bounce suppresses the address permanently.
    assert r.json()["suppression"] == "hard_bounce"
    assert ("POST", "/v1/email/suppressions",
            {"email": "dead@acme.example", "reason": "hard_bounce",
             "source_ref": "<m2@x>"}) in FakeAsyncClient.calls


# ---------------------------------------------------------------- inbound

def test_inbound_drives_full_loop(monkeypatch):
    FakeAsyncClient.calls = []
    agent_calls = []

    async def fake_post(self, url, json=None):
        agent_calls.append(json)
        return _Resp({"reply": "Happy to arrange that — a Mercedes S-Class meets you at arrivals."})

    monkeypatch.setattr(main.httpx, "AsyncClient", FakeAsyncClient)
    # The second client (agent call) uses plain httpx.AsyncClient too —
    # route by URL: FakeAsyncClient.post handles core-api paths; the agent
    # call hits an absolute URL through the same class.
    async def core_post(self, path, json=None):
        if str(path).endswith("/v1/turns"):
            return await fake_post(self, path, json)
        FakeAsyncClient.calls.append(("POST", str(path), json))
        if path == "/v1/clients/resolve":
            return _Resp({"client": {"id": "c9"}, "is_new": True})
        if path == "/v1/conversations":
            return _Resp({"id": "conv-1", "state": "CAPTURING"})
        if path == "/v1/cases/intake":
            return _Resp({"opened": False})
        return _Resp({})

    FakeAsyncClient.post = core_post
    monkeypatch.setattr(main, "SMTP_CFG", SMTPConfig())  # sandbox reply
    sandbox.sent.clear()
    monkeypatch.setattr(main, "DEFAULT_TENANT", "t1")

    client = TestClient(app)
    r = client.post("/webhooks/email/inbound", json={
        "from_email": "vp@acme.example", "from_name": "Pat Buyer",
        "subject": "F1 Portimão transfers", "text": "Need two cars race weekend.",
        "message_id": "<in1@acme.example>"})
    assert r.status_code == 200
    body = r.json()
    assert body["client_id"] == "c9" and body["conversation_id"] == "conv-1"
    assert body["replied"] is True
    # Email-channel conversation + intake idem keyed on the message id.
    assert ("POST", "/v1/conversations",
            {"client_id": "c9", "channel": "EMAIL",
             "external_ref": "<in1@acme.example>"}) in FakeAsyncClient.calls
    intake = [c for c in FakeAsyncClient.calls if c[1] == "/v1/cases/intake"]
    assert intake and intake[0][2]["idempotency_key"] == "email:<in1@acme.example>"
    # The reply went out sandbox-mode with an Re: subject.
    assert sandbox.sent[-1]["subject"] == "Re: F1 Portimão transfers"
    assert agent_calls and agent_calls[0]["client_email"] == "vp@acme.example"
