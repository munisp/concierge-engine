import { useEffect, useState } from "react";
import { api, dispatchStream, getTenantId } from "../api";
import type { Assignment, CrewMember, FleetResource } from "../types";

// Dispatch board v1 (M4): assignment list + live SSE feed
// (assignment / trip_status / geostamp events).
// Phase 32: fleet & crew management moved into the console — the
// onboarding "fleet"/"drivers" steps derive from these resources, so they
// must be completable here, not only via curl.
type Availability = {
  fleet_resource_id: string;
  label: string;
  free: boolean;
  busy: { assignment_id: string; booking_id: string; start_at: string; end_at: string }[];
};

export default function Dispatch() {
  const [assignments, setAssignments] = useState<Assignment[]>([]);
  const [events, setEvents] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [fleet, setFleet] = useState<FleetResource[]>([]);
  const [crew, setCrew] = useState<CrewMember[]>([]);
  const [fKind, setFKind] = useState("sedan");
  const [fLabel, setFLabel] = useState("");
  const [cName, setCName] = useState("");
  const [cCerts, setCCerts] = useState("");
  // Phase 22: scheduling — availability query across the fleet.
  const [availFrom, setAvailFrom] = useState("");
  const [availTo, setAvailTo] = useState("");
  const [avail, setAvail] = useState<Availability[] | null>(null);

  async function checkAvailability() {
    setError("");
    try {
      setAvail(await api.get<Availability[]>(
        `/v1/dispatch/availability?from=${encodeURIComponent(new Date(availFrom).toISOString())}` +
        `&to=${encodeURIComponent(new Date(availTo).toISOString())}`));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function refresh() {
    try {
      setAssignments(await api.get<Assignment[]>("/v1/dispatch/assignments"));
      setFleet(await api.get<FleetResource[]>("/v1/dispatch/fleet"));
      setCrew(await api.get<CrewMember[]>("/v1/dispatch/crew"));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function addFleet() {
    setError("");
    try {
      await api.post("/v1/dispatch/fleet", { kind: fKind.trim(), label: fLabel.trim() });
      setFLabel("");
      await refresh();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  async function addCrew() {
    setError("");
    try {
      await api.post("/v1/dispatch/crew", {
        full_name: cName.trim(),
        certificates: cCerts.split(",").map((s) => s.trim()).filter(Boolean),
      });
      setCName(""); setCCerts("");
      await refresh();
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

  // Phase 25: mint the passenger's journey-tracking link (read-only token).
  const [trackLinks, setTrackLinks] = useState<Record<string, string>>({});
  async function clientLink(a: Assignment) {
    try {
      const r = await api.post<{ token: string; url: string }>(
        `/v1/dispatch/assignments/${a.id}/track-link`);
      const full = `${window.location.origin}${r.url}`;
      setTrackLinks((prev) => ({ ...prev, [a.id]: full }));
      try { await navigator.clipboard.writeText(full); } catch { /* clipboard optional */ }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
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
      <div className="card">
        <h2>Fleet availability</h2>
        <div className="row">
          <input type="datetime-local" value={availFrom} onChange={(e) => setAvailFrom(e.target.value)} />
          <input type="datetime-local" value={availTo} onChange={(e) => setAvailTo(e.target.value)} />
          <button onClick={checkAvailability} disabled={!availFrom || !availTo}>Check</button>
        </div>
        {avail && (
          avail.length === 0 ? <p className="muted">No active fleet resources.</p> : (
            <table>
              <thead><tr><th>Resource</th><th>Status</th><th>Busy windows (turnaround-padded)</th></tr></thead>
              <tbody>
                {avail.map((r) => (
                  <tr key={r.fleet_resource_id}>
                    <td>{r.label || r.fleet_resource_id.slice(0, 8)}</td>
                    <td>{r.free ? "free" : "busy"}</td>
                    <td>{r.busy.map((b) =>
                      `${new Date(b.start_at).toLocaleString()} – ${new Date(b.end_at).toLocaleTimeString()}`
                    ).join("; ") || "—"}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )
        )}
      </div>
      <div className="card">
        <h2>Fleet &amp; crew</h2>
        <div className="row">
          <div style={{ flex: 1 }}>
            <h3 style={{ margin: "0 0 6px" }}>Fleet ({fleet.length})</h3>
            {fleet.map((f) => (
              <p key={f.id} className="muted" style={{ margin: "2px 0" }}>
                <strong>{f.label}</strong> · {f.kind}{!f.active && " · inactive"}
              </p>
            ))}
            <div className="row">
              <select value={fKind} onChange={(e) => setFKind(e.target.value)}>
                <option value="sedan">sedan</option>
                <option value="suv">suv</option>
                <option value="van">van</option>
                <option value="minibus">minibus</option>
                <option value="coach">coach</option>
              </select>
              <input placeholder="Label (e.g. Mercedes E · 12-AB-34)" value={fLabel}
                onChange={(e) => setFLabel(e.target.value)} />
              <button onClick={addFleet} disabled={!fLabel.trim()}>Add vehicle</button>
            </div>
          </div>
          <div style={{ flex: 1 }}>
            <h3 style={{ margin: "0 0 6px" }}>Crew ({crew.length})</h3>
            {crew.map((c) => (
              <p key={c.id} className="muted" style={{ margin: "2px 0" }}>
                <strong>{c.full_name}</strong>
                {c.certificates?.length > 0 && ` · ${c.certificates.join(", ")}`}
                {!c.active && " · inactive"}
              </p>
            ))}
            <div className="row">
              <input placeholder="Full name" value={cName}
                onChange={(e) => setCName(e.target.value)} />
              <input placeholder="Certificates (comma-sep, optional)" value={cCerts}
                onChange={(e) => setCCerts(e.target.value)} />
              <button onClick={addCrew} disabled={!cName.trim()}>Add crew</button>
            </div>
          </div>
        </div>
      </div>
      <div className="row">
        <div style={{ flex: 2 }}>
          {assignments.length === 0 && <p className="muted">No active assignments.</p>}
          {assignments.map((a) => (
            <div key={a.id} className="card">
              <span className="badge">{a.status}</span>{" "}
              <strong>Booking {a.booking_id.slice(0, 8)}</strong>
              <p className="muted">
                Resource {a.fleet_resource_id?.slice(0, 8) ?? "—"} · Crew {a.crew_member_id?.slice(0, 8) ?? "—"}
                {a.start_at && a.end_at && (
                  <>
                    {" · "}{new Date(a.start_at).toLocaleString()} – {new Date(a.end_at).toLocaleTimeString()}
                  </>
                )}
              </p>
              {next[a.status] && (
                <button onClick={() => advance(a, next[a.status])}>
                  Advance → {next[a.status].replaceAll("_", " ").toLowerCase()}
                </button>
              )}{" "}
              <button onClick={() => clientLink(a)}>Client link</button>
              {trackLinks[a.id] && (
                <p className="muted" style={{ wordBreak: "break-all" }}>
                  Tracking link (copied): {trackLinks[a.id]}
                </p>
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
