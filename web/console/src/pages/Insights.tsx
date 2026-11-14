// Phase 30: tenant insights — the operational KPI dashboard: bookings and
// revenue, service mix, acquisition channels, fleet utilization, driver
// performance, review quality. Every number traces to real store rows.
import { useEffect, useState } from "react";
import { api, getTenantId } from "../api";

type Overview = {
  days: number; from: string; to: string;
  totals: {
    bookings_created: number; bookings_confirmed: number; trips_completed: number;
    gmv_minor: number; collected_minor: number; currency: string;
    new_clients: number; review_count: number; review_avg: string;
  };
  by_service: { service_id: string; name: string; vertical: string; bookings: number; gmv_minor: number }[];
  by_channel: { channel: string; kind: string; count: number; value_minor: number }[];
  fleet: { resource_id: string; label: string; kind: string; active: boolean;
    assignments: number; completed: number; hours_booked: number; utilization_pct: number }[];
  drivers: { crew_id: string; name: string; active: boolean; assignments: number;
    completed: number; review_count: number; review_avg: number }[];
  notes: string[];
};

const money = (minor: number, cur: string) => `${cur} ${(minor / 100).toFixed(2)}`;

export default function Insights() {
  const [days, setDays] = useState(30);
  const [o, setO] = useState<Overview | null>(null);
  const [err, setErr] = useState("");
  const tenant = getTenantId();

  useEffect(() => {
    if (!tenant) return;
    api.get<Overview>(`/v1/analytics/overview?days=${days}`).then(setO).catch((e) => setErr(String(e)));
  }, [tenant, days]);

  if (!tenant) return <p className="muted">Set a tenant ID first (Settings).</p>;
  if (err) return <p className="error">{err}</p>;
  if (!o) return <p className="muted">Loading…</p>;
  const t = o.totals;

  return (
    <div>
      <h1>Insights</h1>
      <p className="muted">
        Operational KPIs from the live store — every figure traces to real
        bookings, postings, assignments and reviews.
        Window: <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
          {[7, 30, 90, 365].map((d) => <option key={d} value={d}>{d} days</option>)}
        </select>
      </p>

      <div className="card" style={{ display: "grid", gridTemplateColumns: "repeat(auto-fill,minmax(150px,1fr))", gap: 12 }}>
        {[
          ["Bookings", t.bookings_created],
          ["Confirmed", t.bookings_confirmed],
          ["Trips done", t.trips_completed],
          ["GMV", money(t.gmv_minor, t.currency)],
          ["Collected", money(t.collected_minor, t.currency)],
          ["New clients", t.new_clients],
          ["Reviews", t.review_count],
          ["Avg rating", t.review_avg || "—"],
        ].map(([k, v]) => (
          <div key={String(k)}>
            <div className="muted" style={{ fontSize: 12 }}>{k}</div>
            <div style={{ fontSize: 22, fontWeight: 600 }}>{v}</div>
          </div>
        ))}
      </div>

      <div className="card">
        <h2>Service mix</h2>
        {o.by_service.length === 0 && <p className="muted">No bookings in window.</p>}
        {o.by_service.length > 0 && (
          <table><thead><tr><th>Service</th><th>Vertical</th><th>Bookings</th><th>GMV</th></tr></thead>
            <tbody>{o.by_service.map((s) => (
              <tr key={s.vertical}><td>{s.name}</td><td>{s.vertical}</td><td>{s.bookings}</td>
                <td>{money(s.gmv_minor, t.currency)}</td></tr>
            ))}</tbody></table>
        )}
      </div>

      <div className="card">
        <h2>Fleet (vehicles &amp; resources)</h2>
        {o.fleet.length === 0 && <p className="muted">No fleet resources registered.</p>}
        {o.fleet.length > 0 && (
          <table><thead><tr><th>Resource</th><th>Kind</th><th>Assignments</th><th>Completed</th>
            <th>Hours booked</th><th>Utilization</th></tr></thead>
            <tbody>{o.fleet.map((f) => (
              <tr key={f.resource_id}>
                <td>{f.label}{!f.active && " (inactive)"}</td><td>{f.kind}</td>
                <td>{f.assignments}</td><td>{f.completed}</td>
                <td>{f.hours_booked.toFixed(1)}h</td><td>{f.utilization_pct.toFixed(0)}%</td>
              </tr>
            ))}</tbody></table>
        )}
      </div>

      <div className="card">
        <h2>Drivers (crew)</h2>
        {o.drivers.length === 0 && <p className="muted">No crew registered.</p>}
        {o.drivers.length > 0 && (
          <table><thead><tr><th>Driver</th><th>Assignments</th><th>Completed</th>
            <th>Reviews</th><th>Avg rating</th></tr></thead>
            <tbody>{o.drivers.map((d) => (
              <tr key={d.crew_id}>
                <td>{d.name}{!d.active && " (inactive)"}</td>
                <td>{d.assignments}</td><td>{d.completed}</td>
                <td>{d.review_count}</td><td>{d.review_count ? d.review_avg.toFixed(1) : "—"}</td>
              </tr>
            ))}</tbody></table>
        )}
      </div>

      <div className="card">
        <h2>Acquisition channels</h2>
        {o.by_channel.length === 0 && <p className="muted">No attributed events yet.</p>}
        {o.by_channel.length > 0 && (
          <table><thead><tr><th>Channel</th><th>Event</th><th>Count</th><th>Value</th></tr></thead>
            <tbody>{o.by_channel.map((c, i) => (
              <tr key={i}><td>{c.channel}</td><td>{c.kind}</td><td>{c.count}</td>
                <td>{money(c.value_minor, t.currency)}</td></tr>
            ))}</tbody></table>
        )}
        <p className="muted" style={{ fontSize: 12 }}>{o.notes.join(" ")}</p>
      </div>
    </div>
  );
}
