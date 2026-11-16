import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Phase 47: KYC / identity verification queue. Operators work the manual
// review lane here: open cases for clients, record document metadata (the
// document itself lives in the tenant's secure channel — the platform only
// ever stores the reference), and decide. Approving advances the client's
// kyc_status, which the booking gate reads on high-value quotes.

type KycCase = {
  id: string;
  client_id: string;
  level: string;
  status: string;
  provider: string;
  document_type?: string;
  document_ref?: string;
  decision_reason?: string;
  decided_by?: string;
  created_at: string;
  decided_at?: string;
};

const STATUSES = ["", "PENDING", "IN_REVIEW", "APPROVED", "REJECTED", "EXPIRED"];
const DOC_TYPES = ["passport", "national_id", "driving_licence", "company_registry", "proof_of_address", "source_of_wealth"];

export default function Kyc() {
  const [cases, setCases] = useState<KycCase[]>([]);
  const [status, setStatus] = useState("PENDING");
  const [err, setErr] = useState("");

  const [clientId, setClientId] = useState("");
  const [level, setLevel] = useState("STANDARD");
  const [idem, setIdem] = useState("");

  const [docType, setDocType] = useState("passport");
  const [docRef, setDocRef] = useState("");
  const [rejectReason, setRejectReason] = useState<Record<string, string>>({});

  const load = useCallback(() => {
    const qs = status ? `?status=${status}` : "";
    api.get<KycCase[]>(`/v1/kyc/cases${qs}`).then(setCases).catch((e) => setErr(String(e)));
  }, [status]);

  useEffect(load, [load]);

  const open = async () => {
    setErr("");
    try {
      await api.post("/v1/kyc/cases", { client_id: clientId, level, idempotency_key: idem || `console-${Date.now()}` });
      setClientId(""); setIdem("");
      load();
    } catch (e) { setErr(String(e)); }
  };

  const submit = async (id: string) => {
    setErr("");
    try {
      await api.post(`/v1/kyc/cases/${id}/submit`, { document_type: docType, document_ref: docRef });
      setDocRef("");
      load();
    } catch (e) { setErr(String(e)); }
  };

  const decide = async (id: string, approve: boolean) => {
    setErr("");
    try {
      await api.post(`/v1/kyc/cases/${id}/decide`, { approve, reason: rejectReason[id] || "", decided_by: "console" });
      load();
    } catch (e) { setErr(String(e)); }
  };

  return (
    <div>
      <h1>KYC verification queue</h1>
      {err && <p className="error">{err}</p>}

      <div className="card">
        <h2>Open case</h2>
        <div className="row">
          <input placeholder="client id" value={clientId} onChange={(e) => setClientId(e.target.value)} />
          <select value={level} onChange={(e) => setLevel(e.target.value)}>
            <option>STANDARD</option>
            <option>ENHANCED</option>
          </select>
          <input placeholder="idempotency key (optional)" value={idem} onChange={(e) => setIdem(e.target.value)} />
          <button onClick={open} disabled={!clientId}>Open</button>
        </div>
      </div>

      <div className="card">
        <h2>Queue</h2>
        <div className="row">
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            {STATUSES.map((s) => <option key={s} value={s}>{s || "ALL"}</option>)}
          </select>
          <span className="muted">Document review happens in your secure channel; record only the reference here.</span>
        </div>
        <table>
          <thead>
            <tr><th>Client</th><th>Level</th><th>Status</th><th>Document</th><th>Opened</th><th>Actions</th></tr>
          </thead>
          <tbody>
            {cases.map((c) => (
              <tr key={c.id}>
                <td className="mono">{c.client_id.slice(0, 8)}…</td>
                <td>{c.level}</td>
                <td>{c.status}</td>
                <td>{c.document_type ? `${c.document_type} (${c.document_ref})` : "—"}</td>
                <td>{new Date(c.created_at).toLocaleDateString()}</td>
                <td>
                  {c.status === "PENDING" && (
                    <span className="row">
                      <select value={docType} onChange={(e) => setDocType(e.target.value)}>
                        {DOC_TYPES.map((d) => <option key={d}>{d}</option>)}
                      </select>
                      <input placeholder="document ref" value={docRef} onChange={(e) => setDocRef(e.target.value)} />
                      <button onClick={() => submit(c.id)} disabled={!docRef}>Mark submitted</button>
                    </span>
                  )}
                  {c.status === "IN_REVIEW" && (
                    <span className="row">
                      <input placeholder="reject reason" value={rejectReason[c.id] || ""}
                        onChange={(e) => setRejectReason({ ...rejectReason, [c.id]: e.target.value })} />
                      <button onClick={() => decide(c.id, true)}>Approve</button>
                      <button onClick={() => decide(c.id, false)} disabled={!rejectReason[c.id]}>Reject</button>
                    </span>
                  )}
                  {c.decision_reason && <span className="muted">{c.decision_reason}</span>}
                </td>
              </tr>
            ))}
            {cases.length === 0 && <tr><td colSpan={6} className="muted">No cases{status ? ` with status ${status}` : ""}.</td></tr>}
          </tbody>
        </table>
      </div>
    </div>
  );
}
