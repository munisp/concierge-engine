"""Interactive-reply tests: taps on outbound list/button options come back
as structured payloads and must enter the same intake pipeline as typed
text, with the option id kept for provenance."""

import asyncio
from unittest.mock import patch


def _run_meta(payload: dict) -> tuple[dict, list]:
    from whatsapp_adapter import main as adapter_main

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
    return out, seen


def _envelope(message: dict) -> dict:
    return {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [message],
    }}]}]}


def test_button_reply_becomes_text_with_option_id():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.b1", "type": "interactive",
        "interactive": {"type": "button_reply",
                        "button_reply": {"id": "confirm-booking", "title": "Confirm"}},
    }))
    assert out["processed"] == 1
    assert seen[0].text == "Confirm [option: confirm-booking]"
    assert seen[0].external_ref == "wamid.b1"  # idempotency discipline applies


def test_list_reply_becomes_text_with_selection():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.l1", "type": "interactive",
        "interactive": {"type": "list_reply",
                        "list_reply": {"id": "veh-s-class", "title": "Mercedes S-Class",
                                       "description": "executive sedan"}},
    }))
    assert out["processed"] == 1
    assert seen[0].text == "Mercedes S-Class [option: veh-s-class]"


def test_legacy_quick_reply_button():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.q1", "type": "button",
        "button": {"text": "Yes, 6am works", "payload": "pickup-6am"},
    }))
    assert out["processed"] == 1
    assert seen[0].text == "Yes, 6am works [option: pickup-6am]"


def test_interactive_without_title_skipped():
    out, seen = _run_meta(_envelope({
        "from": "31612345678", "id": "wamid.b2", "type": "interactive",
        "interactive": {"type": "button_reply", "button_reply": {"id": "x"}},
    }))
    assert out["processed"] == 0
    assert seen == []
