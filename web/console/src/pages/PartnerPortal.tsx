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
};

const PREF_LABELS: Record<string, string> = {
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

type Portal = {
  partner_name: string;
  verticals: string[];
  assignments: Assignment[];
  settlement: {
    commission_pct: number;
    payout_rail: string;
    totals: Record<string, number>;
  };
};

const minor = (n: number) => (n / 100).toFixed(2);

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
      {error && <p className="error">{error}</p>}
    </section>
  );
}
