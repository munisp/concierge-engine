import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Phase 36: lead engine — pipeline board, capture form and conversion
// actions for person / corporate / partner / event leads.
// Phase 37: board (kanban) view, and bulk import for greenfield tenants
// bootstrapping from a spreadsheet/CRM export (POST /v1/leads/import).

type Lead = {
  id: string;
  kind: string;
  name: string;
  phone?: string;
  email?: string;
  company?: string;
  event_type?: string;
  event_date?: string;
  headcount?: number;
  venue?: string;
  verticals?: string[];
  source: string;
  channel?: string;
  campaign?: string;
  status: string;
  score: number;
  score_reasons?: string[];
  lost_reason?: string;
  consent_marketing: boolean;
  converted_client_id?: string;
  converted_partner_id?: string;
  converted_account_id?: string;
  created_at: string;
};

type Pipeline = {
  total: number;
  open: number;
  by_status: Record<string, number>;
  by_kind: Record<string, number>;
};

type ImportReceipt = {
  total: number;
  created: number;
  deduped: number;
  rejected: number;
  errors?: { index: number; name?: string; reason: string }[];
};

const KINDS = ["person", "corporate", "partner", "event"];
const STATUSES = ["NEW", "CONTACTED", "QUALIFIED", "CONVERTED", "LOST"];
const NEXT: Record<string, string[]> = {
  NEW: ["CONTACTED", "QUALIFIED", "LOST"],
  CONTACTED: ["QUALIFIED", "LOST"],
  QUALIFIED: ["LOST"],
};

// CSV columns for the import panel (header row required):
// kind,name,phone,email,company,event_type,event_date,headcount,venue,
// verticals(; separated),channel,campaign,source,consent_marketing,referral_code
function parseImport(text: string): Record<string, unknown>[] {
  const trimmed = text.trim();
  if (!trimmed) return [];
  if (trimmed.startsWith("[")) {
    const rows = JSON.parse(trimmed) as Record<string, unknown>[];
    return rows.map((r) => ({ source: "import", ...r }));
  }
  const lines = trimmed.split(/\r?\n/).filter((l) => l.trim());
  const header = lines[0].split(",").map((h) => h.trim().toLowerCase());
  return lines.slice(1).map((line) => {
    const cells = line.split(",").map((c) => c.trim());
    const row: Record<string, unknown> = { source: "import" };
    header.forEach((h, i) => {
      const v = cells[i] ?? "";
      if (v === "") return;
      switch (h) {
        case "headcount": row.headcount = Number(v); break;
        case "consent_marketing": row.consent_marketing = /^(true|yes|1)$/i.test(v); break;
        case "verticals": row.verticals = v.split(";").map((x) => x.trim()).filter(Boolean); break;
        case "event_date": row.event_date = new Date(v).toISOString(); break;
        default: row[h] = v;
      }
    });
    return row;
  });
}

