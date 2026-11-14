// Phase 32: Catalog page — the service catalog as a first-class console
// surface. Until now a greenfield tenant could only seed services through
// the import JSON on the Setup page; here services are created, priced,
// re-imaged and (de)activated directly. Every write is API-first
// (POST /v1/catalog, PUT /v1/catalog/{id} — PATCH-shaped).
import { useCallback, useEffect, useState } from "react";
import { api, getTenantId } from "../api";
import MediaThumb from "../components/MediaThumb";
import type { CatalogService, Media, Tenant } from "../types";
import { money } from "../types";

// Editable draft of one service. Money fields are MAJOR units in the form,
// converted to minor on save.
type Draft = {
  name: string;
  vertical: string;
  currency: string;
  base: string;
  per_km: string;
  per_hour: string;
  auto_quotable: boolean;
  active: boolean;
  image_id: string;
};

const EMPTY: Draft = {
  name: "", vertical: "ground", currency: "", base: "", per_km: "", per_hour: "",
  auto_quotable: true, active: true, image_id: "",
};

function toDraft(s: CatalogService): Draft {
  return {
    name: s.name, vertical: s.vertical, currency: s.currency,
    base: (s.base_minor / 100).toString(),
    per_km: (s.per_km_minor / 100).toString(),
    per_hour: (s.per_hour_minor / 100).toString(),
    auto_quotable: s.auto_quotable, active: s.active, image_id: s.image_id ?? "",
  };
}

function minor(major: string): number {
  const n = parseFloat(major);
  return Number.isFinite(n) ? Math.round(n * 100) : 0;
}

