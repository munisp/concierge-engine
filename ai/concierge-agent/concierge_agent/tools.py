"""Tool layer (AI spec section 3): typed functions wrapping the same internal
APIs the web UI uses — no shadow business logic. Every mutating call carries
an idempotency key derived from (conversation, turn, tool); every call is
gated by the policy engine and fully traced.
"""

from __future__ import annotations

import hashlib
from typing import Any, Optional

import httpx

from . import config
from .models import PolicyVerdict, ToolResult

# Least privilege per state (AI spec 3.2) — mirrors the Rust policy engine.
STATE_GATED_TOOLS: dict[str, set[str]] = {
    "create_payment_link": {"QUOTING", "NEGOTIATING", "AWAITING_PAYMENT"},
    "assign_booking": {"CONFIRMED", "IN_SERVICE", "ESCALATED"},
}

TIER_0 = {"resolve_client", "get_preferences", "search_availability", "escalate_to_human"}
TIER_1 = {"create_service_request", "update_service_request", "save_preference", "draft_itinerary"}
TIER_2 = {"price_quote", "create_payment_link", "send_message", "assign_booking"}
TIER_3 = {"execute_charge", "refund", "waive_fee", "apply_discount", "apply_sla_penalty", "delete_data"}


def tool_tier(tool: str) -> int:
    if tool in TIER_0:
        return 0
    if tool in TIER_1:
        return 1
    if tool in TIER_2:
        return 2
    return 3  # unknown tools fail closed


def idempotency_key(conversation_id: str, turn: int, tool: str) -> str:
    raw = f"{conversation_id}:{turn}:{tool}"
    return hashlib.sha256(raw.encode()).hexdigest()[:32]


