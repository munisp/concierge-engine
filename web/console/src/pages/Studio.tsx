// Phase 30: Site Studio — design + content layer for the public site.
// Presets/theme tokens, business-case content generation (agent or
// template — provenance shown, applied only on explicit click), static
// export for external hosting, and CMS/WordPress integration snippets.
// Phase 32: media library (upload/list/delete, hero assignment, gallery
// membership) and the design layer (layout family + section visibility).
import { useCallback, useEffect, useState } from "react";
import { api, getTenantId } from "../api";
import MediaThumb from "../components/MediaThumb";
import type { Media, Tenant } from "../types";

type Preset = {
  name: string; description: string;
  primary: string; accent: string; font: string; corners: string;
};
type Draft = {
  locale: string; tagline: string; description: string;
  service_descs: Record<string, string>; engine: string;
};
type Integrations = { shapes: Record<string, Record<string, string>> };

// Keep in sync with domain.SiteDesigns / domain.SiteSections (core-api).
const DESIGN_FAMILIES: Record<string, string> = {
  classic: "Card grid — the default premium layout",
  editorial: "Editorial — oversized serif display, asymmetric hero, alternating rows",
};
const SECTIONS: [string, string][] = [
  ["trust", "Trust chips"], ["stats", "Stats band"], ["services", "Services"],
  ["gallery", "Gallery strip"], ["steps", "How it works"], ["platform", "Platform band"],
  ["reviews", "Reviews"], ["areas", "Service areas"], ["cta", "Call to action"],
];
const GALLERY_MAX = 8;

type SiteConfig = NonNullable<Tenant["site_config"]>;

