"""LiveKit token mint/verify roundtrip and webhook signature binding —
all stdlib, no LiveKit dependency."""

import base64
import hashlib
import json
import time

from voice_adapter.tokens import (mint_access_token, sign_webhook_body,
                                  verify_token, verify_webhook)

KEY, SECRET = "APIkey123", "s3cret-s3cret-s3cret"


def _claims(token: str) -> dict:
    pay = token.split(".")[1]
    return json.loads(base64.urlsafe_b64decode(pay + "=" * (-len(pay) % 4)))


def test_mint_verify_roundtrip():
    tok = mint_access_token(KEY, SECRET, identity="client-abc", room="ce-voice-t1",
                            ttl_minutes=15, name="Ada")
    claims = verify_token(tok, SECRET)
    assert claims is not None
    assert claims["iss"] == KEY
    assert claims["sub"] == "client-abc"
    assert claims["name"] == "Ada"
    grant = claims["video"]
    assert grant["roomJoin"] is True and grant["room"] == "ce-voice-t1"
    assert grant["canPublish"] and grant["canSubscribe"]
    assert 14 * 60 <= claims["exp"] - claims["iat"] <= 15 * 60 + 1


def test_wrong_secret_rejected():
    tok = mint_access_token(KEY, SECRET, identity="client-abc", room="r")
    assert verify_token(tok, "wrong-secret") is None


def test_tampered_payload_rejected():
    tok = mint_access_token(KEY, SECRET, identity="client-abc", room="r")
    head, pay, sig = tok.split(".")
    forged = _claims(tok)
    forged["video"]["room"] = "someone-elses-room"
    pay2 = base64.urlsafe_b64encode(json.dumps(forged).encode()).rstrip(b"=").decode()
    assert verify_token(f"{head}.{pay2}.{sig}", SECRET) is None


def test_expired_token_rejected():
    tok = mint_access_token(KEY, SECRET, identity="client-abc", room="r",
                            ttl_minutes=0)  # exp == iat → already past by next second
    claims = _claims(tok)
    claims["exp"] = int(time.time()) - 10
    head = tok.split(".")[0]
    pay = base64.urlsafe_b64encode(json.dumps(claims).encode()).rstrip(b"=").decode()
    import hmac
    sig = base64.urlsafe_b64encode(
        hmac.new(SECRET.encode(), f"{head}.{pay}".encode(), hashlib.sha256).digest()
    ).rstrip(b"=").decode()
    assert verify_token(f"{head}.{pay}.{sig}", SECRET) is None


def test_webhook_verify_binds_body():
    body = json.dumps({"event": "room_finished", "room": {"name": "ce-voice-t1"}}).encode()
    auth = "Bearer " + sign_webhook_body(KEY, SECRET, body)
    event = verify_webhook(auth, body, SECRET)
    assert event is not None and event["event"] == "room_finished"


def test_webhook_tampered_body_rejected():
    body = json.dumps({"event": "room_finished", "room": {"name": "a"}}).encode()
    auth = "Bearer " + sign_webhook_body(KEY, SECRET, body)
    tampered = json.dumps({"event": "room_finished", "room": {"name": "b"}}).encode()
    assert verify_webhook(auth, tampered, SECRET) is None


def test_webhook_wrong_secret_rejected():
    body = b"{}"
    auth = "Bearer " + sign_webhook_body(KEY, "other-secret", body)
    assert verify_webhook(auth, body, SECRET) is None


def test_webhook_missing_bearer_rejected():
    assert verify_webhook("", b"{}", SECRET) is None
    assert verify_webhook("not-a-jwt", b"{}", SECRET) is None
