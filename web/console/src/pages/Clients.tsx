import { useEffect, useState } from "react";
import { api } from "../api";

// Phase 15 CRM console: client search, profile enrichment, consent
// management, the 360 view, and duplicate merge.

type Client = {
  id: string;
  full_name?: string;
  email?: string;
  phone?: string;
  locale?: string;
  timezone?: string;
  vip_flags?: string[];
  loyalty_tier?: string;
  kyc_status: string;
  consent: Record<string, unknown>;
  erased_at?: string;
  created_at: string;
};

type View360 = {
  client: Client;
  referral_code: string;
  preferences: { key: string; value: string; consent_scope: string }[];
  requests: { id: string; state: string; created_at: string }[];
  bookings: { id: string; state: string; created_at: string }[];
  cases: { id: string; title: string; priority: string; state: string; created_at: string }[];
  conversations: { id: string; channel: string; state: string; created_at: string }[];
  first_touch?: { kind: string; channel?: string; campaign?: string };
  recent_outreach?: { id: string; purpose: string; status: string; created_at: string }[];
  spend_minor: number;
};

const CONSENT_SCOPES = ["marketing", "service", "data_processing"];

// Phase 44: one row of the consent evidence ledger (GDPR Art. 7 proof).
type ConsentEvent = {
  id: string;
  scope: string;
  granted: boolean;
  source: string;
  detail?: string;
  created_at: string;
};

