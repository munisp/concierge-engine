import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Phase 36: lead engine — pipeline board, capture form and conversion
// actions for person / corporate / partner / event leads. Nurture itself
// lives in the outreach engine; this board is the operator's pipeline view.

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

const KINDS = ["person", "corporate", "partner", "event"];
const STATUSES = ["NEW", "CONTACTED", "QUALIFIED", "CONVERTED", "LOST"];
const NEXT: Record<string, string[]> = {
  NEW: ["CONTACTED", "QUALIFIED", "LOST"],
  CONTACTED: ["QUALIFIED", "LOST"],
  QUALIFIED: ["LOST"],
};

export default function Leads() {
  const [leads, setLeads] = useState<Lead[]>([]);
  const [pipeline, setPipeline] = useState<Pipeline | null>(null);
  const [kindFilter, setKindFilter] = useState("");
  const [statusFilter, setStatusFilter] = useState("");
  const [error, setError] = useState("");

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

      <div className="row" style={{ gap: 8 }}>
        <select value={kindFilter} onChange={(e) => setKindFilter(e.target.value)}>
          <option value="">all kinds</option>
          {KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        <select value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)}>
          <option value="">all statuses</option>
          {STATUSES.map((s) => <option key={s} value={s}>{s}</option>)}
        </select>
        <button onClick={() => void load()}>Refresh</button>
      </div>

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
              <td>
                {(NEXT[l.status] ?? []).map((to) => (
                  <button key={to} onClick={() => void transition(l, to)} style={{ marginRight: 4 }}>
                    {to === "LOST" ? "Lose" : to.charAt(0) + to.slice(1).toLowerCase()}
                  </button>
                ))}
                {(l.status === "QUALIFIED" || l.status === "CONTACTED" || l.status === "NEW") && (
                  <button onClick={() => void convert(l)}>Convert</button>
                )}
              </td>
            </tr>
          ))}
          {leads.length === 0 && (
            <tr><td colSpan={8}>No leads yet — capture one above or post to /site/{"{slug}"}/leads.</td></tr>
          )}
        </tbody>
      </table>
    </section>
  );
}