export default function Leads() {
  const [leads, setLeads] = useState<Lead[]>([]);
  const [pipeline, setPipeline] = useState<Pipeline | null>(null);
  const [kindFilter, setKindFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [error, setError] = useState("");
  const [view, setView] = useState<"table" | "board">("board");

  // capture form
  const [kind, setKind] = useState("person");
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [email, setEmail] = useState("");
  const [company, setCompany] = useState("");
  const [eventType, setEventType] = useState("");
  const [eventDate, setEventDate] = useState("");
  const [headcount, setHeadcount] = useState("");
  const [venue, setVenue] = useState("");
  const [verticals, setVerticals] = useState("");
  const [consent, setConsent] = useState(false);

  // bulk import (Phase 37)
  const [importText, setImportText] = useState("");
  const [importReceipt, setImportReceipt] = useState<ImportReceipt | null>(null);
  const [importing, setImporting] = useState(false);

  const load = useCallback(async () => {
    try {
      const q = new URLSearchParams();
      if (kindFilter) q.set("kind", kindFilter);
      if (statusFilter) q.set("status", statusFilter);
      const suffix = q.toString() ? `?${q.toString()}` : "";
      const [list, pipe] = await Promise.all([
        api.get<{ leads: Lead[] }>(`/v1/leads${suffix}`),
        api.get<Pipeline>("/v1/leads/pipeline"),
      ]);
      setLeads(list.leads ?? []);
      setPipeline(pipe);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, [kindFilter, statusFilter]);

  useEffect(() => {
    void load();
  }, [load]);

  async function capture() {
    const body: Record<string, unknown> = {
      kind,
      name,
      phone,
      email,
      source: "api",
      consent_marketing: consent,
    };
    if (company) body.company = company;
    if (kind === "event") {
      if (eventType) body.event_type = eventType;
      if (eventDate) body.event_date = new Date(eventDate).toISOString();
      if (headcount) body.headcount = Number(headcount);
      if (venue) body.venue = venue;
    }
    if (kind === "partner" && verticals) {
      body.verticals = verticals.split(",").map((v) => v.trim()).filter(Boolean);
    }
    try {
      await api.post("/v1/leads", body);
      setName(""); setPhone(""); setEmail(""); setCompany("");
      setEventType(""); setEventDate(""); setHeadcount(""); setVenue(""); setVerticals("");
      setConsent(false);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "capture failed");
    }
  }

  async function runImport() {
    setImporting(true);
    setImportReceipt(null);
    try {
      const rows = parseImport(importText);
      if (rows.length === 0) {
        setError("nothing to import — paste CSV with a header row or a JSON array");
        return;
      }
      const receipt = await api.post<ImportReceipt>("/v1/leads/import", { leads: rows });
      setImportReceipt(receipt);
      setImportText("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "import failed");
    } finally {
      setImporting(false);
    }
  }

  async function transition(lead: Lead, to: string) {
    const body: Record<string, unknown> = { to };
    if (to === "LOST") {
      const reason = window.prompt("Lost reason (required):");
      if (!reason) return;
      body.lost_reason = reason;
    }
    await api.post(`/v1/leads/${lead.id}/transition`, body);
    await load();
  }

  async function convert(lead: Lead) {
    await api.post(`/v1/leads/${lead.id}/convert`, {});
    await load();
  }

  function leadActions(l: Lead) {
    return (
      <>
        {(NEXT[l.status] ?? []).map((to) => (
          <button key={to} className="secondary" onClick={() => void transition(l, to)}>
            {to === "LOST" ? "Lose" : to.charAt(0) + to.slice(1).toLowerCase()}
          </button>
        ))}
        {["NEW", "CONTACTED", "QUALIFIED"].includes(l.status) && (
          <button onClick={() => void convert(l)}>Convert</button>
        )}
      </>
    );
  }

  return (
    <section>
      <h2>Leads</h2>
      {error && <p className="error">{error}</p>}

      {pipeline && (
        <div className="row" style={{ gap: 12, flexWrap: "wrap" }}>
          <div className="card"><strong>{pipeline.open}</strong> open / {pipeline.total} total</div>
          {STATUSES.map((s) => (
            <div className="card" key={s}>{s}: {pipeline.by_status[s] ?? 0}</div>
          ))}
          {KINDS.map((k) => (
            <div className="card" key={k}>{k}: {pipeline.by_kind[k] ?? 0}</div>
          ))}
        </div>
      )}

      <div className="card">
        <h3>Capture lead</h3>
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <select value={kind} onChange={(e) => setKind(e.target.value)}>
            {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
          </select>
          <input placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <input placeholder="Phone" value={phone} onChange={(e) => setPhone(e.target.value)} />
          <input placeholder="Email" value={email} onChange={(e) => setEmail(e.target.value)} />
          {(kind === "corporate" || kind === "partner") && (
            <input placeholder="Company" value={company} onChange={(e) => setCompany(e.target.value)} />
          )}
          {kind === "event" && (
            <>
              <input placeholder="Event type" value={eventType} onChange={(e) => setEventType(e.target.value)} />
              <input type="date" value={eventDate} onChange={(e) => setEventDate(e.target.value)} />
              <input placeholder="Headcount" value={headcount} onChange={(e) => setHeadcount(e.target.value)} style={{ width: 90 }} />
              <input placeholder="Venue" value={venue} onChange={(e) => setVenue(e.target.value)} />
            </>
          )}
          {kind === "partner" && (
            <input placeholder="Verticals (comma-separated)" value={verticals} onChange={(e) => setVerticals(e.target.value)} />
          )}
          <label>
            <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} /> marketing consent
          </label>
          <button onClick={() => void capture()} disabled={!name || (!phone && !email)}>Capture</button>
        </div>
      </div>

      <div className="card">
        <h3>Bulk import <span className="muted">— bootstrap from a spreadsheet or CRM export</span></h3>
        <p className="muted" style={{ marginTop: 0 }}>
          Paste CSV (header row: kind,name,phone,email,company,event_type,event_date,headcount,venue,verticals;…,channel,campaign,consent_marketing)
          or a JSON array. Rows run through the same dedup, scoring and consent gates as live capture;
          bad rows are reported, not fatal. Re-importing a keyed export is replay-safe.
        </p>
        <textarea
          rows={5}
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          placeholder={"kind,name,phone,email,company,channel,consent_marketing\nperson,Ada Lovelace,+351961234567,,,google,true\ncorporate,James Whitfield,+447700900123,james@apex.example,Apex Racing Hospitality,paid_social,true"}
        />
        <div style={{ marginTop: 8 }}>
          <button onClick={() => void runImport()} disabled={importing || !importText.trim()}>
            {importing ? "Importing…" : "Import leads"}
          </button>
        </div>
        {importReceipt && (
          <p className="notice" style={{ marginTop: 8 }}>
            Imported {importReceipt.created} new · {importReceipt.deduped} merged/duplicate · {importReceipt.rejected} rejected
            of {importReceipt.total}.
            {(importReceipt.errors ?? []).length > 0 && (
              <ul style={{ margin: "0.4rem 0 0" }}>
                {(importReceipt.errors ?? []).slice(0, 10).map((e) => (
                  <li key={e.index}>row {e.index + 1}{e.name ? ` (${e.name})` : ""}: {e.reason}</li>
                ))}
              </ul>
            )}
          </p>
        )}
      </div>

      <div className="row" style={{ gap: 8, alignItems: "center" }}>
        <select value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
          <option value="">all kinds</option>
          {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="">all statuses</option>
          {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <div style={{ display: "flex", gap: 4 }}>
          <button className={view === "board" ? "" : "secondary"} onClick={() => setView("board")}>Board</button>
          <button className={view === "table" ? "" : "secondary"} onClick={() => setView("table")}>Table</button>
        </div>
        <button className="secondary" onClick={() => void load()}>Refresh</button>
      </div>

      {view === "board" ? (
        <div className="kanban">
          {STATUSES.map((status) => {
            const col = leads.filter((l) => l.status === status);
            return (
              <div className="kanban-col" key={status}>
                <div className="kanban-head">{status} <span className="muted">{col.length}</span></div>
                {col.map((l) => (
                  <div className="lead-card" key={l.id}>
                    <div className="lead-card-top">
                      <strong>{l.name}</strong>
                      <span className="lead-score" title={(l.score_reasons ?? []).join("\n")}>{l.score}</span>
                    </div>
                    <div className="muted">
                      {l.kind}{l.company ? ` · ${l.company}` : ""}
                      {l.event_type ? ` · ${l.event_type}${l.headcount ? ` (${l.headcount})` : ""}` : ""}
                    </div>
                    <div className="muted">{l.source}{l.channel ? `/${l.channel}` : ""} · {new Date(l.created_at).toLocaleDateString()}</div>
                    {l.lost_reason && <div className="error">LOST — {l.lost_reason}</div>}
                    {(l.converted_account_id || l.converted_partner_id || l.converted_client_id) && (
                      <div className="muted">converted ✓</div>
                    )}
                    <div className="lead-actions">{leadActions(l)}</div>
                  </div>
                ))}
                {col.length === 0 && <div className="muted" style={{ padding: 4 }}>—</div>}
              </div>
            );
          })}
        </div>
      ) : (
        <table>
          <thead>
            <tr>
              <th>Lead</th><th>Kind</th><th>Score</th><th>Status</th><th>Source</th><th>Contact</th><th>Captured</th><th></th>
            </tr>
          </thead>
          <tbody>
            {leads.map((l) => (
              <tr key={l.id}>
                <td>
                  {l.name}
                  {l.company ? ` · ${l.company}` : ""}
                  {l.event_type ? ` · ${l.event_type}${l.headcount ? ` (${l.headcount})` : ""}` : ""}
                </td>
                <td>{l.kind}</td>
                <td title={(l.score_reasons ?? []).join("\n")}>{l.score}</td>
                <td>{l.status}{l.lost_reason ? ` — ${l.lost_reason}` : ""}</td>
                <td>{l.source}{l.channel ? `/${l.channel}` : ""}</td>
                <td>{l.phone || l.email}</td>
                <td>{new Date(l.created_at).toLocaleDateString()}</td>
                <td className="lead-actions">{leadActions(l)}</td>
              </tr>
            ))}
            {leads.length === 0 && (
              <tr><td colSpan={8}>No leads yet — capture one above, bulk-import a list, or post to /site/{"{slug}"}/leads.</td></tr>
            )}
          </tbody>
        </table>
      )}
    </section>
  );
}
