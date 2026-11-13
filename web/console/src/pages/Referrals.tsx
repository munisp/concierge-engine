import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Referral network (Phase 3): cross-tenant demand handoffs with commission
// settlement — the network-effect KPI surface.

type Referral = {
  id: string;
  referring_tenant_id: string;
  receiving_tenant_id: string;
  client_name: string;
  client_contact: string;
  request_summary: string;
  est_value_minor: number;
  currency: string;
  fee_pct: number;
  state: string;
  commission_minor?: number;
};

type Metrics = {
  offered: number;
  accepted: number;
  settled: number;
  accept_rate: number;
  commission_earned_minor: number;
  commission_paid_minor: number;
};

export default function Referrals() {
  const [referrals, setReferrals] = useState<Referral[]>([]);
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [receiver, setReceiver] = useState("");
  const [clientName, setClientName] = useState("");
  const [clientContact, setClientContact] = useState("");
  const [summary, setSummary] = useState("");
  const [estValue, setEstValue] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setReferrals(await api.get<Referral[]>("/v1/referrals"));
      setMetrics(await api.get<Metrics>("/v1/referrals/metrics"));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function offer() {
    try {
      await api.post("/v1/referrals", {
        receiving_tenant_id: receiver,
        client_name: clientName,
        client_contact: clientContact,
        request_summary: summary,
        est_value_minor: Math.round(parseFloat(estValue) * 100),
        currency: "EUR",
        idempotency_key: `console-${Date.now()}`,
      });
      setClientName("");
      setClientContact("");
      setSummary("");
      setEstValue("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "offer failed");
    }
  }

  async function act(id: string, action: "accept" | "decline") {
    try {
      await api.post(`/v1/referrals/${id}/${action}`, action === "decline" ? { reason: "console" } : {});
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : `${action} failed`);
    }
  }

  return (
    <div>
      <h1>Referral network</h1>
      {error && <p className="error">{error}</p>}
      {metrics && (
        <p className="kpis">
          Offered {metrics.offered} · Accepted {metrics.accepted} · Settled {metrics.settled} ·
          Accept rate {(metrics.accept_rate * 100).toFixed(0)}% ·
          Earned €{(metrics.commission_earned_minor / 100).toFixed(2)} ·
          Paid €{(metrics.commission_paid_minor / 100).toFixed(2)}
        </p>
      )}

      <section>
        <h2>Offer a referral</h2>
        <div className="form-row">
          <input placeholder="receiving tenant id" value={receiver} onChange={(e) => setReceiver(e.target.value)} />
          <input placeholder="client name" value={clientName} onChange={(e) => setClientName(e.target.value)} />
          <input placeholder="client contact" value={clientContact} onChange={(e) => setClientContact(e.target.value)} />
          <input placeholder="request summary" value={summary} onChange={(e) => setSummary(e.target.value)} />
          <input placeholder="est. value (EUR)" value={estValue} onChange={(e) => setEstValue(e.target.value)} />
          <button onClick={() => void offer()} disabled={!receiver || !clientContact || !summary}>
            Offer
          </button>
        </div>
      </section>

      <section>
        <h2>Referrals</h2>
        <table>
          <thead>
            <tr><th>Client</th><th>Summary</th><th>Est. value</th><th>Fee</th><th>State</th><th>Commission</th><th /></tr>
          </thead>
          <tbody>
            {referrals.map((r) => (
              <tr key={r.id}>
                <td>{r.client_name}</td>
                <td>{r.request_summary}</td>
                <td>€{(r.est_value_minor / 100).toFixed(2)}</td>
                <td>{(r.fee_pct * 100).toFixed(0)}%</td>
                <td>{r.state}</td>
                <td>{r.commission_minor ? `€${(r.commission_minor / 100).toFixed(2)}` : "—"}</td>
                <td>
                  {r.state === "OFFERED" && (
                    <>
                      <button onClick={() => void act(r.id, "accept")}>Accept</button>
                      <button onClick={() => void act(r.id, "decline")}>Decline</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
