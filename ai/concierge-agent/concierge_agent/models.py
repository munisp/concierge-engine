from __future__ import annotations

from typing import Any, Optional

from pydantic import BaseModel, Field


class PolicyVerdict(BaseModel):
    verdict_id: str
    tier: int
    verdict: str
    reasons: list[str] = Field(default_factory=list)


class ToolResult(BaseModel):
    ok: bool
    data: dict[str, Any] = Field(default_factory=dict)
    error: Optional[str] = None
    verdict: Optional[PolicyVerdict] = None
