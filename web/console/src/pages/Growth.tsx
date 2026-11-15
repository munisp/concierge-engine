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
  gift?: boolean;
  switch_offer?: boolean;
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
  scheduled_for?: string;
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

// Phase 35: acquisition & outreach surfaces.
type LeaderRow = {
  client_id: string;
  name: string;
  code: string;
  conversions: number;
  rewards_minor: number;
  currency?: string;
};

type SegmentRules = {
  locale?: string;
  loyalty_tier?: string;
  min_bookings?: number;
  no_booking_since_days?: number;
  has_bookings?: boolean;
  marketing_consent?: boolean;
  contact_basis?: string;
};

type BroadcastResult = {
  campaign: string;
  matched: number;
  sent: number;
  scheduled: number;
  held: number;
  failed: number;
  variant_a?: number;
  variant_b?: number;
};

type CampaignFunnel = {
  campaign: string;
  sent: number;
  scheduled: number;
  held: number;
  failed: number;
  converted: number;
  variants?: Record<string, number>;
};

type MarketingLinks = {
  capture_links: Record<string, string>;
  active_campaign: string;
  capture_page: string;
};

const KIND_ORDER = ["page_view", "ad_click", "conversation_started", "request_created", "booking", "outreach_sent", "referral_converted"];

export default function Growth() {
  const [funnel, setFunnel] = useState<FunnelRow[]>([]);
  const [sends, setSends] = useState<Send[]>([]);
  const [sendsView, setSendsView] = useState<"feed" | "table">("feed");
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
  const [oWhen, setOWhen] = useState(""); // Phase 35 (O6): schedule for later

  const [promos, setPromos] = useState<Promo[]>([]);
  const [pCode, setPCode] = useState("");
  const [pGift, setPGift] = useState(false);
  const [pSwitch, setPSwitch] = useState(false);
  // Phase 26
  const [cohort, setCohort] = useState<Cohort | null>(null);
  const [invoice, setInvoice] = useState<FeeInvoice | null>(null);
  const [pCredit, setPCredit] = useState("");
  const [pCurrency, setPCurrency] = useState("EUR");
  const [pMax, setPMax] = useState("");
  const [pDays, setPDays] = useState("");
  // Phase 35: leaderboard, broadcasts, campaign funnel, link builder.
  const [leaders, setLeaders] = useState<LeaderRow[]>([]);
  const [links, setLinks] = useState<MarketingLinks | null>(null);
  const [bCampaign, setBCampaign] = useState("");
  const [bBody, setBBody] = useState("");
  const [bTemplateId, setBTemplateId] = useState("");
  const [bAB, setBAB] = useState(false);
  const [bTier, setBTier] = useState("");
  const [bMinBookings, setBMinBookings] = useState("");
  const [bDormantDays, setBDormantDays] = useState("");
  const [bConsentOnly, setBConsentOnly] = useState(true);
  const [bBasis, setBBasis] = useState(""); // Phase 38: contact-basis segment rule
  const [segCount, setSegCount] = useState<number | null>(null);
  const [bResult, setBResult] = useState<BroadcastResult | null>(null);
  const [fName, setFName] = useState("");
  const [funnel35, setFunnel35] = useState<CampaignFunnel | null>(null);

  const segmentRules = (): SegmentRules => {
    const r: SegmentRules = {};
    if (bTier) r.loyalty_tier = bTier;
    if (bMinBookings) r.min_bookings = parseInt(bMinBookings, 10);
    if (bDormantDays) r.no_booking_since_days = parseInt(bDormantDays, 10);
    if (bConsentOnly) r.marketing_consent = true;
    if (bBasis) r.contact_basis = bBasis;
    return r;
  };

  const load = () => {
    api.get<FunnelRow[]>("/v1/attribution/funnel").then(setFunnel).catch((e) => setErr(String(e)));
    api.get<Send[]>("/v1/outreach/sends?limit=20").then(setSends).catch((e) => setErr(String(e)));
    api.get<Promo[]>("/v1/promos").then(setPromos).catch((e) => setErr(String(e)));
    api.get<MsgTemplate[]>("/v1/outreach/templates").then(setTemplates).catch((e) => setErr(String(e)));
    api.get<Cohort>("/v1/acquisition/cohort").then(setCohort).catch(() => setCohort(null));
    api.get<FeeInvoice>("/v1/billing/acquisition-fees").then(setInvoice).catch(() => setInvoice(null));
    api.get<{ leaderboard: LeaderRow[] }>("/v1/growth/referral-leaderboard?limit=10")
      .then((r) => setLeaders(r.leaderboard)).catch(() => setLeaders([]));
    api.get<MarketingLinks>("/v1/marketing/links").then(setLinks).catch(() => setLinks(null));
  };

  const previewSegment = async () => {
    setErr(""); setSegCount(null);
    try {
      const r = await api.post<{ count: number }>("/v1/outreach/segments/preview", segmentRules());
      setSegCount(r.count);
    } catch (e) { setErr(String(e)); }
  };

  const broadcast = async () => {
    setErr(""); setBResult(null);
    try {
      const r = await api.post<BroadcastResult>("/v1/outreach/broadcast", {
        campaign: bCampaign, purpose: "marketing",
        body: bBody || undefined, template_id: bTemplateId || undefined,
        ab_test: bAB || undefined, segment: segmentRules(),
      });
      setBResult(r);
      load();
    } catch (e) { setErr(String(e)); }
  };

  const loadFunnel = async () => {
    setErr(""); setFunnel35(null);
    try {
      setFunnel35(await api.get<CampaignFunnel>(`/v1/outreach/campaigns/${encodeURIComponent(fName)}/funnel`));
    } catch (e) { setErr(String(e)); }
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
        gift: pGift || undefined,
        switch_offer: pSwitch || undefined,
      });
      setPCode(""); setPCredit(""); setPMax(""); setPDays(""); setPGift(false); setPSwitch(false);
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
        scheduled_for: oWhen ? new Date(oWhen).toISOString() : undefined,
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
        <h2>Marketing links</h2>
        {links ? (
          <>
            {links.active_campaign && (
              <p className="ok">Campaign window active: <strong>{links.active_campaign}</strong> — all CTAs stamp it as utm_campaign.</p>
            )}
            <table>
              <thead><tr><th>Channel</th><th>Capture link</th></tr></thead>
              <tbody>
                {Object.entries(links.capture_links).map(([ch, url]) => (
                  <tr key={ch}>
                    <td>{ch}</td>
                    <td><code style={{ fontSize: 11 }}>{url}</code></td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        ) : <p className="muted">Link builder unavailable.</p>}
      </div>

      <div className="card">
        <h2>Referral leaderboard</h2>
        {leaders.length === 0 ? <p>No referral conversions yet — share a client's code to start the loop.</p> : (
          <table>
            <thead><tr><th>#</th><th>Client</th><th>Code</th><th>Conversions</th><th>Rewards earned</th></tr></thead>
            <tbody>
              {leaders.map((r, i) => (
                <tr key={r.client_id}>
                  <td>{i + 1}</td>
                  <td>{r.name || r.client_id.slice(0, 8)}</td>
                  <td><strong>{r.code}</strong></td>
                  <td>{r.conversions}</td>
                  <td>{(r.rewards_minor / 100).toFixed(2)} {r.currency || ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>

      <div className="card">
        <h2>Broadcast campaign</h2>
        <p className="muted">
          Fan one message out to a segment. Every send keeps its consent gate, weekly cap and
          quiet-hours deferral; re-running the same campaign name is idempotent per client.
        </p>
        <div className="row">
          <input placeholder="campaign name (e.g. spring-launch)" value={bCampaign} onChange={(e) => setBCampaign(e.target.value)} />
          <select value={bTemplateId} onChange={(e) => setBTemplateId(e.target.value)}>
            <option value="">freeform body…</option>
            {templates.map((t) => (
              <option key={t.id} value={t.id}>{t.name} ({t.purpose})</option>
            ))}
          </select>
          <label><input type="checkbox" checked={bAB} onChange={(e) => setBAB(e.target.checked)} disabled={!bTemplateId} /> A/B (needs a #b template)</label>
        </div>
        {!bTemplateId && (
          <div className="row">
            <input style={{ flex: 1 }} placeholder="message body" value={bBody} onChange={(e) => setBBody(e.target.value)} />
          </div>
        )}
        <div className="row">
          <select value={bTier} onChange={(e) => setBTier(e.target.value)}>
            <option value="">any tier</option>
            <option value="vip">vip</option>
            <option value="gold">gold</option>
            <option value="silver">silver</option>
          </select>
          <input placeholder="min bookings" value={bMinBookings} onChange={(e) => setBMinBookings(e.target.value)} style={{ width: 110 }} />
          <input placeholder="dormant ≥ days" value={bDormantDays} onChange={(e) => setBDormantDays(e.target.value)} style={{ width: 130 }} />
          <select value={bBasis} onChange={(e) => setBBasis(e.target.value)} title="Lead contact basis (Phase 38)">
            <option value="">any basis</option>
            <option value="self_reported">self-reported</option>
            <option value="partner_consent">partner-consented</option>
            <option value="b2b_legitimate_interest">b2b sourced</option>
          </select>
          <label><input type="checkbox" checked={bConsentOnly} onChange={(e) => setBConsentOnly(e.target.checked)} /> consented only</label>
          <button onClick={previewSegment}>Preview audience</button>
          <button onClick={broadcast} disabled={!bCampaign || (!bBody && !bTemplateId)}>Send broadcast</button>
        </div>
        {segCount !== null && <p className="ok">Audience: <strong>{segCount}</strong> clients match.</p>}
        {bResult && (
          <p className="ok">
            {bResult.campaign}: {bResult.matched} matched — {bResult.sent} sent, {bResult.scheduled} scheduled,{" "}
            {bResult.held} held, {bResult.failed} failed
            {bResult.variant_a !== undefined && ` · A ${bResult.variant_a} / B ${bResult.variant_b}`}
          </p>
        )}
      </div>

      <div className="card">
        <h2>Campaign funnel</h2>
        <div className="row">
          <input placeholder="campaign name" value={fName} onChange={(e) => setFName(e.target.value)} />
          <button onClick={loadFunnel} disabled={!fName}>Load funnel</button>
        </div>
        {funnel35 && (
          <p>
            <strong>{funnel35.campaign}</strong> — sent {funnel35.sent} · scheduled {funnel35.scheduled} ·
            held {funnel35.held} · failed {funnel35.failed} · <strong>converted {funnel35.converted}</strong>
            {funnel35.variants && ` · A ${funnel35.variants.a ?? 0} sent / B ${funnel35.variants.b ?? 0} sent`}
          </p>
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
          <label title="One-recipient voucher an existing client hands to a non-client">
            <input type="checkbox" checked={pGift} onChange={(e) => setPGift(e.target.checked)} /> gift
          </label>
          <label title="Competitor-switch offer — redemption alerts your operator roster">
            <input type="checkbox" checked={pSwitch} onChange={(e) => setPSwitch(e.target.checked)} /> switch offer
          </label>
          <button onClick={createPromo} disabled={!pCode || !pCredit}>Create</button>
        </div>
        {promos.length === 0 ? <p>No campaigns yet.</p> : (
          <table>
            <thead><tr><th>Code</th><th>Credit</th><th>Cap</th><th>Kind</th><th>Expires</th><th>Status</th></tr></thead>
            <tbody>
              {promos.map((pr) => (
                <tr key={pr.id}>
                  <td><strong>{pr.code}</strong></td>
                  <td>{(pr.credit_minor / 100).toFixed(2)} {pr.currency}</td>
                  <td>{pr.max_redemptions || "∞"}</td>
                  <td>{pr.gift ? "gift voucher" : pr.switch_offer ? "switch offer" : "promo"}</td>
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
          <input type="datetime-local" title="schedule for later (optional)" value={oWhen} onChange={(e) => setOWhen(e.target.value)} />
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
        <div style={{ marginBottom: 8, display: "flex", gap: 4 }}>
          <button className={sendsView === "feed" ? "" : "secondary"} onClick={() => setSendsView("feed")}>Feed</button>
          <button className={sendsView === "table" ? "" : "secondary"} onClick={() => setSendsView("table")}>Table</button>
        </div>
        {sends.length === 0 ? <p>No outreach sends yet.</p> : sendsView === "feed" ? (
          <div className="outfeed">
            {sends.map((s) => (
              <div className={`bubble ${s.status}`} key={s.id}>
                <div className="bubble-head">
                  {s.purpose}{s.template ? ` · ${s.template}` : ""} → client {s.client_id.slice(0, 8)}
                </div>
                {s.body && <div className="bubble-body">{s.body}</div>}
                <div className="bubble-foot">
                  <span className={`chip chip-${s.status}`}>
                    {s.status}{s.status === "scheduled" && s.scheduled_for
                      ? ` · ${new Date(s.scheduled_for).toLocaleString()}` : ""}
                  </span>
                  {s.detail ? ` ${s.detail}` : ""} · {new Date(s.created_at).toLocaleString()}
                  {s.external_ref ? ` · ${s.external_ref.slice(0, 14)}` : ""}
                </div>
              </div>
            ))}
          </div>
        ) : (
          <table>
            <thead><tr><th>When</th><th>Client</th><th>Purpose</th><th>Status</th><th>Detail</th><th>Ref</th></tr></thead>
            <tbody>
              {sends.map((s) => (
                <tr key={s.id}>
                  <td>{new Date(s.created_at).toLocaleString()}</td>
                  <td>{s.client_id.slice(0, 8)}</td>
                  <td>{s.purpose}</td>
                  <td>{s.status}{s.status === "scheduled" && s.scheduled_for
                    ? ` (${new Date(s.scheduled_for).toLocaleString()})` : ""}</td>
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
