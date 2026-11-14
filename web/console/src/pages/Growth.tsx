import { useEffect, useState } from "react";
import { api } from "../api";

// Phase 14 growth board: acquisition funnel (first-touch attributed),
// client referral codes, and the proactive outreach composer with live
// consent / frequency-cap verdicts.

type FunnelRow = {
  kind: string;
  channel: string;
  campaign: string;
  count: number;
  value_minor: number;
};

type Verdict = {
  allowed: boolean;
  reason?: string;
  marketing_sent_7d: number;
  marketing_cap: number;
  marketing_consent: boolean;
};

type RewardRow = {
  id: string;
  kind: string;
  amount_minor: number;
  currency: string;
  state: string;
  created_at: string;
  expires_at?: string;
  redeemed_intent_id?: string;
};

type RewardSummary = {
  client_id: string;
  balance_minor: number;
  rewards: RewardRow[];
};

type Send = {
  id: string;
  client_id: string;
  purpose: string;
  body?: string;
  template?: string;
  status: string;
  detail?: string;
  external_ref?: string;
  created_at: string;
};

const KIND_ORDER = ["page_view", "ad_click", "conversation_started", "request_created", "booking", "outreach_sent", "referral_converted"];

export default function Growth() {
  const [funnel, setFunnel] = useState<FunnelRow[]>([]);
  const [sends, setSends] = useState<Send[]>([]);
  const [err, setErr] = useState("");

  // referral code lookup
  const [clientId, setClientId] = useState("");
  const [code, setCode] = useState("");
  const [rewards, setRewards] = useState<RewardSummary | null>(null);

  // outreach composer
  const [oClient, setOClient] = useState("");
  const [oPurpose, setOPurpose] = useState("service");
  const [oBody, setOBody] = useState("");
  const [oTemplate, setOTemplate] = useState("");
  const [verdict, setVerdict] = useState<Verdict | null>(null);
  const [sentMsg, setSentMsg] = useState("");

  const load = () => {
    api.get<FunnelRow[]>("/v1/attribution/funnel").then(setFunnel).catch((e) => setErr(String(e)));
    api.get<Send[]>("/v1/outreach/sends?limit=20").then(setSends).catch((e) => setErr(String(e)));
  };
  useEffect(() => { load(); }, []);

  const getCode = async () => {
    setErr(""); setCode("");
    try {
      const r = await api.get<{ code: string }>(`/v1/clients/${encodeURIComponent(clientId)}/referral-code`);
      setCode(r.code);
    } catch (e) { setErr(String(e)); }
  };

  const getRewards = async () => {
    setErr(""); setRewards(null);
    try {
      setRewards(await api.get<RewardSummary>(`/v1/clients/${encodeURIComponent(clientId)}/rewards`));
    } catch (e) { setErr(String(e)); }
  };

  const checkVerdict = async () => {
    setErr(""); setVerdict(null);
    try {
      setVerdict(await api.get<Verdict>(
        `/v1/outreach/verdict?client_id=${encodeURIComponent(oClient)}&purpose=${encodeURIComponent(oPurpose)}`));
    } catch (e) { setErr(String(e)); }
  };

  const send = async () => {
    setErr(""); setSentMsg("");
    try {
      const o = await api.post<Send>("/v1/outreach/send", {
        client_id: oClient, purpose: oPurpose, body: oBody || undefined,
        template: oTemplate || undefined,
        idempotency_key: `console-${Date.now()}`,
      });
      setSentMsg(`${o.status}${o.detail ? ` — ${o.detail}` : ""}${o.external_ref ? ` (ref ${o.external_ref})` : ""}`);
      load();
    } catch (e) { setErr(String(e)); }
  };

  const kinds = KIND_ORDER.filter((k) => funnel.some((r) => r.kind === k));
  const byKind = (k: string) => funnel.filter((r) => r.kind === k);
  const kindTotal = (k: string) => byKind(k).reduce((n, r) => n + r.count, 0);
  const kindValue = (k: string) => byKind(k).reduce((n, r) => n + r.value_minor, 0);
  const conv = (from: string, to: string) => {
    const f = kindTotal(from), t = kindTotal(to);
    return f > 0 ? `${((t / f) * 100).toFixed(1)}%` : "—";
  };

  return (
    <div>
      <h1>Growth</h1>
      {err && <p className="error">{err}</p>}

      <div className="card">
        <h2>Acquisition funnel (first-touch attributed)</h2>
        {funnel.length === 0 ? <p>No attribution events yet.</p> : (
          <>
            <p>
              {kinds.map((k, i) => (
                <span key={k}>
                  {i > 0 && " → "}
                  <strong>{k}</strong> {kindTotal(k)}
                  {kindValue(k) > 0 && ` (${(kindValue(k) / 100).toFixed(2)})`}
                </span>
              ))}
            </p>
            <p>
              conversation→request <strong>{conv("conversation_started", "request_created")}</strong>
              {" · "}request→booking <strong>{conv("request_created", "booking")}</strong>
            </p>
            <table>
              <thead><tr><th>Kind</th><th>Channel</th><th>Campaign</th><th>Count</th><th>Value</th></tr></thead>
              <tbody>
                {funnel.map((r, i) => (
                  <tr key={i}>
                    <td>{r.kind}</td><td>{r.channel || "—"}</td><td>{r.campaign || "—"}</td>
                    <td>{r.count}</td><td>{r.value_minor > 0 ? (r.value_minor / 100).toFixed(2) : "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </div>

      <div className="card">
        <h2>Client referral codes</h2>
        <div className="row">
          <input placeholder="client id" value={clientId} onChange={(e) => setClientId(e.target.value)} />
          <button onClick={getCode}>Get code</button>
          <button onClick={getRewards} disabled={!clientId}>Rewards</button>
          {code && <strong>{code}</strong>}
        </div>
        <p className="muted">
          Share as a capture link: <code>/capture?ref={code || "VIP-…"}</code> — the referee's
          first booking fires <code>referral.converted</code> and lands in the funnel above.
          Both sides earn credit per the tenant's reward program (referrer % + referee
          welcome %, optional cap and expiry); credit redeems against the holder's next
          payment link and a refund never burns it.
        </p>
        {rewards && (
          <>
            <p>
              Redeemable balance: <strong>{(rewards.balance_minor / 100).toFixed(2)}</strong>
              {rewards.rewards[0] ? ` ${rewards.rewards[0].currency}` : ""}
            </p>
            {rewards.rewards.length === 0 ? <p>No rewards yet.</p> : (
              <table>
                <thead><tr><th>When</th><th>Kind</th><th>Amount</th><th>State</th><th>Expires</th></tr></thead>
                <tbody>
                  {rewards.rewards.map((rw) => (
                    <tr key={rw.id}>
                      <td>{new Date(rw.created_at).toLocaleString()}</td>
                      <td>{rw.kind === "referee_welcome" ? "welcome credit" : "referral reward"}</td>
                      <td>{(rw.amount_minor / 100).toFixed(2)} {rw.currency}</td>
                      <td>{rw.state}</td>
                      <td>{rw.expires_at ? new Date(rw.expires_at).toLocaleDateString() : "never"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </>
        )}
      </div>

      <div className="card">
        <h2>Outreach composer</h2>
        <div className="row">
          <input placeholder="client id" value={oClient} onChange={(e) => setOClient(e.target.value)} />
          <select value={oPurpose} onChange={(e) => setOPurpose(e.target.value)}>
            <option value="service">service</option>
            <option value="marketing">marketing</option>
          </select>
          <button onClick={checkVerdict}>Check verdict</button>
        </div>
        {verdict && (
          <p className={verdict.allowed ? "ok" : "error"}>
            {verdict.allowed ? "allowed" : `held — ${verdict.reason}`}
            {oPurpose === "marketing" &&
              ` · consent ${verdict.marketing_consent ? "yes" : "no"} · ${verdict.marketing_sent_7d}/${verdict.marketing_cap} this week`}
          </p>
        )}
        <div className="row">
          <input placeholder="body (freeform, inside 24h session)" value={oBody} onChange={(e) => setOBody(e.target.value)} />
        </div>
        <div className="row">
          <input placeholder="or approved template name" value={oTemplate} onChange={(e) => setOTemplate(e.target.value)} />
          <button onClick={send} disabled={!oClient || (!oBody && !oTemplate)}>Send</button>
        </div>
        {sentMsg && <p className="ok">{sentMsg}</p>}
      </div>

      <div className="card">
        <h2>Recent outreach sends</h2>
        {sends.length === 0 ? <p>No outreach sends yet.</p> : (
          <table>
            <thead><tr><th>When</th><th>Client</th><th>Purpose</th><th>Status</th><th>Detail</th><th>Ref</th></tr></thead>
            <tbody>
              {sends.map((s) => (
                <tr key={s.id}>
                  <td>{new Date(s.created_at).toLocaleString()}</td>
                  <td>{s.client_id.slice(0, 8)}</td>
                  <td>{s.purpose}</td>
                  <td>{s.status}</td>
                  <td>{s.detail || "—"}</td>
                  <td>{s.external_ref ? s.external_ref.slice(0, 14) : "—"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
