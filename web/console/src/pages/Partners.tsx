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
      });
      setName("");
      setPayoutRef("");
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
          <button onClick={() => void create()} disabled={!name}>Add</button>
        </div>
      </div>
      <table>
        <thead>
          <tr><th>Name</th><th>Verticals</th><th>Status</th><th>Quality</th><th>Rail</th><th /></tr>
        </thead>
        <tbody>
          {partners.map((p) => (
            <tr key={p.id}>
              <td>{p.name}</td>
              <td>{(p.verticals ?? []).join(", ")}</td>
              <td><strong>{p.status}</strong></td>
              <td>{p.quality_score.toFixed(2)}</td>
              <td>{p.payout_rail}</td>
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
    </section>
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
