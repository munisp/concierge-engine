import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api } from "../api";

// Aging (Phase 69): the operator's receivables queue — the Phase 67
// derived aging view as a first-class console page. Every number is
// computed server-side from live ISSUED folios against the tenant's
// payment terms on every load; there is no aging table to disagree
// with the statements. Currencies are never blended.

type AgingRow = {
  partner_id: string;
  partner_name: string;
  currency: string;
  terms_days: number;
  open_folios: number;
  current_minor: number;
  overdue_1_7_minor: number;
  overdue_8_30_minor: number;
  overdue_30_plus_minor: number;
  total_minor: number;
  oldest_overdue_days: number;
};

type AgingReport = { terms_days: number; rows: AgingRow[] };

type DunningReceipt = { sent: number; skipped: number };

const minor = (n: number) => (n / 100).toFixed(2);

export default function Aging() {
  const [report, setReport] = useState<AgingReport | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [receipt, setReceipt] = useState<DunningReceipt | null>(null);

  const load = useCallback(async () => {
    try {
      setReport(await api.get<AgingReport>("/v1/folios/aging"));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "aging unavailable");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function runDunning() {
    setBusy(true);
    setReceipt(null);
    try {
      // The receipt is verbatim: how many reminders the channel adapter
      // actually accepted vs recorded with a skip/failure status.
      setReceipt(await api.post<DunningReceipt>("/v1/dunning/run", {}));
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "dunning run failed");
    } finally {
      setBusy(false);
    }
  }

  if (error && !report) {
    return (
      <section>
        <h2>Receivables aging</h2>
        <p className="error">{error}</p>
      </section>
    );
  }
  if (!report) return <section><p>Loading…</p></section>;

  const rows = report.rows ?? [];
  // Per-currency totals — never blended across currencies.
  const byCurrency = new Map<string, { total: number; overdue: number; worst: number }>();
  for (const r of rows) {
    const overdue = r.overdue_1_7_minor + r.overdue_8_30_minor + r.overdue_30_plus_minor;
    const agg = byCurrency.get(r.currency) ?? { total: 0, overdue: 0, worst: 0 };
    agg.total += r.total_minor;
    agg.overdue += overdue;
    if (r.overdue_30_plus_minor > 0) agg.worst += r.overdue_30_plus_minor;
    byCurrency.set(r.currency, agg);
  }

  return (
    <section>
      <div className="row" style={{ justifyContent: "space-between", flexWrap: "wrap" }}>
        <h2>Receivables aging</h2>
        <span className="row" style={{ gap: 8, alignItems: "center" }}>
          {receipt && (
            <span className="muted">
              dunning: {receipt.sent} sent · {receipt.skipped} skipped/failed
            </span>
          )}
          <button disabled={busy} onClick={() => void runDunning()}>
            {busy ? "Running…" : "Run dunning sweep"}
          </button>
        </span>
      </div>
      <p className="muted">
        Issued partner statements vs {report.terms_days}-day payment terms — derived from live
        folios on every load. Manage a partner's statements under <Link to="/partners">Partners</Link>.
      </p>

      {byCurrency.size > 0 && (
        <div className="card row" style={{ gap: 24, flexWrap: "wrap" }}>
          {[...byCurrency.entries()].map(([cur, agg]) => (
            <span key={cur}>
              {cur}: <strong>{minor(agg.total)}</strong> outstanding
              {agg.overdue > 0 && <> · <span className="error">{minor(agg.overdue)} overdue</span></>}
              {agg.worst > 0 && <> · <span className="error">{minor(agg.worst)} 30+ days</span></>}
            </span>
          ))}
        </div>
      )}

      <table>
        <thead>
          <tr>
            <th>Partner</th><th>CCY</th><th>Open</th><th>Current</th>
            <th>1–7d</th><th>8–30d</th><th>30+d</th><th>Total</th><th>Oldest</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.partner_id + "/" + r.currency}>
              <td><strong>{r.partner_name || r.partner_id}</strong></td>
              <td>{r.currency}</td>
              <td>{r.open_folios}</td>
              <td>{minor(r.current_minor)}</td>
              <td className={r.overdue_1_7_minor > 0 ? "error" : "muted"}>{minor(r.overdue_1_7_minor)}</td>
              <td className={r.overdue_8_30_minor > 0 ? "error" : "muted"}>{minor(r.overdue_8_30_minor)}</td>
              <td className={r.overdue_30_plus_minor > 0 ? "error" : "muted"}>{minor(r.overdue_30_plus_minor)}</td>
              <td><strong>{minor(r.total_minor)}</strong></td>
              <td className={r.oldest_overdue_days > 0 ? "error" : "muted"}>
                {r.oldest_overdue_days > 0 ? `${r.oldest_overdue_days}d overdue` : "in terms"}
              </td>
            </tr>
          ))}
          {rows.length === 0 && (
            <tr><td colSpan={9} className="muted">Nothing outstanding — no issued, unsettled statements.</td></tr>
          )}
        </tbody>
      </table>
      {error && <p className="error">{error}</p>}
    </section>
  );
}
