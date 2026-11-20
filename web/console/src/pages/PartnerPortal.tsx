import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

// Partner portal (Phase 57): magic-link PWA for tour operators and DMCs.
// The token IS the credential (HMAC-signed, carries tenant+partner) — no
// login, no install, works on any phone. Accept/decline assignments and see
// commission earnings.

type Assignment = {
  id: string;
  booking_id: string;
  status: string;
  start_at?: string;
  end_at?: string;
  created_at: string;
  preferences?: Record<string, string>; // Phase 58: client comfort/dietary
  prep_tasks?: string[]; // Phase 60: actionable prep checklist
};

const PREF_LABELS: Record<string, string> = {
  "ride.mode": "Mode",
  "ride.music": "Music",
  "ride.temperature_c": "Temp",
  "ride.conversation": "Conversation",
  "ride.child_seat": "Child seat",
  "ride.luggage": "Luggage",
  "access.mobility": "Mobility",
  "comms.language": "Language",
  "dining.dietary": "Dietary",
  "dining.allergens": "Allergens",
};

function PrefChips({ prefs }: { prefs?: Record<string, string> }) {
  if (!prefs || Object.keys(prefs).length === 0) return null;
  return (
    <span>
      {Object.entries(prefs).map(([k, v]) => (
        <span key={k} className="chip" style={{ marginLeft: 6 }}>
          {(PREF_LABELS[k] ?? k) + ": " + v.replace(/_/g, " ")}
        </span>
      ))}
    </span>
  );
}

// Phase 60: the operator's prep checklist (child seat, wheelchair setup…)
// — same tasks the chauffeur sees, consent-filtered upstream.
function PrepList({ tasks }: { tasks?: string[] }) {
  if (!tasks || tasks.length === 0) return null;
  return (
    <ul className="muted" style={{ margin: "2px 0 0", paddingLeft: 18, fontSize: 13 }}>
      {tasks.map((t) => (
        <li key={t}>{t}</li>
      ))}
    </ul>
  );
}

// Phase 68: the partner's own bill-to-desk statements, as the operator
// sees them — verbatim reminders ledger included.
type PortalFolio = {
  id: string;
  period: string;
  currency: string;
  state: string; // OPEN | ISSUED | SETTLED
  total_minor: number;
  paid_minor: number; // Phase 70: derived from the payment ledger
  outstanding_minor: number;
  lines?: { booking_id: string; guest_name?: string; amount_minor: number }[];
  issued_at?: string;
  settled_at?: string;
  due_at?: string; // ISSUED only — derived from the tenant's payment terms
  overdue_days: number;
  reminders?: { n: number; channel: string; status: string; created_at: string }[];
};

type Portal = {
  partner_name: string;
  verticals: string[];
  assignments: Assignment[];
  settlement: {
    commission_pct: number;
    payout_rail: string;
    totals: Record<string, number>;
  };
  // Phase 63: demand this partner sourced (book-on-behalf).
  sourced_requests?: {
    id: string;
    state: string;
    guest_name?: string;
    items: number;
    created_at: string;
  }[];
  // Phase 68: bill-to-desk statements.
  folios?: PortalFolio[];
};

const minor = (n: number) => (n / 100).toFixed(2);

// Phase 68: one statement row — expandable lines + reminder history.
function FolioRow({ f }: { f: PortalFolio }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <tr onClick={() => setOpen(!open)} style={{ cursor: "pointer" }}>
        <td><strong>{f.period}</strong></td>
        <td>
          {minor(f.total_minor)} {f.currency}
          {f.paid_minor > 0 && (
            <div className="muted" style={{ fontSize: 12 }}>
              paid {minor(f.paid_minor)}
              {f.state === "ISSUED" && f.outstanding_minor > 0 && <> · owed {minor(f.outstanding_minor)}</>}
            </div>
          )}
        </td>
        <td>
          <strong>{f.state === "OPEN" ? "Accruing" : f.state}</strong>
          {f.state === "ISSUED" && f.due_at && (
            <span className="muted">
              {" "}· due {new Date(f.due_at).toLocaleDateString()}
              {f.overdue_days > 0 && <span className="error"> — {f.overdue_days}d overdue</span>}
            </span>
          )}
          {f.state === "SETTLED" && f.settled_at && (
            <span className="muted"> · paid {new Date(f.settled_at).toLocaleDateString()}</span>
          )}
        </td>
        <td className="muted">{f.lines?.length ?? 0} line(s){open ? " ▾" : " ▸"}</td>
      </tr>
      {open && (
        <tr>
          <td colSpan={4}>
            {(f.lines ?? []).map((l) => (
              <div key={l.booking_id} className="muted" style={{ fontSize: 13 }}>
                {l.guest_name || "Guest"} — {minor(l.amount_minor)} {f.currency}{" "}
                (<code>{l.booking_id.slice(0, 8)}…</code>)
              </div>
            ))}
            {(f.reminders ?? []).map((r) => (
              <div key={r.n} className="muted" style={{ fontSize: 13 }}>
                reminder {r.n} via {r.channel} — <strong>{r.status}</strong> —{" "}
                {new Date(r.created_at).toLocaleString()}
              </div>
            ))}
            {(f.lines ?? []).length === 0 && <span className="muted">No lines yet.</span>}
          </td>
        </tr>
      )}
    </>
  );
}

