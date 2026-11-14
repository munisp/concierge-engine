// Phase 30: Site Studio — design + content layer for the public site.
// Presets/theme tokens, business-case content generation (agent or
// template — provenance shown, applied only on explicit click), static
// export for external hosting, and CMS/WordPress integration snippets.
import { useEffect, useState } from "react";
import { api, getTenantId } from "../api";

type Preset = {
  name: string; description: string;
  primary: string; accent: string; font: string; corners: string;
};
type Draft = {
  locale: string; tagline: string; description: string;
  service_descs: Record<string, string>; engine: string;
};
type Integrations = { shapes: Record<string, Record<string, string>> };

export default function Studio() {
  const [presets, setPresets] = useState<Preset[]>([]);
  const [theme, setTheme] = useState("");
  const [saved, setSaved] = useState("");
  const [err, setErr] = useState("");
  const [biz, setBiz] = useState("");
  const [locale, setLocale] = useState("en");
  const [draft, setDraft] = useState<Draft | null>(null);
  const [integrations, setIntegrations] = useState<Integrations | null>(null);
  const tenant = getTenantId();

  useEffect(() => {
    if (!tenant) return;
    api.get<{ presets: Preset[] }>("/v1/studio/presets").then((r) => setPresets(r.presets)).catch(() => {});
    api.get<Integrations>("/v1/studio/integrations").then(setIntegrations).catch(() => {});
  }, [tenant]);

  const applyPreset = async (p: Preset) => {
    setErr("");
    try {
      await api.put("/v1/studio/theme", {
        theme: { preset: p.name },
      });
      setSaved(`Theme "${p.name}" is live on your public site.`);
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
