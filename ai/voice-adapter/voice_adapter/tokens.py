"""LiveKit access-token minting and webhook verification — stdlib only.

LiveKit access tokens are plain JWTs (HS256) with a `video` grant claim, and
webhook events arrive with an Authorization bearer JWT whose `sha256` claim
is the base64-encoded SHA-256 of the raw body. Both directions are
implementable with hmac/hashlib/base64 — the livekit-api SDK stays an
optional convenience, never a hard dependency, so CI runs hermetic.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import time
from typing import Any, Optional


def _b64url(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode()


def _b64url_decode(data: str) -> bytes:
    pad = "=" * (-len(data) % 4)
    return base64.urlsafe_b64decode(data + pad)


def mint_access_token(api_key: str, api_secret: str, *, identity: str, room: str,
                      ttl_minutes: int = 15, name: str = "",
                      can_publish: bool = True, can_subscribe: bool = True) -> str:
    """Mint a LiveKit room token. Identity is namespaced by the caller
    (`client-<id>` for the caller, `concierge-ai` for the bot participant)."""
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload: dict[str, Any] = {
        "iss": api_key,
        "sub": identity,
        "iat": now,
        "nbf": now - 10,
        "exp": now + ttl_minutes * 60,
        "video": {
            "roomJoin": True,
            "room": room,
            "canPublish": can_publish,
            "canSubscribe": can_subscribe,
        },
    }
    if name:
        payload["name"] = name
    signing_input = f"{_b64url(json.dumps(header, separators=(',', ':')).encode())}." \
                    f"{_b64url(json.dumps(payload, separators=(',', ':')).encode())}"
    sig = hmac.new(api_secret.encode(), signing_input.encode(), hashlib.sha256).digest()
    return f"{signing_input}.{_b64url(sig)}"


def verify_token(token: str, api_secret: str, *, check_exp: bool = True) -> Optional[dict]:
    """Verify HS256 signature (constant-time) and expiry. Returns claims or None."""
    try:
        head_b64, pay_b64, sig_b64 = token.split(".")
    except ValueError:
        return None
    signing_input = f"{head_b64}.{pay_b64}"
    expected = hmac.new(api_secret.encode(), signing_input.encode(), hashlib.sha256).digest()
    try:
        actual = _b64url_decode(sig_b64)
    except Exception:
        return None
    if not hmac.compare_digest(expected, actual):
        return None
    try:
        claims = json.loads(_b64url_decode(pay_b64))
    except Exception:
        return None
    if check_exp and int(claims.get("exp", 0)) < int(time.time()):
        return None
    return claims


def sign_webhook_body(api_key: str, api_secret: str, body: bytes) -> str:
    """Mint the webhook Authorization JWT the way LiveKit server does:
    claims carry sha256 = base64(SHA-256(raw body)). Used by tests and by
    any local replay tooling."""
    digest = base64.b64encode(hashlib.sha256(body).digest()).decode()
    now = int(time.time())
    header = {"alg": "HS256", "typ": "JWT"}
    payload = {"iss": api_key, "iat": now, "exp": now + 300, "sha256": digest}
    signing_input = f"{_b64url(json.dumps(header, separators=(',', ':')).encode())}." \
                    f"{_b64url(json.dumps(payload, separators=(',', ':')).encode())}"
    sig = hmac.new(api_secret.encode(), signing_input.encode(), hashlib.sha256).digest()
    return f"{signing_input}.{_b64url(sig)}"


def verify_webhook(auth_header: str, body: bytes, api_secret: str) -> Optional[dict]:
    """Verify a LiveKit webhook: bearer JWT signature + body hash binding.
    Returns the decoded event or None. A valid signature over a tampered
    body must NOT verify — the sha256 claim pins the payload."""
    if not auth_header.startswith("Bearer "):
        return None
    claims = verify_token(auth_header[len("Bearer "):], api_secret)
    if not claims:
        return None
    want = claims.get("sha256", "")
    got = base64.b64encode(hashlib.sha256(body).digest()).decode()
    if not hmac.compare_digest(want.encode(), got.encode()):
        return None
    try:
        return json.loads(body)
    except Exception:
        return None
