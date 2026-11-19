import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Partner network (Phase 2): directory, vetting, fill-rate KPI.

type Partner = {
  id: string;
  name: string;
  verticals: string[];
  markets: string[];
  status: string;
  quality_score: number;
  payout_rail: string;
  payout_ref?: string;
  commission_pct?: number;
  phone?: string;
};

type Metrics = {
  total_assignments: number;
  partner_assignments: number;
  fill_rate: number;
  vetted_partners: number;
  avg_quality_score: number;
};

// Phase 48: settlement batches + the partner settlement statement.
type Batch = {
  id: string;
  partner_id: string;
  currency: string;
  amount_minor: number;
  receive_minor?: number;
  fees_minor?: number;
  state: string;
  transfer_id?: string;
};

type StatementLine = {
  currency: string;
  payouts: number;
  pending_minor: number;
  in_flight_minor: number;
  settled_minor: number;
  settled_receive_minor: number;
  compensated_minor: number;
  fees_minor: number;
};

type Statement = {
  partner_id: string;
  currencies: StatementLine[];
  batches: Batch[];
};

const minor = (n: number) => (n / 100).toFixed(2);

export default function Partners() {
  const [partners, setPartners] = useState<Partner[]>([]);
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [name, setName] = useState("");
  const [verticals, setVerticals] = useState("CHAUFFEUR_POINT_TO_POINT");
  const [rail, setRail] = useState("MANUAL");
  const [payoutRef, setPayoutRef] = useState("");
  const [phone, setPhone] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setPartners(await api.get<Partner[]>("/v1/partners"));
      setMetrics(await api.get<Metrics>("/v1/partners/metrics"));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create() {
    try {
      await api.post("/v1/partners", {
        name,
        verticals: verticals.split(",").map((v) => v.trim()).filter(Boolean),
        payout_rail: rail,
        payout_ref: payoutRef,
        phone,
      });
      setName("");
      setPayoutRef("");
      setPhone("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "create failed");
    }
  }

  async function setStatus(id: string, status: string) {
    await api.put(`/v1/partners/${id}/status`, { status });
    await load();
  }

  return (
    <section>
      <h2>Partner network</h2>
      {metrics && (
        <div className="card row" style={{ gap: 24 }}>
          <span>Fill rate: <strong>{(metrics.fill_rate * 100).toFixed(0)}%</strong></span>
          <span>Partner assignments: {metrics.partner_assignments}/{metrics.total_assignments}</span>
          <span>Vetted: {metrics.vetted_partners}</span>
          <span>Avg quality: {metrics.avg_quality_score.toFixed(2)}</span>
        </div>
      )}
      {error && <p className="error">{error}</p>}
      <div className="card">
        <h3>Register partner</h3>
        <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
          <input placeholder="Name" value={name} onChange={(e) => setName(e.target.value)} />
          <input
            placeholder="Verticals (comma-separated)"
            value={verticals}
            onChange={(e) => setVerticals(e.target.value)}
          />
          <select value={rail} onChange={(e) => setRail(e.target.value)}>
            <option value="MANUAL">Manual payout</option>
            <option value="MOJALOOP">Mojaloop payout</option>
          </select>
          <input placeholder="Payout ref (FSPIOP id / bank)" value={payoutRef}
            onChange={(e) => setPayoutRef(e.target.value)} />
          <input placeholder="WhatsApp (E.164, e.g. +234…)" value={phone}
            onChange={(e) => setPhone(e.target.value)} />
          <button onClick={() => void create()} disabled={!name}>Add</button>
        </div>
      </div>
      <table>
        <thead>
          <tr><th>Name</th><th>Verticals</th><th>Status</th><th>Quality</th><th>Rail</th><th>Comm.</th><th /></tr>
        </thead>
        <tbody>
          {partners.map((p) => (
            <tr key={p.id}>
              <td>{p.name}</td>
              <td>{(p.verticals ?? []).join(", ")}</td>
              <td><strong>{p.status}</strong></td>
              <td>{p.quality_score.toFixed(2)}</td>
              <td>{p.payout_rail}</td>
              <td>{p.commission_pct ? `${p.commission_pct}%` : "—"}</td>
              <td className="row" style={{ gap: 6 }}>
                {p.status !== "VETTED" && (
                  <button onClick={() => void setStatus(p.id, "VETTED")}>Vet</button>
                )}
                {p.status === "VETTED" && (
                  <button className="secondary" onClick={() => void setStatus(p.id, "SUSPENDED")}>
                    Suspend
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <SettlementsCard partners={partners} onError={setError} />
      <IntegrationCard partners={partners} onError={setError} />
    </section>
  );
}

// Phase 56: partner integration — commission rate, partner-scoped API keys
// (secret shown once), partner webhook feeds, and the embeddable lead
// capture snippet for the partner's own website.
type Integration = {
  partner: Partner;
  keys: { id: string; name: string; prefix: string; revoked: boolean }[];
  webhooks: { id: string; url: string; events: string[]; active: boolean }[];
  capture_endpoint: string;
  capture_source: string;
};

function IntegrationCard({ partners, onError }: { partners: Partner[]; onError: (m: string) => void }) {
  const [partnerID, setPartnerID] = useState("");
  const [info, setInfo] = useState<Integration | null>(null);
  const [pct, setPct] = useState("");
  const [keyName, setKeyName] = useState("");
  const [hookURL, setHookURL] = useState("");
  const [hookEvents, setHookEvents] = useState("assignment.*,partner.*");
  const [secret, setSecret] = useState("");
  const [notice, setNotice] = useState("");

  const load = useCallback(async (pid: string) => {
    if (!pid) {
      setInfo(null);
      return;
    }
    try {
      const res = await api.get<Integration>(`/v1/partners/${pid}/integration`);
      setInfo(res);
      setPct(res.partner.commission_pct ? String(res.partner.commission_pct) : "");
      setSecret("");
    } catch (e) {
      onError(e instanceof Error ? e.message : "integration load failed");
    }
  }, [onError]);

  useEffect(() => {
    void load(partnerID);
  }, [partnerID, load]);

  async function act(fn: () => Promise<unknown>) {
    try {
      await fn();
      await load(partnerID);
    } catch (e) {
      onError(e instanceof Error ? e.message : "integration action failed");
    }
  }

  const snippet = info
    ? `<script>\n` +
      `fetch("${window.location.origin}${info.capture_endpoint}", {\n` +
      `  method: "POST", headers: { "Content-Type": "application/json" },\n` +
      `  body: JSON.stringify({ kind: "person", name, email, phone }),\n` +
      `});\n` +
      `</script>`
    : "";

  return (
    <div className="card">
      <h3>Partner integration (API &amp; webhooks)</h3>
      <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        <select value={partnerID} onChange={(e) => setPartnerID(e.target.value)}>
          <option value="">Select partner…</option>
          {partners.map((p) => (
            <option key={p.id} value={p.id}>{p.name}</option>
          ))}
        </select>
      </div>
      {info && (
        <>
          <h4>Commission</h4>
          <div className="row" style={{ gap: 8 }}>
            <input
              type="number" min="0" max="50" step="0.5" style={{ width: 110 }}
              placeholder="%" value={pct} onChange={(e) => setPct(e.target.value)}
            />
            <button disabled={pct === ""} onClick={() => void act(() =>
              api.put(`/v1/partners/${partnerID}/commission`, { commission_pct: Number(pct) }))}>
              Save rate
            </button>
            <span className="muted">0–50%; accrues as a payout on every partner-routed confirmed booking.</span>
          </div>
          <h4>API keys</h4>
          <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
            <input placeholder="Key name (e.g. partner portal)" value={keyName}
              onChange={(e) => setKeyName(e.target.value)} />
            <button disabled={!keyName} onClick={() => void act(async () => {
              const res = await api.post<{ secret: string }>(`/v1/partners/${partnerID}/keys`, { name: keyName });
              setSecret(res.secret);
              setKeyName("");
            })}>
              Create key
            </button>
          </div>
          {secret && (
            <p className="error">
              Copy this key now — it is shown once: <code>{secret}</code>
            </p>
          )}
          {notice && <p><code style={{ wordBreak: "break-all" }}>{notice}</code></p>}
          {info.keys.length > 0 && (
            <table>
              <thead><tr><th>Name</th><th>Prefix</th><th>State</th></tr></thead>
              <tbody>
                {info.keys.map((k) => (
                  <tr key={k.id}>
                    <td>{k.name}</td><td><code>{k.prefix}…</code></td>
                    <td>{k.revoked ? "REVOKED" : "ACTIVE"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <h4>Webhook feed</h4>
          <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
            <input placeholder="https://partner.example.com/hook" value={hookURL}
              onChange={(e) => setHookURL(e.target.value)} style={{ minWidth: 280 }} />
            <input placeholder="assignment.*,partner.*" value={hookEvents}
              onChange={(e) => setHookEvents(e.target.value)} />
            <button disabled={!hookURL} onClick={() => void act(async () => {
              const res = await api.post<{ secret: string }>(`/v1/partners/${partnerID}/webhooks`, {
                url: hookURL,
                events: hookEvents.split(",").map((v) => v.trim()).filter(Boolean),
              });
              setSecret(res.secret);
              setHookURL("");
            })}>
              Add feed
            </button>
          </div>
          {info.webhooks.length > 0 && (
            <table>
              <thead><tr><th>URL</th><th>Events</th><th>State</th></tr></thead>
              <tbody>
                {info.webhooks.map((wh) => (
                  <tr key={wh.id}>
                    <td>{wh.url}</td><td>{wh.events.join(", ")}</td>
                    <td>{wh.active ? "ACTIVE" : "PAUSED"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
          <h4>Portal &amp; WhatsApp</h4>
          <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
            <button onClick={() => void act(async () => {
              const res = await api.get<{ url: string }>(`/v1/partners/${partnerID}/portal-link`);
              await navigator.clipboard.writeText(res.url);
              setNotice(res.url);
            })}>
              Copy portal link
            </button>
            <button onClick={() => void act(async () => {
              const res = await api.post<{ whatsapp: string }>(`/v1/partners/${partnerID}/notify`, {});
              setNotice(`WhatsApp: ${res.whatsapp}`);
            })}>
              Send WhatsApp offer
            </button>
            <span className="muted">
              The portal link is the partner&apos;s credential — share over a trusted channel.
              WhatsApp needs the partner&apos;s E.164 number and the channel adapter.
            </span>
          </div>
          <h4>Lead capture snippet</h4>
          <p className="muted">
            Partner posts leads into your pipeline with attribution <code>{info.capture_source}</code>:
          </p>
          <pre style={{ whiteSpace: "pre-wrap", fontSize: 12 }}>{snippet}</pre>
        </>
      )}
    </div>
  );
}

// Settlement batching (Phase 48): net a partner's pending Mojaloop payouts
// into one scheme transfer — one fee instead of N — plus the per-currency
// settlement statement for reconciliation.
function SettlementsCard({ partners, onError }: { partners: Partner[]; onError: (m: string) => void }) {
  const [partnerID, setPartnerID] = useState("");
  const [currency, setCurrency] = useState("EUR");
  const [statement, setStatement] = useState<Statement | null>(null);
  const [batches, setBatches] = useState<Batch[]>([]);

  const load = useCallback(async (pid: string) => {
    if (!pid) {
      setStatement(null);
      return;
    }
    try {
      setStatement(await api.get<Statement>(`/v1/partners/${pid}/settlement-statement`));
      setBatches(await api.get<Batch[]>("/v1/settlements/batches"));
    } catch (e) {
      onError(e instanceof Error ? e.message : "settlement load failed");
    }
  }, [onError]);

  useEffect(() => {
    void load(partnerID);
  }, [partnerID, load]);

  async function act(fn: () => Promise<unknown>) {
    try {
      await fn();
      await load(partnerID);
    } catch (e) {
      onError(e instanceof Error ? e.message : "settlement action failed");
    }
  }

  const partnerBatches = batches.filter((b) => b.partner_id === partnerID);

  return (
    <div className="card">
      <h3>Settlements (Mojaloop batching)</h3>
      <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        <select value={partnerID} onChange={(e) => setPartnerID(e.target.value)}>
          <option value="">Select partner…</option>
          {partners.filter((p) => p.status === "VETTED" && p.payout_rail === "MOJALOOP").map((p) => (
            <option key={p.id} value={p.id}>{p.name}</option>
          ))}
        </select>
        <select value={currency} onChange={(e) => setCurrency(e.target.value)}>
          <option>EUR</option><option>USD</option><option>GBP</option><option>NGN</option><option>KES</option><option>AED</option>
        </select>
        <button disabled={!partnerID} onClick={() => void act(() =>
          api.post("/v1/settlements/batches", {
            partner_id: partnerID, currency,
            idempotency_key: `console-${partnerID}-${currency}-${Date.now()}`,
          }))}>
          Assemble batch
        </button>
      </div>
      {statement && statement.currencies.length > 0 && (
        <table>
          <thead>
            <tr><th>Currency</th><th>Payouts</th><th>Pending</th><th>In flight</th><th>Settled</th><th>Received</th><th>Compensated</th><th>Fees</th></tr>
          </thead>
          <tbody>
            {statement.currencies.map((c) => (
              <tr key={c.currency}>
                <td><strong>{c.currency}</strong></td>
                <td>{c.payouts}</td>
                <td>{minor(c.pending_minor)}</td>
                <td>{minor(c.in_flight_minor)}</td>
                <td>{minor(c.settled_minor)}</td>
                <td>{minor(c.settled_receive_minor)}</td>
                <td>{minor(c.compensated_minor)}</td>
                <td>{minor(c.fees_minor)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {partnerBatches.length > 0 && (
        <table>
          <thead>
            <tr><th>Batch</th><th>State</th><th>Amount</th><th>Receive</th><th>Fees</th><th /></tr>
          </thead>
          <tbody>
            {partnerBatches.map((b) => (
              <tr key={b.id}>
                <td>{b.id.slice(0, 8)}…</td>
                <td><strong>{b.state}</strong></td>
                <td>{minor(b.amount_minor)} {b.currency}</td>
                <td>{b.receive_minor ? minor(b.receive_minor) : "—"}</td>
                <td>{b.fees_minor ? minor(b.fees_minor) : "—"}</td>
                <td className="row" style={{ gap: 6 }}>
                  {b.state === "OPEN" && (
                    <button onClick={() => void act(() => api.post(`/v1/settlements/batches/${b.id}/quote`, {}))}>Quote</button>
                  )}
                  {b.state === "QUOTED" && (
                    <button onClick={() => void act(() => api.post(`/v1/settlements/batches/${b.id}/execute`, {}))}>Execute</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