export default function Clients() {
  const [q, setQ] = useState("");
  const [results, setResults] = useState<Client[]>([]);
  const [view, setView] = useState<View360 | null>(null);
  const [err, setErr] = useState("");
  const [msg, setMsg] = useState("");

  // profile editor
  const [fName, setFName] = useState("");
  const [fEmail, setFEmail] = useState("");
  const [fLocale, setFLocale] = useState("");
  const [fTz, setFTz] = useState("");

  // merge tool
  const [mergeLoser, setMergeLoser] = useState("");

  // Phase 44: consent evidence timeline
  const [consentHistory, setConsentHistory] = useState<ConsentEvent[]>([]);

  const search = async () => {
    setErr(""); setResults([]); setView(null);
    try {
      setResults(await api.get<Client[]>(`/v1/clients?q=${encodeURIComponent(q)}`));
    } catch (e) { setErr(String(e)); }
  };

  const open360 = async (id: string) => {
    setErr(""); setMsg("");
    try {
      const v = await api.get<View360>(`/v1/clients/${id}/360`);
      setView(v);
      setFName(v.client.full_name ?? ""); setFEmail(v.client.email ?? "");
      setFLocale(v.client.locale ?? ""); setFTz(v.client.timezone ?? "");
      // Phase 44: evidence timeline is best-effort — never blocks the 360.
      api.get<ConsentEvent[]>(`/v1/clients/${id}/consent-history`)
        .then(setConsentHistory)
        .catch(() => setConsentHistory([]));
    } catch (e) { setErr(String(e)); }
  };

  const saveProfile = async () => {
    if (!view) return;
    setErr(""); setMsg("");
    try {
      await api.patch(`/v1/clients/${view.client.id}`, {
        full_name: fName || undefined, email: fEmail || undefined,
        locale: fLocale || undefined, timezone: fTz || undefined,
      });
      setMsg("profile saved");
      open360(view.client.id);
    } catch (e) { setErr(String(e)); }
  };

  const setConsent = async (scope: string, granted: boolean) => {
    if (!view) return;
    setErr("");
    try {
      await api.put(`/v1/clients/${view.client.id}/consent`, { scope, granted });
      open360(view.client.id);
    } catch (e) { setErr(String(e)); }
  };

  // Phase 45: copy the client's self-service consent-center link (also
  // rides in every marketing email footer — this is for pasting into a
  // direct conversation).
  const copyConsentLink = async () => {
    if (!view) return;
    setErr(""); setMsg("");
    try {
      const r = await api.get<{ url: string }>(`/v1/clients/${view.client.id}/consent-link`);
      await navigator.clipboard.writeText(r.url);
      setMsg("consent link copied");
    } catch (e) { setErr(String(e)); }
  };

  // Phase 16 (GDPR/CCPA): data-subject rights — access export downloads one
  // JSON document; erasure scrubs identity to a pseudonymous tombstone while
  // financial history is retained for accounting.
  const exportData = async () => {
    if (!view) return;
    setErr("");
    try {
      const data = await api.get<unknown>(`/v1/clients/${view.client.id}/export`);
      const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
      const a = document.createElement("a");
      a.href = URL.createObjectURL(blob);
      a.download = `client-export-${view.client.id.slice(0, 8)}.json`;
      a.click();
      URL.revokeObjectURL(a.href);
    } catch (e) { setErr(String(e)); }
  };

  const erase = async () => {
    if (!view) return;
    if (!window.confirm(
      "Erase this client? Identity is scrubbed permanently (GDPR Art. 17 / CCPA). " +
      "Booking and payment history is kept anonymized for accounting. This cannot be undone.")) return;
    setErr(""); setMsg("");
    try {
      await api.post(`/v1/clients/${view.client.id}/erase`, {});
      setMsg("client erased — identity scrubbed, financial history retained anonymized");
      open360(view.client.id);
    } catch (e) { setErr(String(e)); }
  };

  const merge = async () => {
    if (!view || !mergeLoser) return;
    setErr(""); setMsg("");
    try {
      await api.post("/v1/clients/merge", { winner_id: view.client.id, loser_id: mergeLoser });
      setMsg(`merged ${mergeLoser.slice(0, 8)} into this client`);
      setMergeLoser("");
      open360(view.client.id);
    } catch (e) { setErr(String(e)); }
  };

  return (
    <div>
      <h1>Clients</h1>
      {err && <p className="error">{err}</p>}
      {msg && <p className="ok">{msg}</p>}

      <div className="card">
        <h2>Search</h2>
        <div className="row">
          <input placeholder="name, email or phone" value={q} onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => e.key === "Enter" && search()} />
          <button onClick={search}>Search</button>
        </div>
        {results.length > 0 && (
          <table>
            <thead><tr><th>Name</th><th>Phone</th><th>Email</th><th>Tier</th><th></th></tr></thead>
            <tbody>
              {results.map((c) => (
                <tr key={c.id}>
                  <td>{c.full_name || "—"}</td><td>{c.phone || "—"}</td><td>{c.email || "—"}</td>
                  <td>{c.loyalty_tier || "—"}</td>
                  <td><button onClick={() => open360(c.id)}>360</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      {view && (
        <>
          <div className="card">
            <h2>{view.client.full_name || view.client.id.slice(0, 8)} — 360°</h2>
            <p className="muted">
              {view.client.phone || "no phone"} · {view.client.email || "no email"} ·{" "}
              {view.client.locale || "no locale"} · {view.client.timezone || "no tz"} ·{" "}
              referral <strong>{view.referral_code}</strong> · spend{" "}
              <strong>{(view.spend_minor / 100).toFixed(2)}</strong>
            </p>
            {view.first_touch && (
              <p className="muted">
                First touch: {view.first_touch.kind}
                {view.first_touch.channel && ` via ${view.first_touch.channel}`}
                {view.first_touch.campaign && ` (${view.first_touch.campaign})`}
              </p>
            )}

            {view.client.erased_at && (
              <p className="error">
                Erased {new Date(view.client.erased_at).toLocaleDateString()} — GDPR/CCPA
                tombstone. No further processing or contact permitted.
              </p>
            )}

            <h3>Data rights (GDPR / CCPA)</h3>
            <div className="row">
              <button onClick={exportData}>Export all data (JSON)</button>
              {!view.client.erased_at && (
                <button className="danger" onClick={erase}>Erase client</button>
              )}
            </div>

            <h3>Profile</h3>
            <div className="row">
              <input placeholder="full name" value={fName} onChange={(e) => setFName(e.target.value)} />
              <input placeholder="email" value={fEmail} onChange={(e) => setFEmail(e.target.value)} />
            </div>
            <div className="row">
              <input placeholder="locale (en-NG)" value={fLocale} onChange={(e) => setFLocale(e.target.value)} />
              <input placeholder="timezone (Africa/Lagos)" value={fTz} onChange={(e) => setFTz(e.target.value)} />
              <button onClick={saveProfile}>Save profile</button>
            </div>

            <h3>Consent</h3>
            <div className="row">
              {CONSENT_SCOPES.map((s) => {
                const granted = view.client.consent?.[s] === true;
                return (
                  <button key={s} className={granted ? "" : "muted"}
                    onClick={() => setConsent(s, !granted)}>
                    {s}: {granted ? "granted" : "not granted"}
                  </button>
                );
              })}
              <button className="muted" onClick={copyConsentLink}>Copy client self-service link</button>
            </div>
            {consentHistory.length > 0 && (
              <table><tbody>
                {consentHistory.map((e) => (
                  <tr key={e.id}>
                    <td>{new Date(e.created_at).toLocaleString()}</td>
                    <td>{e.scope}</td>
                    <td>{e.granted ? "granted" : "revoked"}</td>
                    <td className="muted">{e.source}{e.detail ? ` — ${e.detail}` : ""}</td>
                  </tr>
                ))}
              </tbody></table>
            )}

            <h3>Merge duplicate</h3>
            <div className="row">
              <input placeholder="duplicate client id (loser)" value={mergeLoser}
                onChange={(e) => setMergeLoser(e.target.value)} />
              <button onClick={merge}>Merge into this client</button>
            </div>
          </div>

          <div className="card">
            <h2>Commercial history</h2>
            <h3>Requests ({view.requests.length})</h3>
            {view.requests.length > 0 && (
              <table><tbody>
                {view.requests.map((r) => (
                  <tr key={r.id}><td>{r.id.slice(0, 8)}</td><td>{r.state}</td>
                    <td>{new Date(r.created_at).toLocaleDateString()}</td></tr>
                ))}
              </tbody></table>
            )}
            <h3>Bookings ({view.bookings.length})</h3>
            {view.bookings.length > 0 && (
              <table><tbody>
                {view.bookings.map((b) => (
                  <tr key={b.id}><td>{b.id.slice(0, 8)}</td><td>{b.state}</td>
                    <td>{new Date(b.created_at).toLocaleDateString()}</td></tr>
                ))}
              </tbody></table>
            )}
          </div>

          <div className="card">
            <h2>Service &amp; channel history</h2>
            <h3>Cases ({view.cases.length})</h3>
            {view.cases.length > 0 && (
              <table><tbody>
                {view.cases.map((c) => (
                  <tr key={c.id}><td>{c.priority}</td><td>{c.title}</td><td>{c.state}</td></tr>
                ))}
              </tbody></table>
            )}
            <h3>Conversations ({view.conversations.length})</h3>
            {view.conversations.length > 0 && (
              <table><tbody>
                {view.conversations.map((c) => (
                  <tr key={c.id}><td>{c.channel}</td><td>{c.state}</td>
                    <td>{new Date(c.created_at).toLocaleDateString()}</td></tr>
                ))}
              </tbody></table>
            )}
            <RideProfileCard clientID={view.client.id} onSaved={() => open360(view.client.id)} />
            <ChauffeurLinksCard clientID={view.client.id} />
            <JourneyCard clientID={view.client.id} />
            <h3>Preferences ({view.preferences.length})</h3>
            {view.preferences.length > 0 && (
              <table><tbody>
                {view.preferences.map((p, i) => (
                  <tr key={i}><td>{p.key}</td><td>{p.value}</td><td>{p.consent_scope}</td></tr>
                ))}
              </tbody></table>
            )}
            <h3>Recent outreach ({view.recent_outreach?.length ?? 0})</h3>
            {(view.recent_outreach ?? []).length > 0 && (
              <table><tbody>
                {view.recent_outreach!.map((o) => (
                  <tr key={o.id}><td>{o.purpose}</td><td>{o.status}</td>
                    <td>{new Date(o.created_at).toLocaleDateString()}</td></tr>
                ))}
              </tbody></table>
            )}
          </div>
        </>
      )}
    </div>
  );
}

// Phase 58: curated ride-profile quick-set. Values are validated
// server-side against the curated vocabulary (422 on an invalid enum).
const RIDE_QUICK_SET: { key: string; label: string; options: string[] }[] = [
  { key: "ride.music", label: "Music", options: [] }, // free text
  { key: "ride.temperature_c", label: "Temperature °C", options: [] }, // 16–26
  { key: "ride.conversation", label: "Conversation", options: ["quiet", "light", "chatty"] },
  { key: "ride.route", label: "Route", options: ["fastest", "scenic", "no_tolls"] },
  { key: "ride.child_seat", label: "Child seat", options: ["none", "infant", "toddler", "booster"] },
  { key: "ride.luggage", label: "Luggage", options: ["light", "standard", "heavy"] },
  { key: "access.mobility", label: "Mobility", options: ["none", "assistance", "wheelchair"] },
  { key: "dining.dietary", label: "Dietary", options: ["none", "vegetarian", "vegan", "halal", "kosher", "gluten_free"] },
  { key: "dining.allergens", label: "Allergens", options: [] }, // free text
  { key: "comms.language", label: "Language", options: [] }, // BCP-47
];

function RideProfileCard({ clientID, onSaved }: { clientID: string; onSaved: () => Promise<void> | void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [msg, setMsg] = useState("");
  const [err, setErr] = useState("");

  // Phase 60: one-tap mode — server expands the preset bundle.
  async function applyMode(mode: string) {
    try {
      await api.post(`/v1/clients/${clientID}/ride-mode`, { mode });
      setMsg(`${mode} mode applied`);
      setErr("");
      await onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : String(e));
    }
  }

  async function save(key: string, value: string) {
    try {
      await api.put(`/v1/clients/${clientID}/preferences/${key}`, {
        value, consent_scope: "service", recorded_by: "console",
      });
      setValues({ ...values, [key]: value });
      setMsg("Saved");
      setErr("");
      setTimeout(() => setMsg(""), 2000);
      await onSaved();
    } catch (e) {
      setErr(e instanceof Error ? e.message : "save failed");
    }
  }

  return (
    <div className="card">
      <h3>Ride profile (curated)</h3>
      <p className="muted">
        Surfaces to the chauffeur trip view and — consent permitting — to partner offers.
      </p>
      <div className="row" style={{ flexWrap: "wrap", gap: 6, margin: "4px 0 10px" }}>
        {["focus", "relax", "family", "celebrate"].map((m) => (
          <button key={m} onClick={() => void applyMode(m)}>
            {m.charAt(0).toUpperCase() + m.slice(1)} mode
          </button>
        ))}
      </div>
      {RIDE_QUICK_SET.map((f) => (
        <div key={f.key} className="row" style={{ justifyContent: "space-between" }}>
          <span>{f.label}</span>
          {f.options.length > 0 ? (
            <select
              value={values[f.key] ?? ""}
              onChange={(e) => e.target.value && void save(f.key, e.target.value)}
            >
              <option value="">—</option>
              {f.options.map((o) => (
                <option key={o} value={o}>{o.replace(/_/g, " ")}</option>
              ))}
            </select>
          ) : (
            <input
              style={{ maxWidth: 180 }}
              defaultValue={values[f.key] ?? ""}
              onBlur={(e) => e.target.value && void save(f.key, e.target.value)}
            />
          )}
        </div>
      ))}
      {msg && <p>{msg}</p>}
      {err && <p className="error">{err}</p>}
    </div>
  );
}

// Phase 61: the client's end-to-end journey — flights, stays, transfers —
// plus the add-leg form. Flight legs resolve through the GDS chain;
// provenance ("live" / "scheduled" / "manual") is shown verbatim.
type JourneyLeg = {
  id: string;
  kind: string;
  title: string;
  flight_number?: string;
  origin?: string;
  destination?: string;
  sched_start: string;
  actual_start?: string;
  status: string;
  delay_minutes?: number;
  source?: string;
};

const JOURNEY_KINDS = ["flight", "hotel", "ground", "partner"];

function JourneyCard({ clientID }: { clientID: string }) {
  const [legs, setLegs] = useState<JourneyLeg[]>([]);
  const [err, setErr] = useState("");
  const [kind, setKind] = useState("flight");
  const [flightNo, setFlightNo] = useState("");
  const [title, setTitle] = useState("");
  const [origin, setOrigin] = useState("");
  const [destination, setDestination] = useState("");
  const [start, setStart] = useState("");
  const [end, setEnd] = useState("");

  const load = async () => {
    try {
      const r = await api.get<{ legs: JourneyLeg[] }>(`/v1/clients/${clientID}/journey`);
      setLegs(r.legs ?? []);
    } catch (e) { setErr(String(e)); }
  };
  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [clientID]);

  async function addLeg() {
    setErr("");
    try {
      const body: Record<string, unknown> = {
        kind,
        sched_start: new Date(start).toISOString(),
      };
      if (end) body.sched_end = new Date(end).toISOString();
      if (kind === "flight") {
        body.flight_number = flightNo;
        body.origin = origin;
        body.destination = destination;
      } else {
        body.title = title;
        if (origin) body.origin = origin;
        if (destination) body.destination = destination;
      }
      await api.post(`/v1/clients/${clientID}/journey/legs`, body);
      setFlightNo(""); setTitle(""); setOrigin(""); setDestination(""); setStart(""); setEnd("");
      await load();
    } catch (e) { setErr(String(e)); }
  }

  return (
    <>
      <h3>Journey ({legs.length})</h3>
      {legs.length > 0 && (
        <table><tbody>
          {legs.map((l) => (
            <tr key={l.id}>
              <td><span className="badge">{l.kind}</span></td>
              <td>{l.title}</td>
              <td>{new Date(l.actual_start || l.sched_start).toLocaleString()}</td>
              <td>{l.status}{l.delay_minutes ? ` +${l.delay_minutes}m` : ""}</td>
              <td className="muted">{l.source || ""}</td>
            </tr>
          ))}
        </tbody></table>
      )}
      <div className="row">
        <select value={kind} onChange={(e) => setKind(e.target.value)}>
          {JOURNEY_KINDS.map((k) => <option key={k} value={k}>{k}</option>)}
        </select>
        {kind === "flight" ? (
          <input placeholder="flight no (BA75)" value={flightNo} onChange={(e) => setFlightNo(e.target.value)} />
        ) : (
          <input placeholder="title (Eko Hotel)" value={title} onChange={(e) => setTitle(e.target.value)} />
        )}
        <input placeholder="origin" value={origin} onChange={(e) => setOrigin(e.target.value)} />
        <input placeholder="destination" value={destination} onChange={(e) => setDestination(e.target.value)} />
      </div>
      <div className="row">
        <input type="datetime-local" value={start} onChange={(e) => setStart(e.target.value)} />
        <input type="datetime-local" value={end} onChange={(e) => setEnd(e.target.value)} />
        <button onClick={addLeg} disabled={!start || (kind === "flight" ? !flightNo : !title)}>
          Add leg
        </button>
      </div>
      {err && <p className="error">{err}</p>}
    </>
  );
}

// Phase 62: the rider's chauffeur marks — favorites boost matching,
// blocked is a hard exclusion. Names resolve against the crew roster.
type ChauffeurLink = {
  crew_member_id: string;
  kind: string;
  created_at: string;
};

function ChauffeurLinksCard({ clientID }: { clientID: string }) {
  const [links, setLinks] = useState<ChauffeurLink[]>([]);
  const [names, setNames] = useState<Record<string, string>>({});

  const load = async () => {
    try {
      const r = await api.get<{ links: ChauffeurLink[] }>(`/v1/clients/${clientID}/chauffeur-links`);
      setLinks(r.links ?? []);
      const crew = await api.get<{ id: string; full_name: string }[]>("/v1/dispatch/crew");
      const m: Record<string, string> = {};
      for (const c of crew) m[c.id] = c.full_name;
      setNames(m);
    } catch { /* links are additive — the 360 stands without them */ }
  };
  useEffect(() => { load(); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, [clientID]);

  async function clear(crewID: string) {
    await api.del(`/v1/clients/${clientID}/chauffeur-links/${crewID}`);
    await load();
  }

  if (links.length === 0) return null;
  return (
    <>
      <h3>Chauffeur preferences ({links.length})</h3>
      <table><tbody>
        {links.map((l) => (
          <tr key={l.crew_member_id}>
            <td>{names[l.crew_member_id] ?? l.crew_member_id.slice(0, 8)}</td>
            <td><span className="badge">{l.kind}</span></td>
            <td className="muted">{new Date(l.created_at).toLocaleDateString()}</td>
            <td><button className="secondary" onClick={() => clear(l.crew_member_id)}>Clear</button></td>
          </tr>
        ))}
      </tbody></table>
    </>
  );
}
