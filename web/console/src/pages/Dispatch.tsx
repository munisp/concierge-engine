import { useEffect, useState } from "react";
import { api, dispatchStream, getTenantId } from "../api";
import type { Assignment } from "../types";

// Dispatch board v1 (M4): assignment list + live SSE feed
// (assignment / trip_status / geostamp events).
export default function Dispatch() {
  const [assignments, setAssignments] = useState<Assignment[]>([]);
  const [events, setEvents] = useState<string[]>([]);
  const [error, setError] = useState("");

  async function refresh() {
    try {
      setAssignments(await api.get<Assignment[]>("/v1/dispatch/assignments"));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    refresh();
    if (!getTenantId()) return;
    const es = dispatchStream(getTenantId());
    const onEvent = (e: MessageEvent) => {
      setEvents((prev) => [`${e.type}: ${e.data}`, ...prev].slice(0, 50));
      refresh();
    };
    es.addEventListener("assignment", onEvent);
    es.addEventListener("trip_status", onEvent);
    es.addEventListener("geostamp", onEvent);
    return () => es.close();
  }, []);

  async function advance(a: Assignment, status: string) {
    await api.post(`/v1/dispatch/assignments/${a.id}/status`, { status });
    await refresh();
  }

  const next: Record<string, string> = {
    ASSIGNED: "EN_ROUTE_TO_PICKUP",
    EN_ROUTE_TO_PICKUP: "ARRIVED",
    ARRIVED: "PASSENGER_ON_BOARD",
    PASSENGER_ON_BOARD: "IN_TRIP",
    IN_TRIP: "COMPLETED_TRIP",
  };

  return (
    <div>
      <h1>Dispatch board</h1>
      {error && <p className="error">{error}</p>}
      <div className="row">
        <div style={{ flex: 2 }}>
          {assignments.length === 0 && <p className="muted">No active assignments.</p>}
          {assignments.map((a) => (
            <div key={a.id} className="card">
              <span className="badge">{a.status}</span>{" "}
              <strong>Booking {a.booking_id.slice(0, 8)}</strong>
              <p className="muted">
                Resource {a.fleet_resource_id?.slice(0, 8) ?? "—"} · Crew {a.crew_member_id?.slice(0, 8) ?? "—"}
              </p>
              {next[a.status] && (
                <button onClick={() => advance(a, next[a.status])}>
                  Advance → {next[a.status].replaceAll("_", " ").toLowerCase()}
                </button>
              )}
            </div>
          ))}
        </div>
        <div>
          <h2>Live feed</h2>
          <div className="card">
            {events.length === 0 && <p className="muted">Waiting for events…</p>}
            {events.map((e, i) => <div key={i} className="board-event">{e}</div>)}
          </div>
        </div>
      </div>
    </div>
  );
}
