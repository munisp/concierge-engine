import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

// Client journey tracking (Phase 25): the passenger's live view of their own
// trip. The token IS the credential (HMAC-signed, carries tenant+assignment)
// — no login, no tenant header. Read-only: a track token can never advance
// status or stamp positions (the server rejects it at parse time).

type TrackPosition = { lat: number; lng: number; kind: string; at: string };

type TrackView = {
  assignment_id: string;
  status: string;
  pickup_address: string;
  dropoff_address?: string;
  pickup_at: number;
  vehicle_class?: string;
  vehicle_label?: string;
  driver_name?: string;
  window_start?: string;
  window_end?: string;
  position?: TrackPosition;
};

const STEPS = [
  "ASSIGNED",
  "EN_ROUTE_TO_PICKUP",
  "ARRIVED",
  "PASSENGER_ON_BOARD",
  "IN_TRIP",
  "COMPLETED_TRIP",
];

const STEP_LABELS: Record<string, string> = {
  ASSIGNED: "Chauffeur assigned",
  EN_ROUTE_TO_PICKUP: "On the way to you",
  ARRIVED: "Arrived at pickup",
  PASSENGER_ON_BOARD: "You're on board",
  IN_TRIP: "Journey in progress",
  COMPLETED_TRIP: "Journey complete",
  CANCELLED_TRIP: "Cancelled",
  NO_SHOW: "No-show",
};

async function tokenCall<T>(method: string, path: string): Promise<T> {
  const resp = await fetch(path, { method });
  if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
  return (await resp.json()) as T;
}

function age(at: string): string {
  const secs = Math.max(0, Math.floor((Date.now() - new Date(at).getTime()) / 1000));
  if (secs < 60) return `${secs}s ago`;
  return `${Math.floor(secs / 60)}m ago`;
}

export default function Track() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [view, setView] = useState<TrackView | null>(null);
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setView(await tokenCall<TrackView>("GET", `/v1/track/${token}`));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load journey");
    }
  }, [token]);

  // Poll every 5s — the server view is cheap (one assignment + latest stamp).
  useEffect(() => {
    if (!token) return;
    void load();
    const t = setInterval(() => void load(), 5000);
    return () => clearInterval(t);
  }, [token, load]);

  if (!token) return <div><h1>Track your journey</h1><p className="error">No tracking token in this link.</p></div>;

  const cancelled = view?.status === "CANCELLED_TRIP" || view?.status === "NO_SHOW";
  const stepIdx = view ? STEPS.indexOf(view.status) : -1;

  return (
    <div>
      <h1>Track your journey</h1>
      {error && <p className="error">{error}</p>}
      {!view && !error && <p className="muted">Loading…</p>}
      {view && (
        <>
          <div className="card">
            <h2>{cancelled ? STEP_LABELS[view.status] : (STEP_LABELS[view.status] ?? view.status)}</h2>
            {!cancelled && (
              <div className="row" style={{ flexWrap: "wrap", gap: 6 }}>
                {STEPS.map((s, i) => (
                  <span key={s} className="badge" style={{
                    opacity: i <= stepIdx ? 1 : 0.35,
                    background: i === stepIdx ? "var(--accent, #4c8dff)" : undefined,
                  }}>
                    {STEP_LABELS[s]}
                  </span>
                ))}
              </div>
            )}
            <p>
              <strong>Pickup:</strong> {view.pickup_address || "—"}
              {view.pickup_at > 0 && <> at {new Date(view.pickup_at * 1000).toLocaleString()}</>}
            </p>
            {view.dropoff_address && <p><strong>Drop-off:</strong> {view.dropoff_address}</p>}
            {(view.vehicle_label || view.vehicle_class) && (
              <p><strong>Vehicle:</strong> {view.vehicle_label ?? view.vehicle_class}</p>
            )}
            {view.driver_name && <p><strong>Your chauffeur:</strong> {view.driver_name}</p>}
            {view.window_start && view.window_end && (
              <p className="muted">
                Service window: {new Date(view.window_start).toLocaleString()} –{" "}
                {new Date(view.window_end).toLocaleTimeString()}
              </p>
            )}
          </div>

          <div className="card">
            <h2>Live position</h2>
            {view.position ? (
              <>
                <p>
                  {view.position.lat.toFixed(5)}, {view.position.lng.toFixed(5)}{" "}
                  <span className="muted">({view.position.kind.replaceAll("_", " ").toLowerCase()}, {age(view.position.at)})</span>
                </p>
                <p>
                  <a
                    href={`https://www.openstreetmap.org/?mlat=${view.position.lat}&mlon=${view.position.lng}#map=16/${view.position.lat}/${view.position.lng}`}
                    target="_blank" rel="noreferrer"
                  >
                    Open in map
                  </a>
                </p>
              </>
            ) : (
              <p className="muted">
                Position pending — your chauffeur hasn't shared a location yet.
                This page updates automatically once they do.
              </p>
            )}
          </div>
        </>
      )}
    </div>
  );
}
