"""Voice adapter HTTP surface.

    GET  /health                     config + pipeline readiness (verbatim truth)
    POST /v1/voice/sessions          open a voice session → room + join token
    GET  /v1/voice/sessions/{id}     session state (tenant-scoped)
    DELETE /v1/voice/sessions/{id}   hang up (operator or client app)
    POST /webhooks/livekit           LiveKit lifecycle events (verified)

Session creation is refused (503) unless the channel is enabled AND LiveKit
credentials exist AND the media pipeline can be assembled — the refusal body
lists exactly what is missing. Identity resolution failures are 502 with the
cause; a voice session without a client anchor is never created.
"""

from __future__ import annotations

from fastapi import Depends, FastAPI, Header, HTTPException, Request
from pydantic import BaseModel

from . import config, pipeline
from .bridge import BridgeError, CoreBridge
from .sessions import ENDED, SessionStore
from .tokens import mint_access_token, verify_webhook
from .webhooks import end_session, handle_event

app = FastAPI(title="concierge-engine voice-adapter", version="0.1.0")
_store = SessionStore()


def get_store() -> SessionStore:
    return _store


def get_bridge() -> CoreBridge:
    return CoreBridge()


@app.get("/health")
def health():
    st = get_store()
    return {
        "ok": True,
        "service": "voice-adapter",
        "config": config.readiness(),
        "pipeline": pipeline.pipeline_readiness(),
        "active_sessions": len(st.active()),
    }


class CreateSession(BaseModel):
    phone: str
    display_name: str = ""


@app.post("/v1/voice/sessions", status_code=201)
async def create_session(body: CreateSession,
                         x_tenant_id: str = Header(...),
                         store: SessionStore = Depends(get_store),
                         bridge: CoreBridge = Depends(get_bridge)):
    ready = pipeline.pipeline_readiness()
    if not ready["ready"]:
        raise HTTPException(503, detail={
            "error": "voice channel unavailable", "missing": ready["missing"]})

    try:
        client_id = await bridge.resolve_client(x_tenant_id, body.phone,
                                                body.display_name)
    except BridgeError as e:
        raise HTTPException(502, detail={"error": str(e)})

    # The session id participates in both the room name (no collision when a
    # client has two concurrent calls) and the conversation external_ref
    # (voice:{session_id}), so create the session first, then open the
    # thread (get-or-created — a returning caller continues their VOICE
    # thread per Phase 22).
    session = store.create(x_tenant_id, client_id, body.phone, "", "")
    session.room = f"{config.ROOM_PREFIX}-{x_tenant_id[:8]}-{client_id[:8]}-{session.id[:6]}"
    try:
        conv = await bridge.open_conversation(x_tenant_id, client_id, session.id)
    except BridgeError as e:
        raise HTTPException(502, detail={"error": str(e)})
    session.conversation_id = conv["id"]

    token = mint_access_token(config.LIVEKIT_API_KEY, config.LIVEKIT_API_SECRET,
                              identity=f"client-{session.client_id}",
                              room=session.room,
                              ttl_minutes=config.TOKEN_TTL_MINUTES,
                              name=body.display_name)
    return {"session": session.to_dict(), "livekit_url": config.LIVEKIT_URL,
            "room": session.room, "token": token,
            "token_ttl_minutes": config.TOKEN_TTL_MINUTES}


@app.get("/v1/voice/sessions/{session_id}")
def get_session(session_id: str, x_tenant_id: str = Header(...),
                store: SessionStore = Depends(get_store)):
    s = store.get_for_tenant(x_tenant_id, session_id)
    if s is None:
        raise HTTPException(404, detail={"error": "session not found"})
    return {"session": s.to_dict()}


@app.delete("/v1/voice/sessions/{session_id}")
async def hangup(session_id: str, x_tenant_id: str = Header(...),
                 store: SessionStore = Depends(get_store),
                 bridge: CoreBridge = Depends(get_bridge)):
    s = store.get_for_tenant(x_tenant_id, session_id)
    if s is None:
        raise HTTPException(404, detail={"error": "session not found"})
    await end_session(store, bridge, s, "hangup")
    return {"session": s.to_dict()}


@app.get("/v1/voice/sessions")
def list_sessions(x_tenant_id: str = Header(...), active: bool = False,
                  store: SessionStore = Depends(get_store)):
    """Operator board: the tenant's sessions, optionally only live ones."""
    if active:
        return {"sessions": [s.to_dict() for s in store.active(tenant_id=x_tenant_id)]}
    return {"sessions": [s.to_dict() for s in store.all_for_tenant(x_tenant_id)]}


class Takeover(BaseModel):
    staff_name: str


@app.post("/v1/voice/sessions/{session_id}/takeover")
async def takeover(session_id: str, body: Takeover,
                   x_tenant_id: str = Header(...),
                   store: SessionStore = Depends(get_store),
                   bridge: CoreBridge = Depends(get_bridge)):
    """A human concierge joins the live room. The client hears the agent
    hand over; the timeline records the takeover as provenance, and the
    staff participant joins with the same token contract as everyone
    else — no privileged media path."""
    s = store.get_for_tenant(x_tenant_id, session_id)
    if s is None:
        raise HTTPException(404, detail={"error": "session not found"})
    if s.state == ENDED or s.state == "FAILED":
        raise HTTPException(409, detail={
            "error": f"session already {s.state.lower()}",
            "end_reason": s.end_reason})
    ready = config.readiness()
    if not ready["livekit_configured"]:
        raise HTTPException(503, detail={
            "error": "livekit not configured", "missing": ready["missing"]})
    if not body.staff_name.strip():
        raise HTTPException(422, detail={"error": "staff_name required"})

    await bridge.post_message(s.tenant_id, s.conversation_id, "SYSTEM",
                              f"[human concierge joining: {body.staff_name.strip()}]")
    slug = "".join(ch for ch in body.staff_name.lower() if ch.isalnum())[:24] or "staff"
    token = mint_access_token(config.LIVEKIT_API_KEY, config.LIVEKIT_API_SECRET,
                              identity=f"staff-{slug}", room=s.room,
                              ttl_minutes=config.MAX_SESSION_MINUTES,
                              name=body.staff_name.strip())
    return {"session": s.to_dict(), "livekit_url": config.LIVEKIT_URL,
            "room": s.room, "token": token, "identity": f"staff-{slug}"}


@app.post("/webhooks/livekit")
async def livekit_webhook(request: Request,
                          store: SessionStore = Depends(get_store),
                          bridge: CoreBridge = Depends(get_bridge)):
    body = await request.body()
    auth = request.headers.get("authorization", "")
    event = verify_webhook(auth, body, config.LIVEKIT_API_SECRET)
    if event is None:
        raise HTTPException(401, detail={"error": "webhook verification failed"})
    return await handle_event(store, bridge, event)
