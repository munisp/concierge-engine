import { useCallback, useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";

// Chauffeur app v1 (PWA): magic-link trip view. The token IS the credential
// (HMAC-signed, carries tenant+assignment) — no login, no tenant header.

type TripView = {
  assignment_id: string;
  status: string;
  allowed_next: string[];
  pickup_address: string;
  dropoff_address?: string;
  pickup_at: number;
  passengers: number;
  vehicle_class?: string;
  client_name: string;
  notes?: string;
  preferences?: Record<string, string>; // Phase 58 rider comfort block
};

const PREF_LABELS: Record<string, string> = {
  "ride.music": "Music",
  "ride.music_volume": "Volume",
  "ride.temperature_c": "Temperature",
  "ride.conversation": "Conversation",
  "ride.route": "Route",
  "ride.seat": "Seat",
  "ride.child_seat": "Child seat",
  "ride.luggage": "Luggage",
  "access.mobility": "Mobility",
  "comms.language": "Language",
};

function prefText(key: string, value: string): string {
  return key === "ride.temperature_c" ? `${value}°C` : value.replace(/_/g, " ");
}

const STATUS_LABELS: Record<string, string> = {
  EN_ROUTE_TO_PICKUP: "En route to pickup",
  ARRIVED: "Arrived",
  PASSENGER_ON_BOARD: "Passenger on board",
  IN_TRIP: "Start trip",
  COMPLETED_TRIP: "Complete trip",
  NO_SHOW: "No-show",
  CANCELLED_TRIP: "Cancel trip",
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

export default function Chauffeur() {
  const [params] = useSearchParams();
  const token = params.get("token") ?? "";
  const [trip, setTrip] = useState<TripView | null>(null);
  const [error, setError] = useState("");
  const [geoMsg, setGeoMsg] = useState("");

  const load = useCallback(async () => {
    try {
      setTrip(await tokenCall<TripView>("GET", `/v1/trips/${token}`));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "failed to load trip");
    }
  }, [token]);

  useEffect(() => {
    if (token) void load();
  }, [token, load]);

  async function advance(status: string) {
    await tokenCall("POST", `/v1/trips/${token}/status`, { status });
    await load();
  }

  function shareLocation() {
    if (!navigator.geolocation) {
      setGeoMsg("geolocation unavailable");
      return;
    }
    navigator.geolocation.getCurrentPosition(
      async (pos) => {
        await tokenCall("POST", `/v1/trips/${token}/geostamps`, {
          kind: "PING",
          lat: pos.coords.latitude,
          lng: pos.coords.longitude,
          accuracy_m: pos.coords.accuracy,
        });
        setGeoMsg("location shared");
      },
      () => setGeoMsg("location permission denied"),
      { enableHighAccuracy: true }
    );
  }

  if (!token) return <p className="muted">Missing trip token.</p>;
  if (error) return <p className="error">Trip unavailable: {error}</p>;
  if (!trip) return <p className="muted">Loading trip…</p>;

  return (
    <section className="card" style={{ maxWidth: 480 }}>
      <h2>Trip</h2>
      <dl>
        <dt>Guest</dt>
        <dd>{trip.client_name}</dd>
        <dt>Pickup</dt>
        <dd>
          {trip.pickup_address}
          {trip.pickup_at > 0 && (
            <> · {new Date(trip.pickup_at * 1000).toLocaleString()}</>
          )}
        </dd>
        {trip.dropoff_address && (
          <>
            <dt>Dropoff</dt>
            <dd>{trip.dropoff_address}</dd>
          </>
        )}
        <dt>Passengers</dt>
        <dd>
          {trip.passengers}
          {trip.vehicle_class ? ` · ${trip.vehicle_class}` : ""}
        </dd>
        {trip.notes && (
          <>
            <dt>Notes</dt>
            <dd>{trip.notes}</dd>
          </>
        )}
        {trip.preferences && Object.keys(trip.preferences).length > 0 && (
          <>
            <dt>Rider preferences</dt>
            <dd>
              {Object.entries(trip.preferences).map(([k, v]) => (
                <span key={k} className="chip" style={{ marginRight: 6 }}>
                  {(PREF_LABELS[k] ?? k) + ": " + prefText(k, v)}
                </span>
              ))}
            </dd>
          </>
        )}
        <dt>Status</dt>
        <dd>
          <strong>{trip.status}</strong>
        </dd>
      </dl>
      <div className="row" style={{ flexWrap: "wrap", gap: 8 }}>
        {trip.allowed_next.map((s) => (
          <button key={s} onClick={() => void advance(s)}>
            {STATUS_LABELS[s] ?? s}
          </button>
        ))}
      </div>
      <div className="row" style={{ marginTop: 12 }}>
        <button className="secondary" onClick={shareLocation}>
          Share location
        </button>
        {geoMsg && <span className="muted">{geoMsg}</span>}
      </div>
    </section>
  );
}
