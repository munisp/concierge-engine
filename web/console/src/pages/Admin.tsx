import { useEffect, useState } from "react";
import { api, getTenantId, setTenantId } from "../api";
import type { Tenant } from "../types";

// Tenant admin v0 (M8): brand, pricing rules, SLAs, autonomy dial.
// The autonomy dial rises only on eval evidence (AI spec 7) — enforced by the API.
export default function Admin() {
  const [tenantId, setTenantIdState] = useState(getTenantId());
  const [tenant, setTenant] = useState<Tenant | null>(null);
  const [dial, setDial] = useState(0);
  const [evidence, setEvidence] = useState("");
  const [message, setMessage] = useState("");

  useEffect(() => {
    if (!tenantId) return;
    api.get<Tenant>("/v1/tenant")
      .then((t) => { setTenant(t); setDial(t.autonomy_dial); })
      .catch(() => setTenant(null));
  }, [tenantId]);

  function saveTenantId() {
    setTenantId(tenantId);
    setTenantIdState(tenantId);
    window.location.reload();
  }

  async function saveDial() {
    try {
      await api.put("/v1/tenant/autonomy", { dial, evidence_ref: evidence });
      setMessage(`Autonomy dial set to ${dial}.`);
    } catch (e) {
      setMessage(e instanceof Error ? e.message : String(e));
    }
  }

  return (
    <div>
      <h1>Tenant admin</h1>
      <div className="card">
        <label>Tenant ID</label>
        <input value={tenantId} onChange={(e) => setTenantIdState(e.target.value)}
          placeholder="paste tenant UUID" />
        <p />
        <button onClick={saveTenantId}>Use this tenant</button>
      </div>

      {tenant && (
        <>
          <div className="card">
            <h2>{tenant.name}</h2>
            <p className="muted">
              {tenant.archetype} · {tenant.slug} · currency {tenant.pricing_rules.currency} ·
              deposit {(tenant.pricing_rules.deposit_pct * 100).toFixed(0)}% ·
              platform fee {(tenant.pricing_rules.platform_fee_pct * 100).toFixed(1)}%
            </p>
          </div>
          <div className="card">
            <h2>AI autonomy dial</h2>
            <p className="muted">
              0 = draft-only · 1 = supervised · 2 = auto within limits · 3 = full auto within policy.
              Raised only on eval evidence.
            </p>
            <div className="row">
              <div>
                <label>Dial</label>
                <select value={dial} onChange={(e) => setDial(Number(e.target.value))}>
                  {[0, 1, 2, 3].map((d) => <option key={d} value={d}>{d}</option>)}
                </select>
              </div>
              <div>
                <label>Evidence reference (eval report)</label>
                <input value={evidence} onChange={(e) => setEvidence(e.target.value)} />
              </div>
            </div>
            <p />
            <button onClick={saveDial}>Save</button>
            {message && <p className="muted">{message}</p>}
          </div>
        </>
      )}
    </div>
  );
}
