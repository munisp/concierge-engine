import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";

// Phase 12 receipts / SLA board: channel delivery health — per-status
// counts, recent failed deliveries correlated to cases, a message-id
// lifecycle lookup, and the full delivery evidence for one case.

type Receipt = {
  id: string;
  channel: string;
  external_ref: string;
  status: string;
  at: string;
};

type FailureRow = {
  receipt: Receipt;
  case_id?: string;
};

type CaseTimeline = {
  case_id: string;
  messages: Record<string, Receipt[]>;
};

const STATUSES = ["sent", "delivered", "read", "failed"];

export default function Receipts() {
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [failures, setFailures] = useState<FailureRow[]>([]);
  const [err, setErr] = useState("");

  const [ref, setRef] = useState("");
  const [lifecycle, setLifecycle] = useState<Receipt[] | null>(null);

  const [caseId, setCaseId] = useState("");
  const [timeline, setTimeline] = useState<CaseTimeline | null>(null);

  const load = () => {
    api.get<Record<string, number>>("/v1/channels/receipts-counts").then(setCounts).catch((e) => setErr(String(e)));
    api.get<FailureRow[]>("/v1/channels/receipts-failures?limit=20").then(setFailures).catch((e) => setErr(String(e)));
  };
  useEffect(() => { load(); }, []);

  const lookup = async () => {
    setErr(""); setLifecycle(null);
    try {
      setLifecycle(await api.get<Receipt[]>(`/v1/channels/receipts?external_ref=${encodeURIComponent(ref)}`));
    } catch (e) { setErr(String(e)); }
  };

  const loadCase = async () => {
    setErr(""); setTimeline(null);
    try {
      setTimeline(await api.get<CaseTimeline>(`/v1/channels/receipts-case?case_id=${encodeURIComponent(caseId)}`));
    } catch (e) { setErr(String(e)); }
  };

  const total = STATUSES.reduce((n, s) => n + (counts[s] ?? 0), 0);
  const deliveredPct = total > 0
    ? Math.round((100 * ((counts.delivered ?? 0) + (counts.read ?? 0))) / total)
    : null;

  return (
    <section>
      <h1>Receipts &amp; SLA</h1>
      {err && <p className="error">{err}</p>}

      <div className="cards">
        {STATUSES.map((s) => (
          <div key={s} className={`card ${s === "failed" && (counts.failed ?? 0) > 0 ? "card-alert" : ""}`}>
            <div className="card-value">{counts[s] ?? 0}</div>
            <div className="card-label">{s}</div>
          </div>
        ))}
        <div className="card">
          <div className="card-value">{deliveredPct === null ? "—" : `${deliveredPct}%`}</div>
          <div className="card-label">delivery rate</div>
        </div>
      </div>

      <h2>Failed deliveries</h2>
      {failures.length === 0 ? <p>No failed deliveries — every approved reply reached its channel.</p> : (
        <table>
          <thead><tr><th>Channel message</th><th>Case</th><th>At</th></tr></thead>
          <tbody>
            {failures.map((f) => (
              <tr key={f.receipt.id}>
                <td><code>{f.receipt.external_ref}</code></td>
                <td>{f.case_id
                  ? <Link to={`/cases?id=${f.case_id}`}>{f.case_id.slice(0, 8)}…</Link>
                  : "(unlinked — template send)"}</td>
                <td>{new Date(f.receipt.at).toLocaleString()}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      <h2>Message lifecycle lookup</h2>
      <div className="form-row">
        <input value={ref} onChange={(e) => setRef(e.target.value)}
          placeholder="channel message id (wamid…)" />
        <button onClick={lookup} disabled={!ref}>Lookup</button>
      </div>
      {lifecycle && (lifecycle.length === 0 ? <p>No receipts for that message id.</p> : (
        <ol>
          {lifecycle.map((r) => (
            <li key={r.id}><strong>{r.status}</strong> — {new Date(r.at).toLocaleString()} ({r.channel})</li>
          ))}
        </ol>
      ))}

      <h2>Case delivery evidence</h2>
      <div className="form-row">
        <input value={caseId} onChange={(e) => setCaseId(e.target.value)} placeholder="case id" />
        <button onClick={loadCase} disabled={!caseId}>Load</button>
      </div>
      {timeline && (Object.keys(timeline.messages).length === 0
        ? <p>No channel messages linked to this case yet.</p>
        : Object.entries(timeline.messages).map(([mref, rcs]) => (
          <div key={mref}>
            <code>{mref}</code>
            <ol>
              {rcs.map((r) => (
                <li key={r.id}><strong>{r.status}</strong> — {new Date(r.at).toLocaleString()}</li>
              ))}
            </ol>
          </div>
        )))}
    </section>
  );
}
