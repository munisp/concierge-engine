#!/usr/bin/env python3
"""SEO foundation (Phase 1 module M1): static, crawlable landing pages per
tenant x vertical. The React console is an app shell; acquisition traffic
lands on these server-renderable pages, each deep-linking into the capture
flow and the click-to-WhatsApp entry.

Usage: python scripts/build_seo_pages.py [tenant-slug ...]
Output: web/console/public/seo/<slug>/<vertical>.html + sitemap.xml + robots.txt
"""

from __future__ import annotations

import html
import json
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
OUT = ROOT / "web" / "console" / "public" / "seo"

VERTICALS = {
    "chauffeur": ("Chauffeur Services", "Business and leisure chauffeur, point-to-point and hourly, vetted drivers and late-model fleet."),
    "meet-and-greet": ("Airport Meet & Greet", "VIP meet & greet, fast-track immigration, lounge and connections handled end to end."),
    "helicopter": ("Helicopter Transfers", "City-to-city and airport helicopter transfers with slot coordination."),
    "private-jet": ("Private Jet Charter", "On-demand charter across vetted operators; transparent quotes in minutes."),
    "security": ("Close Protection", "Licensed executive protection and secure ground transport."),
    "events": ("Event Access", "Curated access to sold-out events, hospitality and after-hours experiences."),
    "yacht": ("Yacht Charter", "Day charters and weekly charters with crew, catering and itinerary design."),
}

TENANTS = {
    "taylor-travel": {
        "name": "Taylor Travel Management",
        "markets": ["Amsterdam", "Dubai", "London", "Paris", "Munich", "Milan"],
        "phone": "+31201234567",
    },
}

TEMPLATE = """<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1" />
<title>{title} | {tenant}</title>
<meta name="description" content="{description}" />
<link rel="canonical" href="/seo/{slug}/{vertical}.html" />
<meta property="og:title" content="{title} — {tenant}" />
<meta property="og:description" content="{description}" />
<script type="application/ld+json">{jsonld}</script>
<style>
body {{ font-family: Georgia, serif; margin: 0; color: #1a1a1a; background: #faf9f7; }}
main {{ max-width: 720px; margin: 0 auto; padding: 64px 24px; }}
h1 {{ font-size: 2.4rem; font-weight: normal; }}
.cta {{ display: inline-block; margin-top: 24px; padding: 14px 28px; background: #1a1a1a;
  color: #faf9f7; text-decoration: none; letter-spacing: .02em; }}
.muted {{ color: #6b6b6b; }}
</style>
</head>
<body><main>
<p class="muted">{tenant} · {markets}</p>
<h1>{title}</h1>
<p>{description}</p>
<p>One request, one price, one timeline — confirmed by your concierge on WhatsApp or web.</p>
<a class="cta" href="/capture?vertical={vertical}">Request a quote</a>
<a class="cta" style="background:#075E54" href="https://wa.me/{wa_number}?text=I%20need%20{vertical}">WhatsApp us</a>
<p class="muted" style="margin-top:48px">Response within one hour, around the clock.</p>
</main></body></html>
"""


def build(slugs: list[str]) -> None:
    urls: list[str] = []
    for slug in slugs:
        tenant = TENANTS[slug]
        for vertical, (title, description) in VERTICALS.items():
            page_dir = OUT / slug
            page_dir.mkdir(parents=True, exist_ok=True)
            jsonld = json.dumps({
                "@context": "https://schema.org",
                "@type": "Service",
                "name": f"{title} — {tenant['name']}",
                "description": description,
                "areaServed": tenant["markets"],
                "provider": {"@type": "Organization", "name": tenant["name"]},
            })
            page = TEMPLATE.format(
                title=html.escape(title), tenant=html.escape(tenant["name"]),
                description=html.escape(description), slug=slug, vertical=vertical,
                markets=html.escape(" · ".join(tenant["markets"])),
                wa_number=tenant["phone"].lstrip("+"), jsonld=jsonld,
            )
            (page_dir / f"{vertical}.html").write_text(page)
            urls.append(f"/seo/{slug}/{vertical}.html")
    (OUT / "sitemap.xml").write_text(
        '<?xml version="1.0" encoding="UTF-8"?>\n'
        '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
        + "".join(f"  <url><loc>{{BASE}}{u}</loc></url>\n" for u in urls)
        + "</urlset>\n"
    )
    (OUT / "robots.txt").write_text("User-agent: *\nAllow: /\nSitemap: {BASE}/seo/sitemap.xml\n")
    print(f"built {len(urls)} pages for {len(slugs)} tenant(s) -> {OUT}")


if __name__ == "__main__":
    build(sys.argv[1:] or list(TENANTS))
