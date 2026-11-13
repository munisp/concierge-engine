"""FastAPI surface for the AI concierge: turn ingestion, handoff queue
inspection, traces (the audit trail and the eval dataset).
"""

from __future__ import annotations

from typing import Optional

from fastapi import FastAPI
from pydantic import BaseModel

from .orchestrator import Orchestrator, TurnContext

app = FastAPI(title="concierge-agent", version="0.1.0")
orchestrator = Orchestrator()


class TurnRequest(BaseModel):
    tenant_id: str
    conversation_id: str
    text: str
    client_id: Optional[str] = None
    client_phone: str = ""
    state: str = "CAPTURING"
    turn: int = 0
    vip_flagged: bool = False


@app.get("/healthz")
def healthz() -> dict:
    return {"status": "ok"}


@app.post("/v1/turns")
async def handle_turn(req: TurnRequest) -> dict:
    ctx = TurnContext(
        tenant_id=req.tenant_id,
        conversation_id=req.conversation_id,
        state=req.state,
        turn=req.turn,
        client_id=req.client_id,
        client_phone=req.client_phone,
        vip_flagged=req.vip_flagged,
    )
    result = await orchestrator.handle_turn(ctx, req.text)
    return {
        "reply": result.reply,
        "state": result.state,
        "escalated": result.escalated,
        "handoff_id": result.handoff_id,
        "actions": result.actions,
    }


@app.get("/v1/traces")
def traces() -> dict:
    # Every tool call: inputs, outputs, latency, policy verdicts (AI spec 3.2).
    return {"traces": orchestrator.tools.traces[-200:]}
