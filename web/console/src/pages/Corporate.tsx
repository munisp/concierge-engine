import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Corporate accounts (Phase 2): EA bookers on consolidated monthly invoices.

type Account = {
  id: string;
  name: string;
  billing_email: string;
  payment_terms_days: number;
  credit_limit_minor: number;
  currency: string;
};

type Invoice = {
  id: string;
  account_id: string;
  period_start: string;
  period_end: string;
  total_minor: number;
  currency: string;
  state: string;
};

function fmtMinor(amountMinor: number, currency: string): string {
  return `${(amountMinor / 100).toFixed(2)} ${currency}`;
}

export default function Corporate() {
  const [accounts, setAccounts] = useState<Account[]>([]);
  const [invoices, setInvoices] = useState<Record<string, Invoice[]>>({});
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      const accs = await api.get<Account[]>("/v1/corporate/accounts");
      setAccounts(accs);
      const inv: Record<string, Invoice[]> = {};
      for (const a of accs) {
        inv[a.id] = await api.get<Invoice[]>(`/v1/corporate/accounts/${a.id}/invoices`);
      }
      setInvoices(inv);
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function create() {
    await api.post("/v1/corporate/accounts", { name, billing_email: email });
    setName("");
    setEmail("");
    await load();
  }

  async function generate(a: Account) {
    // Current month period; idempotency key = account + month.
    const now = new Date();
    const start = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), 1));
    const end = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth() + 1, 1));
    const key = `${a.id}:${start.toISOString().slice(0, 7)}`;
    await api.post(`/v1/corporate/accounts/${a.id}/invoices`, {
      period_start: start.toISOString(),
      period_end: end.toISOString(),
      idempotency_key: key,
    });
    await load();
  }

  async function markPaid(inv: Invoice) {
    await api.post(`/v1/corporate/invoices/${inv.id}/mark-paid`, {});
    await load();
  }

  return (
    <section>
      <h2>Corporate accounts</h2>
      {error && <p className="error">{error}</p>}
      <div className="card">
        <h3>New account</h3>
        <div className="row" style={{ gap: 8 }}>
          <input placeholder="Company" value={name} onChange={(e) => setName(e.target.value)} />
          <input placeholder="Billing email" value={email} onChange={(e) => setEmail(e.target.value)} />
          <button onClick={() => void create()} disabled={!name}>Create</button>
        </div>
      </div>
      {accounts.map((a) => (
        <div className="card" key={a.id}>
          <div className="row" style={{ justifyContent: "space-between" }}>
            <h3>{a.name}</h3>
            <button onClick={() => void generate(a)}>Generate monthly invoice</button>
          </div>
          <p className="muted">
            {a.billing_email} · net {a.payment_terms_days} days
            {a.credit_limit_minor > 0 && ` · credit limit ${fmtMinor(a.credit_limit_minor, a.currency)}`}
          </p>
          <table>
            <thead>
              <tr><th>Period</th><th>Total</th><th>State</th><th /></tr>
            </thead>
            <tbody>
              {(invoices[a.id] ?? []).map((inv) => (
                <tr key={inv.id}>
                  <td>{inv.period_start.slice(0, 10)} → {inv.period_end.slice(0, 10)}</td>
                  <td>{fmtMinor(inv.total_minor, inv.currency)}</td>
                  <td><strong>{inv.state}</strong></td>
                  <td>
                    {inv.state === "ISSUED" && (
                      <button onClick={() => void markPaid(inv)}>Mark paid</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ))}
    </section>
  );
}