export default function Studio() {
  const [presets, setPresets] = useState<Preset[]>([]);
  const [theme, setTheme] = useState("");
  const [saved, setSaved] = useState("");
  const [err, setErr] = useState("");
  const [biz, setBiz] = useState("");
  const [locale, setLocale] = useState("en");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [integrations, setIntegrations] = useState<Integrations | null>(null);
  const [media, setMedia] = useState<Media[]>([]);
  const [site, setSite] = useState<SiteConfig | null>(null);
  const [uploading, setUploading] = useState(false);
  const tenant = getTenantId();

  const load = useCallback(() => {
    if (!tenant) return;
    api.get<{ presets: Preset[] }>("/v1/studio/presets").then((r) => setPresets(r.presets)).catch(() => {});
    api.get<Integrations>("/v1/studio/integrations").then(setIntegrations).catch(() => {});
    api.get<Media[]>("/v1/media").then(setMedia).catch(() => {});
    api.get<Tenant>("/v1/tenant")
      .then((t) => setSite(t.site_config ?? {}))
      .catch(() => {});
  }, [tenant]);

  useEffect(load, [load]);

  const applyPreset = async (p: Preset) => {
    setErr("");
    try {
      await api.put("/v1/studio/theme", {
        theme: { preset: p.name },
      });
      setSaved(`Theme "${p.name}" is live on your public site.`);
      load();
    } catch (e) { setErr(String(e)); }
  };

  const generate = async () => {
    setErr(""); setDraft(null);
    try {
      setDraft(await api.post<Draft>("/v1/studio/generate", { business_case: biz, locale }));
    } catch (e) { setErr(String(e)); }
  };

  const applyDraft = async () => {
    if (!draft) return;
    setErr("");
    try {
      await api.post("/v1/studio/apply", draft);
      setSaved(`Content applied (${draft.locale}, engine: ${draft.engine}).`);
      setDraft(null);
    } catch (e) { setErr(String(e)); }
  };

  // --- Phase 32: media library -------------------------------------------
  // Uploads are JSON base64 (same envelope as the API); 8 MB decoded cap
  // is enforced server-side.
  const upload = async (file: File, kind: string) => {
    setErr(""); setUploading(true);
    try {
      const buf = await file.arrayBuffer();
      let bin = "";
      new Uint8Array(buf).forEach((b) => { bin += String.fromCharCode(b); });
      await api.post("/v1/media", {
        kind, content_type: file.type, data_base64: btoa(bin),
      });
      setSaved(`Uploaded ${file.name}.`);
      load();
    } catch (e) { setErr(String(e)); } finally { setUploading(false); }
  };

  const removeMedia = async (id: string) => {
    setErr("");
    try {
      await api.del(`/v1/media/${id}`);
      setSaved("Image deleted.");
      // If the deleted image was referenced, clear the references too so the
      // next save doesn't fight the server's dangling-id validation.
      if (site) {
        const next: SiteConfig = {
          ...site,
          hero_image_id: site.hero_image_id === id ? "" : site.hero_image_id,
          gallery_ids: (site.gallery_ids ?? []).filter((g) => g !== id),
        };
        setSite(next);
        await saveSite(next, true);
      }
      load();
    } catch (e) { setErr(String(e)); }
  };

  // --- Phase 32: design layer --------------------------------------------
  // site_config is patched WHOLESALE server-side, so every save carries the
  // full object (theme, brand_i18n, locales…) back — never a partial.
  const saveSite = async (next: SiteConfig, silent = false) => {
    setErr("");
    try {
      await api.put("/v1/tenant", { site_config: next });
      if (!silent) setSaved("Design saved — live on your public site.");
    } catch (e) { setErr(String(e)); }
  };

  const patchSite = (patch: Partial<SiteConfig>) => {
    if (!site) return;
    const next = { ...site, ...patch };
    setSite(next);
    void saveSite(next);
  };

  const toggleSection = (id: string) => {
    if (!site) return;
    const hidden = new Set(site.hidden_sections ?? []);
    if (hidden.has(id)) { hidden.delete(id); } else { hidden.add(id); }
    patchSite({ hidden_sections: [...hidden] });
  };

  const toggleGallery = (id: string) => {
    if (!site) return;
    const cur = site.gallery_ids ?? [];
    if (cur.includes(id)) {
      patchSite({ gallery_ids: cur.filter((g) => g !== id) });
    } else if (cur.length < GALLERY_MAX) {
      patchSite({ gallery_ids: [...cur, id] });
    } else {
      setErr(`Gallery holds at most ${GALLERY_MAX} images — remove one first.`);
    }
  };

  if (!tenant) return <p className="muted">Set a tenant ID first (Settings).</p>;

  return (
    <div>
      <h1>Site Studio</h1>
      <p className="muted">
        Design and content for your public booking site. Changes go live
        immediately at your site URL; nothing here affects booking logic.
      </p>
      {err && <p className="error">{err}</p>}
      {saved && <p className="ok">{saved}</p>}

      <div className="card">
        <h2>Theme presets</h2>
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill,minmax(220px,1fr))", gap: 12 }}>
          {presets.map((p) => (
            <div key={p.name} style={{ border: "1px solid #ddd", borderRadius: 8, overflow: "hidden" }}>
              <div style={{ background: p.primary, color: p.accent, padding: "18px 14px",
                fontFamily: p.font === "serif" ? "Georgia,serif" : "system-ui,sans-serif" }}>
                Aa — {p.name}
              </div>
              <div style={{ padding: 10 }}>
                <p className="muted" style={{ margin: "0 0 8px", fontSize: 13 }}>{p.description}</p>
                <button onClick={() => applyPreset(p)}>Apply theme</button>
              </div>
            </div>
          ))}
        </div>
        <p className="muted" style={{ marginTop: 8 }}>
          Custom tokens (exact brand colors, font, corners) via the API:
          PUT /v1/studio/theme with preset "custom" + hex overrides.
        </p>
        <label>Active selection (reference)</label>
        <input value={theme} onChange={(e) => setTheme(e.target.value)} placeholder="custom overrides via API" />
      </div>

      <div className="card">
        <h2>Generate content from your business case</h2>
        <p className="muted">
          Describe the business in one paragraph — the concierge agent drafts
          tagline, site description and per-service copy from your real
          catalog (it never invents services or prices). The draft shows its
          engine; nothing publishes until you apply it.
        </p>
        <label>Business case</label>
        <textarea rows={4} style={{ width: "100%" }} value={biz}
          onChange={(e) => setBiz(e.target.value)}
          placeholder="Luxury airport transfers and private day tours in Lisbon for international hotel guests…" />
        <label>Locale</label>
        <input value={locale} onChange={(e) => setLocale(e.target.value)} placeholder="en" />
        <p /><button onClick={generate} disabled={!biz.trim()}>Generate draft</button>
        {draft && (
          <div style={{ marginTop: 12 }}>
            <p><strong>Engine:</strong> <code>{draft.engine}</code> (labeled provenance — template = deterministic fallback, agent = concierge-agent)</p>
            <p><strong>Tagline:</strong> {draft.tagline}</p>
            <p><strong>Description:</strong> {draft.description}</p>
            <p><strong>Service copy:</strong> {Object.keys(draft.service_descs).length} services drafted</p>
            <button onClick={applyDraft}>Apply draft to site</button>
          </div>
        )}
      </div>

      <div className="card">
        <h2>Layout &amp; sections</h2>
        <p className="muted">
          The layout family restructures the whole page; the theme above only
          retints it. Changes apply on save and are visible immediately.
        </p>
        {Object.entries(DESIGN_FAMILIES).map(([id, desc]) => (
          <label key={id} style={{ display: "block", margin: "6px 0" }}>
            <input type="radio" name="design" style={{ marginRight: 8 }}
              checked={(site?.design || "classic") === id}
              onChange={() => patchSite({ design: id === "classic" ? "" : id })} />
            <strong>{id}</strong> — <span className="muted">{desc}</span>
          </label>
        ))}
        <h3 style={{ margin: "14px 0 4px" }}>Sections</h3>
        <p className="muted">Turn landing sections on or off. Ordering stays fixed so the page narrative always makes sense.</p>
        <div style={{ display: "flex", flexWrap: "wrap", gap: 14 }}>
          {SECTIONS.map(([id, label]) => (
            <label key={id} style={{ display: "flex", alignItems: "center", gap: 6 }}>
              <input type="checkbox"
                checked={!(site?.hidden_sections ?? []).includes(id)}
                onChange={() => toggleSection(id)} />
              {label}
            </label>
          ))}
        </div>
      </div>

      <div className="card">
        <h2>Media library</h2>
        <p className="muted">
          Site imagery — hero, service cards, gallery. JPEG/PNG/WebP up to
          8 MB. Images are served publicly on your site URL.
        </p>
        <div className="row">
          <input type="file" accept="image/jpeg,image/png,image/webp" disabled={uploading}
            onChange={(e) => {
              const f = e.target.files?.[0];
              if (f) void upload(f, "misc");
              e.target.value = "";
            }} />
        </div>
        {media.length === 0 && <p className="muted">No images uploaded yet.</p>}
        <div style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill,minmax(180px,1fr))", gap: 12, marginTop: 10 }}>
          {media.map((m) => {
            const isHero = site?.hero_image_id === m.id;
            const inGallery = (site?.gallery_ids ?? []).includes(m.id);
            return (
              <div key={m.id} style={{ border: "1px solid #ddd", borderRadius: 8, padding: 8 }}>
                <MediaThumb id={m.id} width={164} height={92} />
                <p className="muted" style={{ margin: "6px 0", fontSize: 12 }}>
                  {m.id.slice(0, 8)} · {Math.round(m.size_bytes / 1024)} KiB
                  {isHero && <> · <strong>hero</strong></>}
                  {inGallery && <> · <strong>gallery</strong></>}
                </p>
                <div style={{ display: "flex", gap: 6, flexWrap: "wrap" }}>
                  <button disabled={isHero} onClick={() => patchSite({ hero_image_id: m.id })}>
                    {isHero ? "Hero ✓" : "Set as hero"}
                  </button>
                  <button onClick={() => toggleGallery(m.id)}>
                    {inGallery ? "Remove from gallery" : "Add to gallery"}
                  </button>
                  <button onClick={() => void removeMedia(m.id)}>Delete</button>
                </div>
              </div>
            );
          })}
        </div>
        <p className="muted" style={{ marginTop: 8 }}>
          Gallery: {(site?.gallery_ids ?? []).length}/{GALLERY_MAX} images. Service
          card images are attached on the Catalog page.
        </p>
      </div>

      <div className="card">
        <h2>Deploy &amp; integrate</h2>
        <p>
          <a className="btn" href="/v1/studio/export"
            onClick={(e) => {
              // tenant header needed — fetch manually as blob
              e.preventDefault();
              fetch("/v1/studio/export", { headers: { "X-Tenant-ID": tenant } })
                .then((r) => r.blob()).then((b) => {
                  const a = document.createElement("a");
                  a.href = URL.createObjectURL(b); a.download = "site-export.zip"; a.click();
                });
            }}>Download static export (zip)</a>
        </p>
        {integrations && Object.entries(integrations.shapes).map(([k, v]) => (
          <div key={k} style={{ marginBottom: 14 }}>
            <h3 style={{ margin: "8px 0 4px" }}>{k.replace(/_/g, " ")}</h3>
            <p className="muted" style={{ margin: 0 }}>{v.description}</p>
            {v.iframe && <pre style={{ whiteSpace: "pre-wrap", fontSize: 12, background: "#f5f5f5", padding: 8 }}>{v.iframe}</pre>}
            {v.plugin_php && <p className="muted" style={{ fontSize: 12 }}>WordPress: save the plugin as {v.plugin_file}, activate, use <code>{v.shortcode}</code>. Plugin source via GET /v1/studio/integrations.</p>}
          </div>
        ))}
      </div>
    </div>
  );
}