export default function Catalog() {
  const [services, setServices] = useState<CatalogService[]>([]);
  const [media, setMedia] = useState<Media[]>([]);
  const [currency, setCurrency] = useState("EUR");
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");
  const [create, setCreate] = useState<Draft>(EMPTY);
  const [edits, setEdits] = useState<Record<string, Draft>>({});
  const tenant = getTenantId();

  const load = useCallback(() => {
    if (!tenant) return;
    api.get<CatalogService[]>("/v1/catalog").then(setServices).catch((e) => setErr(String(e)));
    api.get<Media[]>("/v1/media").then(setMedia).catch(() => {});
    api.get<Tenant>("/v1/tenant")
      .then((t) => setCurrency(t.pricing_rules?.currency || "EUR"))
      .catch(() => {});
  }, [tenant]);

  useEffect(load, [load]);

  const add = async () => {
    setErr(""); setMsg("");
    try {
      await api.post("/v1/catalog", {
        vertical: create.vertical.trim(), name: create.name.trim(),
        currency: (create.currency || currency).toUpperCase(),
        base_minor: minor(create.base),
        per_km_minor: minor(create.per_km),
        per_hour_minor: minor(create.per_hour),
        auto_quotable: create.auto_quotable,
        image_id: create.image_id || undefined,
      });
      setCreate(EMPTY);
      setMsg("Service created and live on your site.");
      load();
    } catch (e) { setErr(String(e)); }
  };

  const save = async (id: string) => {
    const d = edits[id];
    if (!d) return;
    setErr(""); setMsg("");
    try {
      await api.put(`/v1/catalog/${id}`, {
        name: d.name.trim(), vertical: d.vertical.trim(),
        currency: d.currency.toUpperCase(),
        base_minor: minor(d.base),
        per_km_minor: minor(d.per_km),
        per_hour_minor: minor(d.per_hour),
        auto_quotable: d.auto_quotable, active: d.active,
        image_id: d.image_id,
      });
      setMsg("Saved.");
      setEdits((prev) => { const n = { ...prev }; delete n[id]; return n; });
      load();
    } catch (e) { setErr(String(e)); }
  };

  const setEdit = (id: string, patch: Partial<Draft>) =>
    setEdits((prev) => ({ ...prev, [id]: { ...prev[id], ...patch } }));

  if (!tenant) return <p className="muted">Set a tenant id on the capture page first.</p>;

  const mediaSelect = (value: string, onChange: (v: string) => void) => (
    <select value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">No image (monogram tile)</option>
      {media.map((m) => (
        <option key={m.id} value={m.id}>
          {m.kind} · {m.id.slice(0, 8)} ({Math.round(m.size_bytes / 1024)} KiB)
        </option>
      ))}
    </select>
  );

  return (
    <div>
      <h1>Catalog</h1>
      <p className="muted">
        The services you sell. Everything here is live on your public site and
        quotable by the concierge agent immediately after saving.
      </p>
      {err && <p className="error">{err}</p>}
      {msg && <p className="ok">{msg}</p>}

      <div className="card">
        <h2>Add a service</h2>
        <div className="row">
          <input placeholder="Name (e.g. Airport Transfer)" value={create.name}
            onChange={(e) => setCreate({ ...create, name: e.target.value })} />
          <input placeholder="Vertical" value={create.vertical} style={{ maxWidth: 120 }}
            onChange={(e) => setCreate({ ...create, vertical: e.target.value })} />
          <input placeholder="CCY" value={create.currency || currency} style={{ maxWidth: 70 }}
            onChange={(e) => setCreate({ ...create, currency: e.target.value })} />
        </div>
        <div className="row">
          <input placeholder="Base price" inputMode="decimal" value={create.base} style={{ maxWidth: 110 }}
            onChange={(e) => setCreate({ ...create, base: e.target.value })} />
          <input placeholder="Per km" inputMode="decimal" value={create.per_km} style={{ maxWidth: 100 }}
            onChange={(e) => setCreate({ ...create, per_km: e.target.value })} />
          <input placeholder="Per hour" inputMode="decimal" value={create.per_hour} style={{ maxWidth: 100 }}
            onChange={(e) => setCreate({ ...create, per_hour: e.target.value })} />
          <label style={{ display: "flex", alignItems: "center", gap: 6 }}>
            <input type="checkbox" checked={create.auto_quotable}
              onChange={(e) => setCreate({ ...create, auto_quotable: e.target.checked })} />
            Auto-quotable
          </label>
        </div>
        <div className="row">
          {mediaSelect(create.image_id, (v) => setCreate({ ...create, image_id: v }))}
          <button onClick={add} disabled={!create.name.trim()}>Create service</button>
        </div>
        {media.length === 0 && (
          <p className="muted">No images yet — upload card imagery in Site Studio → Media library.</p>
        )}
      </div>

      <div className="card">
        <h2>Services ({services.length})</h2>
        {services.length === 0 && (
          <p className="muted">
            No services yet. Add one above, or bulk-import an existing price
            list on the Setup page.
          </p>
        )}
        {services.map((s) => {
          const d = edits[s.id];
          return (
            <div key={s.id} style={{ borderTop: "1px solid #e5e5e5", padding: "10px 0" }}>
              {d ? (
                <>
                  <div className="row">
                    <input value={d.name} onChange={(e) => setEdit(s.id, { name: e.target.value })} />
                    <input value={d.vertical} style={{ maxWidth: 120 }}
                      onChange={(e) => setEdit(s.id, { vertical: e.target.value })} />
                    <input value={d.currency} style={{ maxWidth: 70 }}
                      onChange={(e) => setEdit(s.id, { currency: e.target.value })} />
                  </div>
                  <div className="row">
                    <input value={d.base} inputMode="decimal" style={{ maxWidth: 110 }} title="Base price"
                      onChange={(e) => setEdit(s.id, { base: e.target.value })} />
                    <input value={d.per_km} inputMode="decimal" style={{ maxWidth: 100 }} title="Per km"
                      onChange={(e) => setEdit(s.id, { per_km: e.target.value })} />
                    <input value={d.per_hour} inputMode="decimal" style={{ maxWidth: 100 }} title="Per hour"
                      onChange={(e) => setEdit(s.id, { per_hour: e.target.value })} />
                    <label style={{ display: "flex", alignItems: "center", gap: 6 }}>
                      <input type="checkbox" checked={d.auto_quotable}
                        onChange={(e) => setEdit(s.id, { auto_quotable: e.target.checked })} />
                      Auto-quotable
                    </label>
                    <label style={{ display: "flex", alignItems: "center", gap: 6 }}>
                      <input type="checkbox" checked={d.active}
                        onChange={(e) => setEdit(s.id, { active: e.target.checked })} />
                      Active
                    </label>
                  </div>
                  <div className="row">
                    {mediaSelect(d.image_id, (v) => setEdit(s.id, { image_id: v }))}
                    <button onClick={() => save(s.id)} disabled={!d.name.trim()}>Save</button>
                    <button onClick={() => setEdits((prev) => {
                      const n = { ...prev }; delete n[s.id]; return n;
                    })}>Cancel</button>
                  </div>
                </>
              ) : (
                <div className="row" style={{ alignItems: "center" }}>
                  {s.image_id && <MediaThumb id={s.image_id} />}
                  <div style={{ flex: 1 }}>
                    <strong>{s.name}</strong>{" "}
                    <span className="badge">{s.vertical}</span>{" "}
                    {!s.active && <span className="badge">inactive</span>}{" "}
                    {!s.auto_quotable && <span className="badge">manual quote</span>}
                    <p className="muted" style={{ margin: 0 }}>
                      {money(s.base_minor, s.currency)} base
                      {s.per_km_minor > 0 && ` + ${money(s.per_km_minor, s.currency)}/km`}
                      {s.per_hour_minor > 0 && ` + ${money(s.per_hour_minor, s.currency)}/h`}
                    </p>
                  </div>
                  <button onClick={() => setEdits((prev) => ({ ...prev, [s.id]: toDraft(s) }))}>
                    Edit
                  </button>
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
