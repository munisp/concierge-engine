"""FastAPI surface for the AI concierge: turn ingestion, handoff queue
inspection, traces (the audit trail and the eval dataset).
"""

from __future__ import annotations

from typing import Optional

from fastapi import FastAPI
from pydantic import BaseModel

from .inference import ModelRouter
from .orchestrator import Orchestrator, TurnContext

app = FastAPI(title="concierge-agent", version="0.2.0")
orchestrator = Orchestrator()
router = ModelRouter()


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


# ------------------------------------------------------------- case assist (Phase 4)
# Deterministic contract for core-api's case management: the engine field
# reports which path produced the answer (ollama/qwen or rules) so the audit
# trail and evals stay honest about model provenance.

class TriageRequest(BaseModel):
    text: str


class SummarizeRequest(BaseModel):
    timeline: list[str]


class NextActionRequest(BaseModel):
    case_state: str
    summary: str


class EmbedRequest(BaseModel):
    text: str


class DraftRequest(BaseModel):
    intent: str = "case-reply"
    text: str  # case context (title, summary, recent notes)


@app.post("/v1/ai/embed")
def embed(req: EmbedRequest) -> dict:
    """Semantic vector for case recall. 503 when the daemon is down — the
    caller (core-api) treats that as "semantic memory unavailable"."""
    try:
        vec, engine = router.embed(req.text)
    except Exception as e:
        from fastapi import HTTPException
        raise HTTPException(status_code=503, detail=str(e))
    return {"embedding": vec, "engine": engine}


@app.post("/v1/ai/triage")
def triage(req: TriageRequest) -> dict:
    return router.triage(req.text)


@app.post("/v1/ai/summarize")
def summarize(req: SummarizeRequest) -> dict:
    return router.summarize(req.timeline)


@app.post("/v1/ai/next-action")
def next_action(req: NextActionRequest) -> dict:
    return router.next_action(req.case_state, req.summary)


@app.post("/v1/ai/draft")
def draft(req: DraftRequest) -> dict:
    return router.draft(req.intent, req.text)


@app.get("/v1/ai/health")
def ai_health() -> dict:
    return {
        "ollama": {"base": router.ollama.base, "model": router.ollama.model,
                   "reachable": router.ollama.healthy()},
        "fallback": "deterministic rules (always available)",
    }