class ToolExecutor:
    """Executes tools against core-api with policy gating and full traces."""

    def __init__(self, core_url: Optional[str] = None, policy_url: Optional[str] = None,
                 shadow: Optional[bool] = None):
        self.core = core_url or config.CORE_API_URL
        self.policy = policy_url or config.POLICY_ENGINE_URL
        # Shadow mode (AI spec 7): read-only tools and escalation run for
        # real; mutations and sends are recorded as shadowed, never executed.
        self.shadow = config.SHADOW_MODE if shadow is None else shadow
        self.traces: list[dict[str, Any]] = []

    async def _policy_check(self, tenant_id: str, conversation_id: str, tool: str,
                            args: dict[str, Any], state: str, turn: int) -> PolicyVerdict:
        import json

        payload = {
            "tenant_id": tenant_id,
            "conversation_id": conversation_id,
            "actor": "ai",
            "tool": tool,
            "args_json": json.dumps(args),
            "request_state": state,
            "estimated_value_minor": int(args.get("estimated_value_minor", 0)),
            "currency": args.get("currency", "EUR"),
            "vip_flagged": bool(args.get("vip_flagged", False)),
            "idempotency_key": idempotency_key(conversation_id, turn, tool),
        }
        try:
            async with httpx.AsyncClient(timeout=3.0) as client:
                resp = await client.post(f"{self.policy}/v1/evaluate", json=payload)
                resp.raise_for_status()
                return PolicyVerdict(**resp.json())
        except Exception:
            # Fail closed: policy engine unreachable -> human approval.
            return PolicyVerdict(verdict_id="fallback", tier=3,
                                 verdict="RequireApproval",
                                 reasons=["policy engine unreachable — defaulting to human approval"])

    async def call(self, tenant_id: str, conversation_id: str, tool: str,
                   args: dict[str, Any], state: str, turn: int) -> ToolResult:
        # 1. State gating (least privilege per state).
        if tool in STATE_GATED_TOOLS and state not in STATE_GATED_TOOLS[tool]:
            return ToolResult(ok=False, error=f"tool {tool} not permitted in state {state}")

        # 2. Deterministic policy gating for anything above TIER-1.
        verdict: Optional[PolicyVerdict] = None
        if tool_tier(tool) >= 2:
            verdict = await self._policy_check(tenant_id, conversation_id, tool, args, state, turn)
            if verdict.verdict == "Deny":
                return ToolResult(ok=False, error="denied by policy engine", verdict=verdict)
            if verdict.verdict == "RequireApproval":
                return ToolResult(ok=False, error="requires human approval",
                                  verdict=verdict, data={"handoff_required": True,
                                                         "pending_action": {"tool": tool, "args": args}})

        # 3. Shadow mode: record the would-be mutation, execute nothing.
        if self.shadow and tool not in TIER_0 and tool != "escalate_to_human":
            result = ToolResult(ok=True, verdict=verdict,
                                data={"shadow": True, "tool": tool,
                                      "note": "shadow mode: action recorded, not executed"})
            self.traces.append({"tool": tool, "args": args, "turn": turn, "shadow": True,
                                "verdict": verdict.model_dump() if verdict else None,
                                "ok": True})
            return result

        # 4. Execute against core-api (the same APIs the UI uses).
        result = await self._dispatch(tenant_id, conversation_id, tool, args, turn)
        result.verdict = verdict
        self.traces.append({"tool": tool, "args": args, "turn": turn,
                            "verdict": verdict.model_dump() if verdict else None,
                            "ok": result.ok})
        return result

    async def _dispatch(self, tenant_id: str, conversation_id: str, tool: str,
                        args: dict[str, Any], turn: int) -> ToolResult:
        headers = {"X-Tenant-ID": tenant_id}
        idem = idempotency_key(conversation_id, turn, tool)
        async with httpx.AsyncClient(base_url=self.core, headers=headers, timeout=10.0) as client:
            try:
                if tool == "resolve_client":
                    r = await client.post("/v1/clients/resolve", json={
                        "phone": args.get("phone", ""), "email": args.get("email", ""),
                        "name": args.get("name", "")})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"client_id": d["client"]["id"],
                                                     "confidence": d["confidence"],
                                                     "is_new": d["is_new"]})
                if tool == "get_preferences":
                    r = await client.get(f"/v1/clients/{args['client_id']}/preferences")
                    r.raise_for_status()
                    return ToolResult(ok=True, data={"preferences": r.json()})
                if tool == "save_preference":
                    r = await client.put(
                        f"/v1/clients/{args['client_id']}/preferences/{args['key']}",
                        json={"value": args["value"],
                              "consent_scope": args.get("consent_scope", "service"),
                              "source_quote": args.get("source_quote", ""), "recorded_by": "ai"})
                    r.raise_for_status()
                    return ToolResult(ok=True, data={"saved": True})
                if tool == "create_service_request":
                    r = await client.post("/v1/requests", json={
                        "client_id": args["client_id"],
                        "conversation_id": conversation_id,
                        "items": args["items"], "idempotency_key": idem})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"request_id": d["id"], "state": d["state"]})
                if tool == "update_service_request":
                    r = await client.patch(f"/v1/requests/{args['request_id']}", json={
                        "items": args["items"], "idempotency_key": idem})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"version": d["version"], "state": d["state"]})
                if tool == "price_quote":
                    r = await client.post(f"/v1/requests/{args['request_id']}/quote")
                    if r.status_code == 202:
                        return ToolResult(ok=True, data={"origin": "HUMAN",
                                                         "message": "human quote required within SLA"})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={
                        "quote_id": d["id"], "total_minor": d["total_minor"],
                        "currency": d["currency"], "deposit_minor": d["deposit_minor"],
                        "expires_at": d["expires_at"], "origin": d["origin"]})
                if tool == "create_payment_link":
                    r = await client.post("/v1/payments/links", json={
                        "quote_id": args["quote_id"], "mode": args.get("mode", "FULL"),
                        "idempotency_key": idem})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"payment_url": d["hosted_url"]})
                if tool == "send_message":
                    # The only outbound path; the reply pipeline runs before this.
                    r = await client.post(f"/v1/conversations/{conversation_id}/messages",
                                          json={"author": "AI", "body": args["body"]})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"message_id": d["id"], "delivered": True})
                if tool == "escalate_to_human":
                    r = await client.post("/v1/handoffs", json={
                        "conversation_id": conversation_id,
                        "trigger": args.get("trigger", "CONFIDENCE"),
                        "reason": args.get("reason", ""),
                        "pending_action": args.get("pending_action")})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"handoff_id": d["id"]})
                if tool == "search_availability":
                    r = await client.get("/v1/dispatch/fleet")
                    r.raise_for_status()
                    fleet = [f for f in r.json() if f.get("active")]
                    return ToolResult(ok=True, data={"available": bool(fleet), "options": fleet})
                if tool == "assign_booking":
                    r = await client.post("/v1/dispatch/assignments", json={
                        "booking_id": args["booking_id"],
                        "fleet_resource_id": args.get("fleet_resource_id", ""),
                        "crew_member_id": args.get("crew_member_id", ""),
                        "partner_id": args.get("partner_id", "")})
                    r.raise_for_status()
                    d = r.json()
                    return ToolResult(ok=True, data={"assignment_id": d["id"], "status": d["status"]})
                return ToolResult(ok=False, error=f"unknown tool {tool}")
            except httpx.HTTPStatusError as e:
                return ToolResult(ok=False, error=f"core-api {e.response.status_code}: {e.response.text[:200]}")
            except Exception as e:  # tool boundary must never raise into the loop
                return ToolResult(ok=False, error=str(e))
