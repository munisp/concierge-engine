"""SMTP delivery + MIME construction.

The compliance envelope is split by responsibility: core-api puts the
human-visible unsubscribe footer in the body (recorded, auditable); the
adapter adds the machine headers — List-Unsubscribe (RFC 2369) and
List-Unsubscribe-Post (RFC 8058 one-click) — plus optional open/click
tracking when a public base URL is configured.
"""

from __future__ import annotations

import re
import smtplib
import uuid
from dataclasses import dataclass, field
from email.message import EmailMessage
from email.utils import formataddr

from . import tokens

LINK_RE = re.compile(r"https?://[^\s<>\"')]+")


@dataclass
class SMTPConfig:
    host: str = ""
    port: int = 587
    user: str = ""
    password: str = ""
    sender: str = ""          # From address; defaults to user
    sender_name: str = ""
    tls: str = "starttls"     # starttls | ssl | none

    @property
    def configured(self) -> bool:
        return bool(self.host)


@dataclass
class SandboxOutbox:
    """Sandbox mode: sends are recorded, inspectable, never faked as live."""
    sent: list[dict] = field(default_factory=list)


sandbox = SandboxOutbox()


def _domain_of(address: str) -> str:
    return address.split("@")[-1] if "@" in address else "localhost"


def build_message(cfg: SMTPConfig, *, to: str, subject: str, body: str,
                  unsubscribe_url: str = "",
                  tracking_base: str = "", tracking_secret: str = "",
                  tenant_id: str = "", client_id: str = "",
                  send_ref: str = "") -> tuple[EmailMessage, str]:
    """Build the MIME message; returns (message, message_id)."""
    sender = cfg.sender or cfg.user
    message_id = f"<{uuid.uuid4().hex}@{_domain_of(sender)}>"

    text = body
    if tracking_base and tracking_secret and send_ref:
        pixel = tokens.mint(tracking_secret, "o", tenant_id, client_id, send_ref)
        text += f"\n[open-tracking-pixel: {tracking_base}/t/o/{pixel}]"
        def _wrap(m: re.Match) -> str:
            url = m.group(0)
            if url.startswith(tracking_base):  # don't re-wrap our own links
                return url
            tok = tokens.mint(tracking_secret, "c", tenant_id, client_id, send_ref)
            return f"{tracking_base}/t/c/{tok}?u={url}"
        text = LINK_RE.sub(_wrap, text)

    msg = EmailMessage()
    msg["From"] = formataddr((cfg.sender_name, sender)) if cfg.sender_name else sender
    msg["To"] = to
    msg["Subject"] = subject
    msg["Message-ID"] = message_id
    if unsubscribe_url:
        msg["List-Unsubscribe"] = f"<{unsubscribe_url}>"
        msg["List-Unsubscribe-Post"] = "List-Unsubscribe=One-Click"
    msg.set_content(text)
    return msg, message_id


def send(cfg: SMTPConfig, msg: EmailMessage, to: str) -> None:
    """Deliver via SMTP. Raises on failure — the caller records the send as
    failed with the cause; it never swallows."""
    if cfg.tls == "ssl":
        smtp: smtplib.SMTP = smtplib.SMTP_SSL(cfg.host, cfg.port, timeout=15)
    else:
        smtp = smtplib.SMTP(cfg.host, cfg.port, timeout=15)
    with smtp:
        if cfg.tls == "starttls":
            smtp.starttls()
        if cfg.user:
            smtp.login(cfg.user, cfg.password)
        smtp.send_message(msg, to_addrs=[to])
