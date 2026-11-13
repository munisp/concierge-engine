"""Phase 4 inference tests: Ollama (llama.cpp/Qwen) provider against a fake
daemon, redaction-before-inference, and the deterministic rule floor. No GPU,
no pulled model, no network — CI-safe."""

from __future__ import annotations

import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer

import pytest
from fastapi.testclient import TestClient

from concierge_agent.inference import ModelRouter, OllamaProvider
from concierge_agent.main import app


class _FakeOllama(BaseHTTPRequestHandler):
    """Minimal Ollama stand-in. Records the last request body; responds with
    the queued payloads. Modes: ok, garbage, down (connection refused by
    pointing the client at a closed port instead)."""

    queue: list[dict] = []
    seen: list[dict] = []

    def _handle(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        try:
            _FakeOllama.seen.append(json.loads(body or b"{}"))
        except json.JSONDecodeError:
            _FakeOllama.seen.append({})
        if self.path == "/api/tags":
            payload = {"models": [{"name": "qwen3:8b"}]}
        elif _FakeOllama.queue:
            payload = _FakeOllama.queue.pop(0)
        else:
            payload = {"choices": [{"message": {"content": "ok"}}]}
        data = json.dumps(payload).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = _handle
    do_POST = _handle

    def log_message(self, *args):  # silence test noise
        pass


@pytest.fixture()
def fake_ollama():
    _FakeOllama.queue = []
    _FakeOllama.seen = []
    server = HTTPServer(("127.0.0.1", 0), _FakeOllama)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{server.server_address[1]}"
    provider = OllamaProvider(base=base, model="qwen3:8b", timeout=5)
    yield provider, _FakeOllama
    server.shutdown()


def test_chat_against_fake_daemon(fake_ollama):
    provider, fake = fake_ollama
    fake.queue.append({"choices": [{"message": {"content": "hello back"}}]})
    out = provider.chat("sys", "user text")
    assert out == "hello back"
    assert fake.seen[0]["model"] == "qwen3:8b"
    assert fake.seen[0]["messages"][0]["role"] == "system"


def test_triage_uses_qwen_and_redacts_first(fake_ollama):
    provider, fake = fake_ollama
    fake.queue.append({"choices": [{"message": {"content":
        '{"kind":"booking","priority":"P2","summary":"Airport run tonight","reason":"time-critical"}'}}]})
    router = ModelRouter(ollama=provider)
    out = router.triage("Need a car tonight, reach me at ada@example.com")
    assert out["kind"] == "booking" and out["priority"] == "P2"
    assert out["engine"] == "ollama/qwen3:8b"
    # the model never saw the raw email
    sent = json.dumps(fake.seen)
    assert "ada@example.com" not in sent
    assert "[EMAIL_REDACTED]" in sent
    assert "email" in out["redacted_kinds"]


def test_triage_rule_fallback_when_ollama_down():
    provider = OllamaProvider(base="http://127.0.0.1:9", model="qwen3:8b", timeout=1)
    router = ModelRouter(ollama=provider)
    out = router.triage("We need a helicopter transfer urgently")
    assert out["engine"] == "rules"
    assert out["kind"] in {"vip_request", "booking"}
    assert out["priority"] == "P2"


def test_triage_rule_fallback_on_garbage_json(fake_ollama):
    provider, fake = fake_ollama
    fake.queue.append({"choices": [{"message": {"content": "I cannot classify that, sorry!"}}]})
    router = ModelRouter(ollama=provider)
    out = router.triage("book me a ride tomorrow")
    assert out["engine"] == "rules" and out["kind"] == "booking"


def test_summarize_and_next_action_fallbacks():
    provider = OllamaProvider(base="http://127.0.0.1:9", model="qwen3:8b", timeout=1)
    router = ModelRouter(ollama=provider)
    assert "No activity" in router.summarize([])["summary"]
    assert "quote created" in router.summarize(["case opened", "quote created"])["summary"]
    assert router.summarize(["case opened"])["engine"] == "rules"
    nxt = router.next_action("WAITING_PARTNER", "partner silent 2h")
    assert nxt["engine"] == "rules" and "partner" in nxt["action"].lower()


def test_endpoints():
    from concierge_agent import main
    # pin to a dead daemon: this test asserts the rules-floor contract and
    # must hold whether or not the dev box has Ollama running
    main.router = ModelRouter(ollama=OllamaProvider(base="http://127.0.0.1:9", model="qwen3:8b", timeout=1))
    client = TestClient(app)
    # rules path (no ollama in CI) — contract stability for core-api
    r = client.post("/v1/ai/triage", json={"text": "need a quote for a transfer"})
    assert r.status_code == 200
    body = r.json()
    assert body["kind"] == "booking" and body["engine"] == "rules"
    r = client.post("/v1/ai/summarize", json={"timeline": ["opened", "quoted"]})
    assert r.status_code == 200 and r.json()["summary"] and r.json()["engine"] == "rules"
    r = client.post("/v1/ai/next-action", json={"case_state": "OPEN", "summary": "new case"})
    assert r.status_code == 200 and r.json()["action"]
    r = client.get("/v1/ai/health")
    assert r.status_code == 200 and "ollama" in r.json()
    # embed degrades to 503 without a daemon — core-api treats it as advisory
    r = client.post("/v1/ai/embed", json={"text": "airport transfer"})
    assert r.status_code == 503


def test_embed_against_fake_daemon(fake_ollama):
    provider, fake = fake_ollama
    fake.queue.append({"data": [{"embedding": [0.1, 0.2, 0.3]}]})
    router = ModelRouter(ollama=provider)
    vec, engine = router.embed("VIP client ada@example.com needs a car")
    assert vec == [0.1, 0.2, 0.3]
    assert engine.startswith("ollama/")
    # redaction applies to embeddings too — the daemon saw no raw email
    sent = json.dumps(fake.seen)
    assert "ada@example.com" not in sent
