import { useState } from "react";
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
            </div>

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
