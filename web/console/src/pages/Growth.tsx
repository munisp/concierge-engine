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

type Promo = {
  id: string;
  code: string;
  credit_minor: number;
  currency: string;
  max_redemptions?: number;
  expires_at?: string;
  active: boolean;
  created_at: string;
};

// Phase 26: acquisition cohort scoreboard + pay-for-performance invoice.
type Cohort = {
  goal_clients: number;
  goal_days: number;
  started_at: string;
  days_elapsed: number;
  new_clients: number;
  pacing_expected: number;
  projection: number;
  on_track: boolean;
  by_channel: Record<string, number>;
};

type FeeLine = {
  booking_id: string;
  client_id?: string;
  channel?: string;
  campaign?: string;
  amount_minor: number;
  fee_minor: number;
  refunded_minor?: number;
  currency: string;
  created_at: string;
};

type FeeInvoice = {
  from: string;
  to: string;
  lines: FeeLine[];
  attributed_gmv_minor: number;
  gross_fee_minor: number;
  clawback_minor: number;
  net_fee_minor: number;
  currency: string;
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

type MsgTemplate = {
  id: string;
  name: string;
  purpose: string;
  body: string;
  created_at: string;
  updated_at: string;
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

  // message templates (Phase 21)
  const [templates, setTemplates] = useState<MsgTemplate[]>([]);
  const [tplName, setTplName] = useState("");
  const [tplPurpose, setTplPurpose] = useState("any");
  const [tplBody, setTplBody] = useState("");
  const [oTemplateId, setOTemplateId] = useState("");

  const [promos, setPromos] = useState<Promo[]>([]);
  const [pCode, setPCode] = useState("");
  // Phase 26
  const [cohort, setCohort] = useState<Cohort | null>(null);
  const [invoice, setInvoice] = useState<FeeInvoice | null>(null);
  const [pCredit, setPCredit] = useState("");
  const [pCurrency, setPCurrency] = useState("EUR");
  const [pMax, setPMax] = useState("");
  const [pDays, setPDays] = useState("");

  const load = () => {
    api.get<FunnelRow[]>("/v1/attribution/funnel").then(setFunnel).catch((e) => setErr(String(e)));
    api.get<Send[]>("/v1/outreach/sends?limit=20").then(setSends).catch((e) => setErr(String(e)));
    api.get<Promo[]>("/v1/promos").then(setPromos).catch((e) => setErr(String(e)));
    api.get<MsgTemplate[]>("/v1/outreach/templates").then(setTemplates).catch((e) => setErr(String(e)));
    api.get<Cohort>("/v1/acquisition/cohort").then(setCohort).catch(() => setCohort(null));
    api.get<FeeInvoice>("/v1/billing/acquisition-fees").then(setInvoice).catch(() => setInvoice(null));
  };

  const saveTemplate = async () => {
    setErr("");
    try {
      await api.post<MsgTemplate>("/v1/outreach/templates", {
        name: tplName, purpose: tplPurpose, body: tplBody,
      });
      setTplName(""); setTplBody("");
      load();
    } catch (e) { setErr(String(e)); }
  };

  const deleteTemplate = async (id: string) => {
    setErr("");
    try {
      await api.del(`/v1/outreach/templates/${id}`);
      if (oTemplateId === id) setOTemplateId("");
      load();
    } catch (e) { setErr(String(e)); }
  };

  const createPromo = async () => {
    setErr("");
    try {
      await api.post<Promo>("/v1/promos", {
        code: pCode,
        credit_minor: Math.round(parseFloat(pCredit) * 100),
        currency: pCurrency,
        max_redemptions: pMax ? parseInt(pMax, 10) : undefined,
        expiry_days: pDays ? parseInt(pDays, 10) : undefined,
      });
      setPCode(""); setPCredit(""); setPMax(""); setPDays("");
      load();
    } catch (e) { setErr(String(e)); }
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
        template: oTemplate || undefined, template_id: oTemplateId || undefined,
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

      {cohort && (
        <div className="card">
          <h2>New-client cohort — {cohort.new_clients} / {cohort.goal_clients} in {cohort.goal_days} days</h2>
          <p>
            Day {cohort.days_elapsed} of {cohort.goal_days} · pacing expects{" "}
            {cohort.pacing_expected} · projection <strong>{cohort.projection}</strong>{" "}
            {cohort.on_track
              ? <span className="badge">ON TRACK</span>
              : <span className="badge" style={{ background: "#c0392b" }}>BEHIND</span>}
          </p>
          <div style={{ background: "#e5e5e5", borderRadius: 6, height: 14, overflow: "hidden" }}>
            <div style={{
              background: cohort.on_track ? "#27ae60" : "#c0392b", height: "100%",
              width: `${Math.min(100, (cohort.new_clients / Math.max(1, cohort.goal_clients)) * 100)}%`,
            }} />
          </div>
          {Object.keys(cohort.by_channel).length > 0 && (
            <p className="muted">
              {Object.entries(cohort.by_channel).map(([ch, n]) => `${ch}: ${n}`).join(" · ")}
            </p>
          )}
        </div>
      )}

      {invoice && (
        <div className="card">
          <h2>Acquisition fee (this month)</h2>
          <p>
            Attributed GMV <strong>{(invoice.attributed_gmv_minor / 100).toFixed(2)}</strong>{" "}
            {invoice.currency} · gross fee <strong>{(invoice.gross_fee_minor / 100).toFixed(2)}</strong>
            {invoice.clawback_minor > 0 && <> · refunded −{(invoice.clawback_minor / 100).toFixed(2)}</>}
            {" "}· net due <strong>{(invoice.net_fee_minor / 100).toFixed(2)}</strong>
          </p>
          {invoice.lines.length > 0 && (
            <table>
              <thead><tr><th>Booking</th><th>Channel</th><th>Campaign</th><th>Fee</th></tr></thead>
              <tbody>
                {invoice.lines.slice(0, 10).map((l) => (
                  <tr key={l.booking_id}>
                    <td>{l.booking_id.slice(0, 8)}</td>
                    <td>{l.channel || "—"}</td><td>{l.campaign || "—"}</td>
                    <td>{(l.fee_minor / 100).toFixed(2)} {l.currency}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      )}

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
        <h2>Promo campaigns</h2>
        <p className="muted">
          Fixed-credit codes clients redeem once each — the credit lands in their reward
          balance and comes off their next payment link automatically.
        </p>
        <div className="row">
          <input placeholder="code (e.g. SUMMER25)" value={pCode} onChange={(e) => setPCode(e.target.value)} />
          <input placeholder="credit (25.00)" value={pCredit} onChange={(e) => setPCredit(e.target.value)} />
          <select value={pCurrency} onChange={(e) => setPCurrency(e.target.value)}>
            <option value="EUR">EUR</option><option value="USD">USD</option>
            <option value="GBP">GBP</option><option value="NGN">NGN</option>
          </select>
          <input placeholder="max redemptions" value={pMax} onChange={(e) => setPMax(e.target.value)} />
          <input placeholder="valid days" value={pDays} onChange={(e) => setPDays(e.target.value)} />
          <button onClick={createPromo} disabled={!pCode || !pCredit}>Create</button>
        </div>
        {promos.length === 0 ? <p>No campaigns yet.</p> : (
          <table>
            <thead><tr><th>Code</th><th>Credit</th><th>Cap</th><th>Expires</th><th>Status</th></tr></thead>
            <tbody>
              {promos.map((pr) => (
                <tr key={pr.id}>
                  <td><strong>{pr.code}</strong></td>
                  <td>{(pr.credit_minor / 100).toFixed(2)} {pr.currency}</td>
                  <td>{pr.max_redemptions || "∞"}</td>
                  <td>{pr.expires_at ? new Date(pr.expires_at).toLocaleDateString() : "never"}</td>
                  <td>{pr.active ? "active" : "inactive"}</td>
                </tr>
              ))}
            </tbody>
          </table>
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
          <input placeholder="body (freeform, inside 24h session)" value={oBody}
            onChange={(e) => setOBody(e.target.value)} disabled={!!oTemplateId} />
        </div>
        <div className="row">
          <select value={oTemplateId} onChange={(e) => setOTemplateId(e.target.value)}>
            <option value="">saved template… (rendered server-side)</option>
            {templates.map((t) => (
              <option key={t.id} value={t.id}>{t.name} ({t.purpose})</option>
            ))}
          </select>
          <input placeholder="or approved WA template name" value={oTemplate} onChange={(e) => setOTemplate(e.target.value)} />
          <button onClick={send} disabled={!oClient || (!oBody && !oTemplate && !oTemplateId)}>Send</button>
        </div>
        {oTemplateId && (
          <p className="muted">Body renders server-side from the template — {"{{client_name}}"} and {"{{tenant_name}}"} are substituted at send time.</p>
        )}
        {sentMsg && <p className="ok">{sentMsg}</p>}
      </div>

      <div className="card">
        <h2>Message templates</h2>
        <div className="row">
          <input placeholder="name (e.g. winback-q4)" value={tplName} onChange={(e) => setTplName(e.target.value)} />
          <select value={tplPurpose} onChange={(e) => setTplPurpose(e.target.value)}>
            <option value="any">any purpose</option>
            <option value="service">service</option>
            <option value="marketing">marketing</option>
          </select>
          <button onClick={saveTemplate} disabled={!tplName || !tplBody}>Save template</button>
        </div>
        <div className="row">
          <input style={{ flex: 1 }} placeholder="body — {{client_name}} and {{tenant_name}} substituted at send time"
            value={tplBody} onChange={(e) => setTplBody(e.target.value)} />
        </div>
        {templates.length === 0 ? <p>No templates yet.</p> : (
          <table>
            <thead><tr><th>Name</th><th>Purpose</th><th>Body</th><th>Updated</th><th></th></tr></thead>
            <tbody>
              {templates.map((t) => (
                <tr key={t.id}>
                  <td>{t.name}</td>
                  <td>{t.purpose}</td>
                  <td>{t.body.length > 60 ? t.body.slice(0, 60) + "…" : t.body}</td>
                  <td>{new Date(t.updated_at).toLocaleDateString()}</td>
                  <td><button onClick={() => void deleteTemplate(t.id)}>Delete</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
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
