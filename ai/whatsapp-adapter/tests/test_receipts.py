"""Receipts + contacts tests: delivery/read status callbacks reach core-api
as SLA evidence (advisory — never blocking), Infobip groupName mapping,
and contact-card intake on both channels."""

import asyncio
from unittest.mock import patch

from whatsapp_adapter import main as adapter_main


def _meta_req(payload: dict):
    class _Req:
        async def body(self):
            import json as _json
            return _json.dumps(payload).encode()

        headers = {"x-hub-signature-256": "sha256=ok"}
    return _Req()


def _status_envelope(statuses: list) -> dict:
    return {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "statuses": statuses,
    }}]}]}


def _run_meta(payload: dict, receipt_posts: list):
    async def fake_inbound(msg):
        return {"ok": True}

    with patch.object(adapter_main, "META_APP_SECRET", "s"), \
            patch("whatsapp_adapter.meta.verify_meta_signature", return_value=True), \
            patch.object(adapter_main, "inbound", fake_inbound), \
            patch.object(adapter_main, "_record_receipt",
                         lambda *a: receipt_posts.append(a) or asyncio.sleep(0)), \
            patch.dict("os.environ", {"CE_META_TENANT_MAP": "pn-1:t1"}):
        return asyncio.run(adapter_main.meta_inbound(_meta_req(payload)))


def test_meta_statuses_become_receipts():
    posts = []
    out = _run_meta(_status_envelope([
        {"id": "wamid.reply-1", "status": "delivered", "timestamp": "1763125200"},
        {"id": "wamid.reply-1", "status": "read", "timestamp": "1763125260"},
        {"id": "wamid.reply-1", "status": "accepted"},   # not our vocabulary
        {"status": "read"},                              # no id: skip
    ]), posts)
    assert out["processed"] == 2
    assert posts[0] == ("t1", "whatsapp", "wamid.reply-1", "delivered", "1763125200")
    assert posts[1][3] == "read"


def test_meta_statuses_and_messages_together():
    posts = []
    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "statuses": [{"id": "wamid.reply-1", "status": "read"}],
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.t1", "type": "text",
                      "text": {"body": "thanks"}}],
    }}]}]}
    with patch.object(adapter_main, "META_APP_SECRET", "s"), \
            patch("whatsapp_adapter.meta.verify_meta_signature", return_value=True), \
            patch.object(adapter_main, "inbound", fake_inbound), \
            patch.object(adapter_main, "_record_receipt",
                         lambda *a: posts.append(a) or asyncio.sleep(0)), \
            patch.dict("os.environ", {"CE_META_TENANT_MAP": "pn-1:t1"}):
        out = asyncio.run(adapter_main.meta_inbound(_meta_req(payload)))
    assert out["processed"] == 2
    assert seen[0].text == "thanks"
    assert posts[0][2] == "wamid.reply-1"


def test_meta_contacts_folded_into_text():
    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    payload = {"entry": [{"changes": [{"value": {
        "metadata": {"phone_number_id": "pn-1"},
        "contacts": [{"wa_id": "31612345678", "profile": {"name": "Ada"}}],
        "messages": [{"from": "31612345678", "id": "wamid.c1", "type": "contacts",
                      "contacts": [{"name": {"formatted_name": "Musa Driver"},
                                    "phones": [{"phone": "+971501234567"}]}]}],
    }}]}]}
    with patch.object(adapter_main, "META_APP_SECRET", "s"), \
            patch("whatsapp_adapter.meta.verify_meta_signature", return_value=True), \
            patch.object(adapter_main, "inbound", fake_inbound), \
            patch.dict("os.environ", {"CE_META_TENANT_MAP": "pn-1:t1"}):
        out = asyncio.run(adapter_main.meta_inbound(_meta_req(payload)))
    assert out["processed"] == 1
    assert seen[0].text == "[contact shared: Musa Driver — +971501234567]"
    assert seen[0].external_ref == "wamid.c1"


def test_infobip_status_group_mapping():
    posts = []

    class _Req:
        async def json(self):
            return {"results": [
                {"to": "31687654321", "messageId": "ib-9",
                 "status": {"groupName": "DELIVERED"}},
                {"to": "31687654321", "messageId": "ib-9",
                 "status": {"groupName": "SEEN"}},
                {"to": "31687654321", "messageId": "ib-8",
                 "status": {"groupName": "UNDELIVERABLE"}},
                {"to": "31687654321", "messageId": "ib-7",
                 "status": {"groupName": "SOME_FUTURE_GROUP"}},
            ]}

    with patch.object(adapter_main, "_record_receipt",
                      lambda *a: posts.append(a) or asyncio.sleep(0)), \
            patch.dict("os.environ", {"CE_INFOBIP_DEFAULT_TENANT": "t1"}):
        out = asyncio.run(adapter_main.infobip_status(_Req()))
    assert out["processed"] == 3  # unknown group skipped honestly
    statuses = [p[3] for p in posts]
    assert statuses == ["delivered", "read", "failed"]


def test_infobip_contact_normalized():
    seen = []

    async def fake_inbound(msg):
        seen.append(msg)
        return {"ok": True}

    class _Req:
        async def json(self):
            return {"results": [{
                "from": "31612345678", "to": "31687654321", "messageId": "ib-c1",
                "message": {"type": "CONTACT",
                            "contacts": [{"name": {"formatted_name": "Musa Driver"},
                                          "phones": [{"phone": "+971501234567"}]}]},
            }]}

    with patch("whatsapp_adapter.main.inbound", fake_inbound), \
            patch.dict("os.environ", {"CE_INFOBIP_DEFAULT_TENANT": "t1"}):
        out = asyncio.run(adapter_main.infobip_inbound(_Req()))
    assert out["processed"] == 1
    assert seen[0].text == "[contact shared: Musa Driver — +971501234567]"