const STATUS_LABELS: Record<string, string> = {
  ASSIGNED: "Awaiting your answer",
  PARTNER_ACCEPTED: "Accepted",
  PARTNER_DECLINED: "Declined",
  EN_ROUTE_TO_PICKUP: "En route",
  ARRIVED: "Arrived",
  IN_TRIP: "In progress",
  COMPLETED_TRIP: "Completed",
  CANCELLED_TRIP: "Cancelled",
  NO_SHOW: "No-show",
};

async function tokenCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
  return (await resp.json()) as T;
}

export default function PartnerPortal() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [view, setView] = useState<Portal | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");

  const load = useCallback(async () => {
    if (!token) {
      setError("This portal link is missing its token.");
      return;
    }
    try {
      setView(await tokenCall<Portal>("GET", `/v1/portal/${encodeURIComponent(token)}`));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "portal unavailable");
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  async function respond(assignmentID: string, action: "accept" | "decline") {
    setBusy(assignmentID);
    try {
      await tokenCall("POST", `/v1/portal/${encodeURIComponent(token)}/respond`, {
        assignment_id: assignmentID,
        action,
      });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "response failed");
    } finally {
      setBusy("");
    }
  }

  if (error && !view) {
    return (
      <section>
        <h2>Partner portal</h2>
        <p className="error">{error}</p>
      </section>
    );
  }
  if (!view) return <section><p>Loading…</p></section>;

  const offered = view.assignments.filter((a) => a.status === "ASSIGNED");
  const rest = view.assignments.filter((a) => a.status !== "ASSIGNED");
  const t = view.settlement?.totals ?? {};

  return (
    <section>
      <h2>{view.partner_name}</h2>
      <p className="muted">
        {(view.verticals ?? []).join(", ")} · commission{" "}
        {view.settlement?.commission_pct ?? 0}% · payout {view.settlement?.payout_rail || "MANUAL"}
      </p>

      {offered.length > 0 && (
        <div className="card">
          <h3>New offers ({offered.length})</h3>
          {offered.map((a) => (
            <div key={a.id} className="row" style={{ justifyContent: "space-between", flexWrap: "wrap" }}>
              <span>
                Booking <code>{a.booking_id.slice(0, 8)}…</code>
                {a.start_at && <> · {new Date(a.start_at).toLocaleString()}</>}
                <PrefChips prefs={a.preferences} />
                <PrepList tasks={a.prep_tasks} />
              </span>
              <span className="row" style={{ gap: 6 }}>
                <button disabled={busy === a.id} onClick={() => void respond(a.id, "accept")}>
                  Accept
                </button>
                <button className="secondary" disabled={busy === a.id}
                  onClick={() => void respond(a.id, "decline")}>
                  Decline
                </button>
              </span>
            </div>
          ))}
        </div>
      )}

      <div className="card row" style={{ gap: 24 }}>
        <span>Pending: <strong>{minor(t["PENDING"] ?? 0)}</strong></span>
        <span>Settled: <strong>{minor(t["SETTLED"] ?? 0)}</strong></span>
        <span>In flight: <strong>{minor(t["IN_FLIGHT"] ?? 0)}</strong></span>
      </div>

      <h3>Assignments</h3>
      <table>
        <thead><tr><th>Booking</th><th>Window</th><th>Status</th></tr></thead>
        <tbody>
          {rest.map((a) => (
            <tr key={a.id}>
              <td><code>{a.booking_id.slice(0, 8)}…</code></td>
              <td>{a.start_at ? new Date(a.start_at).toLocaleString() : "—"}</td>
              <td><strong>{STATUS_LABELS[a.status] ?? a.status}</strong></td>
            </tr>
          ))}
          {rest.length === 0 && <tr><td colSpan={3} className="muted">No assignments yet.</td></tr>}
        </tbody>
      </table>
      {view.sourced_requests && view.sourced_requests.length > 0 && (
        <>
          <h3>Guests you referred</h3>
          <table>
            <thead><tr><th>Guest</th><th>Request</th><th>State</th><th>Placed</th></tr></thead>
            <tbody>
              {view.sourced_requests.map((r) => (
                <tr key={r.id}>
                  <td>{r.guest_name || "—"}</td>
                  <td><code>{r.id.slice(0, 8)}…</code></td>
                  <td><strong>{r.state}</strong></td>
                  <td>{new Date(r.created_at).toLocaleString()}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      {view.folios && view.folios.length > 0 && (
        <>
          <h3>Statements</h3>
          <table>
            <thead><tr><th>Period</th><th>Total</th><th>Status</th><th></th></tr></thead>
            <tbody>
              {view.folios.map((f) => <FolioRow key={f.id} f={f} />)}
            </tbody>
          </table>
        </>
      )}
      {error && <p className="error">{error}</p>}
    </section>
  );
}
