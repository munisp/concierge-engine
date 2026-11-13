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
    </section>
  );
}
