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
  // Phase 35 (A11): review deep link + already-reviewed flag.
  review_url?: string;
  review_submitted?: boolean;
  // Phase 62: the rider's mark on this chauffeur (favorite / blocked).
  crew_member_id?: string;
  chauffeur_link?: string;
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

async function tokenCall<T>(method: string, path: string, body?: unknown): Promise<T> {
  const resp = await fetch(path, {
    method,
    headers: body === undefined ? undefined : { "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!resp.ok) throw new Error(`${resp.status}: ${(await resp.text()).slice(0, 200)}`);
  return (await resp.json()) as T;
}

// ReviewForm (Phase 27): after a completed trip the passenger rates it here.
// The track token authenticates the review — no login. Publishing is
// opt-in: without the consent checkbox the review is private feedback only.
function ReviewForm({ token, submitted }: { token: string; submitted?: boolean }) {
  const [rating, setRating] = useState(0);
  const [text, setText] = useState("");
  const [consent, setConsent] = useState(false);
  const [msg, setMsg] = useState("");
  const submit = async () => {
    try {
      await tokenCall("POST", `/v1/track/${token}/review`, {
        rating, text, publish_consent: consent,
      });
      setMsg("Thank you — your feedback is on file.");
    } catch (e) {
      setMsg(e instanceof Error ? e.message : "failed to submit");
    }
  };
  if (msg) return <p className="muted">{msg}</p>;
  if (submitted) return <p className="muted">Thank you — your review is already on file for this trip.</p>;
  return (
    <div className="card" id="review">
      <h2>How was your journey?</h2>
      <div className="row" style={{ gap: 6 }}>
        {[1, 2, 3, 4, 5].map((n) => (
          <button key={n} onClick={() => setRating(n)}
            style={{ fontSize: 20, opacity: rating >= n ? 1 : 0.3 }}>
            ★
          </button>
        ))}
      </div>
      <textarea rows={2} style={{ width: "100%", marginTop: 8 }}
        placeholder="One sentence we can learn from (optional)"
        value={text} onChange={(e) => setText(e.target.value)} />
      <label style={{ display: "block", margin: "8px 0" }}>
        <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />
        {" "}You may show my rating on the company's public page
      </label>
      <button disabled={rating === 0} onClick={() => void submit()}>Submit review</button>
    </div>
  );
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
            <h2>Live position</h2>            {view.position ? (
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

          {view.status === "COMPLETED_TRIP" && <ReviewForm token={token} submitted={view.review_submitted} />}
          {view.crew_member_id && view.driver_name && (
            <ChauffeurMark token={token} driverName={view.driver_name} current={view.chauffeur_link} />
          )}
          <JourneyTimeline token={token} />
          <SoundscapeCard token={token} />
          <RidePreferences token={token} />
        </>
      )}
    </div>
  );
}

// Phase 58: self-serve rider preferences on the tracking link. The client
// edits their own comfort profile (music, temperature, conversation…);
// curated values only — the server validates the vocabulary.
type RideProfile = {
  mode?: string;
  music?: string;
  music_volume?: string;
  temperature_c?: string;
  conversation?: string;
  route?: string;
  seat?: string;
  child_seat?: string;
  luggage?: string;
  mobility?: string;
  language?: string;
};

// Phase 60: one-tap ride modes — the server expands the preset bundle
// through the validated preference path; these labels are cosmetic only.
const RIDE_MODES = ["focus", "relax", "family", "celebrate"] as const;
const RIDE_MODE_LABELS: Record<string, string> = {
  focus: "🎯 Focus",
  relax: "🌿 Relax",
  family: "👨‍👩‍👧 Family",
  celebrate: "🎉 Celebrate",
};

const RIDE_PREF_FIELDS: { key: keyof RideProfile; label: string; options: string[] }[] = [
  { key: "conversation", label: "Conversation", options: ["quiet", "light", "chatty"] },
  { key: "route", label: "Route", options: ["fastest", "scenic", "no_tolls"] },
  { key: "seat", label: "Seat", options: ["front_passenger", "rear_left", "rear_right"] },
  { key: "child_seat", label: "Child seat", options: ["none", "infant", "toddler", "booster"] },
  { key: "luggage", label: "Luggage", options: ["light", "standard", "heavy"] },
  { key: "music_volume", label: "Music volume", options: ["low", "medium", "high"] },
];

const PREF_KEY_MAP: Record<string, string> = {
  conversation: "ride.conversation",
  route: "ride.route",
  seat: "ride.seat",
  child_seat: "ride.child_seat",
  luggage: "ride.luggage",
  music_volume: "ride.music_volume",
  music: "ride.music",
  temperature_c: "ride.temperature_c",
};

function RidePreferences({ token }: { token: string }) {
  const [profile, setProfile] = useState<RideProfile>({});
  const [saved, setSaved] = useState("");
  const [err, setErr] = useState("");

  const load = useCallback(async () => {
    try {
      setProfile(await tokenCall<RideProfile>("GET", `/v1/track/${token}/preferences`));
    } catch {
      // preferences are optional — the journey view is the point
    }
  }, [token]);

  useEffect(() => {
    void load();
  }, [load]);

  async function setPref(field: keyof RideProfile, value: string) {
    try {
      await tokenCall("PUT", `/v1/track/${token}/preferences/${PREF_KEY_MAP[field]}`, { value });
      setProfile({ ...profile, [field]: value });
      setSaved("Saved");
      setErr("");
      setTimeout(() => setSaved(""), 2000);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "save failed");
    }
  }

  async function applyMode(mode: string) {
    try {
      await tokenCall("POST", `/v1/track/${token}/ride-mode`, { mode });
      setSaved(`${RIDE_MODE_LABELS[mode] ?? mode} mode applied`);
      setErr("");
      await load();
      setTimeout(() => setSaved(""), 2500);
    } catch (e) {
      setErr(e instanceof Error ? e.message : "save failed");
    }
  }

  return (
    <div className="card" style={{ maxWidth: 480 }}>
      <h3>Your ride preferences</h3>
      <p className="muted">Set once — your chauffeur sees them on every trip.</p>
      <div className="row" style={{ flexWrap: "wrap", gap: 6, marginBottom: 8 }}>
        {RIDE_MODES.map((m) => (
          <button
            key={m}
            className={profile.mode === m ? "badge" : ""}
            onClick={() => void applyMode(m)}
          >
            {RIDE_MODE_LABELS[m]}
          </button>
        ))}
      </div>
      {RIDE_PREF_FIELDS.map((f) => (
        <div key={f.key} className="row" style={{ justifyContent: "space-between" }}>
          <span>{f.label}</span>
          <select
            value={profile[f.key] ?? ""}
            onChange={(e) => void setPref(f.key, e.target.value)}
          >
            <option value="">—</option>
            {f.options.map((o) => (
              <option key={o} value={o}>{o.replace(/_/g, " ")}</option>
            ))}
          </select>
        </div>
      ))}
      <div className="row" style={{ justifyContent: "space-between" }}>
        <span>Music</span>
        <input
          placeholder="afrobeats, jazz, off…"
          defaultValue={profile.music ?? ""}
          onBlur={(e) => e.target.value && void setPref("music", e.target.value)}
        />
      </div>
      <div className="row" style={{ justifyContent: "space-between" }}>
        <span>Temperature (°C)</span>
        <input
          type="number" min={16} max={26} style={{ width: 80 }}
          defaultValue={profile.temperature_c ?? ""}
          onBlur={(e) => e.target.value && void setPref("temperature_c", e.target.value)}
        />
      </div>
      {saved && <p>{saved}</p>}
      {err && <p className="error">{err}</p>}
    </div>
  );
}

// Phase 59: the rider's signature soundscape. Provenance is explicit —
// AI-composed tracks name the provider; a degraded render shows the
// curated fallback it actually is, never dressed up as composed.
type SoundscapeView = {
  status: string;
  title?: string;
  genre?: string;
  mood?: string;
  duration_seconds?: number;
  provider?: string;
  ai_composed: boolean;
  detail?: string;
  fallback_id?: string;
  has_audio: boolean;
};

function SoundscapeCard({ token }: { token: string }) {
  const [view, setView] = useState<SoundscapeView | null>(null);
  const [audioURL, setAudioURL] = useState("");

  useEffect(() => {
    (async () => {
      try {
        const r = await tokenCall<{ soundscape: SoundscapeView; audio_url: string }>(
          "GET", `/v1/track/${token}/soundscape`);
        setView(r.soundscape);
        setAudioURL(r.audio_url);
      } catch {
        // soundscape is a gift, not a guarantee — the journey view is the point
      }
    })();
  }, [token]);

  if (!view || view.status === "NONE") return null;
  return (
    <div className="card" style={{ maxWidth: 480 }}>
      <h3>Your ride soundtrack</h3>
      {view.status === "RENDERED" && audioURL ? (
        <>
          <p>
            <strong>{view.title || "Signature Ride"}</strong>
            {view.genre ? ` — ${view.genre}` : ""}
            {view.mood ? `, ${view.mood}` : ""}
          </p>
          <audio controls preload="none" src={audioURL} style={{ width: "100%" }} />
          <p className="muted">
            Composed by AI{view.provider ? ` (${view.provider})` : ""} from your
            ride preferences, just for this journey.
          </p>
        </>
      ) : view.status === "DEGRADED" && view.detail?.startsWith("rider-prefers") ? (
        <p className="muted">
          Quiet ride, per your preference — no soundtrack this time.
        </p>
      ) : view.status === "DEGRADED" ? (
        <p className="muted">
          Your personal composer is off duty today — your chauffeur has a
          curated {view.genre || "lounge"} playlist ready instead.
        </p>
      ) : (
        <p className="muted">Composing your soundtrack… check back shortly.</p>
      )}
    </div>
  );
}

// Phase 61: the rider's end-to-end journey — flight, ground transfer,
// hotel — as one honest timeline. Statuses and delays come from the GDS
// chain (labeled live / scheduled); we never invent an ETA.
type JourneyLeg = {
  id: string;
  kind: string;
  title: string;
  origin?: string;
  destination?: string;
  sched_start: string;
  sched_end?: string;
  actual_start?: string;
  status: string;
  delay_minutes?: number;
  source?: string;
};

const LEG_KIND_LABELS: Record<string, string> = {
  flight: "Flight",
  hotel: "Stay",
  ground: "Transfer",
  partner: "Partner",
};

function legTime(l: JourneyLeg): string {
  const t = l.actual_start || l.sched_start;
  return t ? new Date(t).toLocaleString() : "";
}

function JourneyTimeline({ token }: { token: string }) {
  const [legs, setLegs] = useState<JourneyLeg[]>([]);

  useEffect(() => {
    let alive = true;
    const load = async () => {
      try {
        const r = await tokenCall<{ legs: JourneyLeg[] }>("GET", `/v1/track/${token}/journey`);
        if (alive) setLegs(r.legs ?? []);
      } catch {
        // journey context is additive — the tracking view stands alone
      }
    };
    load();
    const iv = setInterval(load, 60_000);
    return () => { alive = false; clearInterval(iv); };
  }, [token]);

  if (legs.length === 0) return null;
  return (
    <div className="card" style={{ maxWidth: 480 }}>
      <h3>Your journey</h3>
      <ol style={{ listStyle: "none", padding: 0, margin: 0 }}>
        {legs.map((l) => (
          <li key={l.id} style={{ marginBottom: 10 }}>
            <span className="badge">{LEG_KIND_LABELS[l.kind] ?? l.kind}</span>{" "}
            <strong>{l.title}</strong>
            <br />
            <span className="muted">
              {legTime(l)}
              {l.delay_minutes ? ` · ${l.delay_minutes} min delay` : ""}
              {l.status === "DELAYED" ? " · rescheduled" : ""}
              {l.status === "ARRIVED" || l.status === "COMPLETED" ? " · done" : ""}
            </span>
            {l.kind === "flight" && l.source && (
              <span className="muted">
                {" "}· {l.source === "live" ? "live flight data" : "scheduled times"}
              </span>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}

// Phase 62: the rider marks this chauffeur for future matching — a
// favorite is a strong signal, a block is a hard exclusion. The mark
// lands on the rider's profile, never just on this trip.
function ChauffeurMark({ token, driverName, current }: {
  token: string; driverName: string; current?: string;
}) {
  const [mark, setMark] = useState(current ?? "");
  const [msg, setMsg] = useState("");

  async function set(kind: string) {
    setMsg("");
    try {
      await tokenCall("PUT", `/v1/track/${token}/chauffeur`, { kind });
      setMark(kind === "none" ? "" : kind);
      setMsg(kind === "favorite" ? `${driverName} is now your preferred chauffeur`
        : kind === "blocked" ? "Noted — you won't be matched again"
        : "Preference cleared");
    } catch { setMsg("Couldn't save right now — please try again later"); }
  }

  return (
    <div className="card" style={{ maxWidth: 480 }}>
      <h3>Your chauffeur, {driverName}</h3>
      <div className="row">
        <button className={mark === "favorite" ? "" : "secondary"} onClick={() => set("favorite")}>
          {mark === "favorite" ? "★ Preferred chauffeur" : "Ride together again"}
        </button>
        <button className={mark === "blocked" ? "danger" : "secondary"} onClick={() => set("blocked")}>
          {mark === "blocked" ? "Blocked" : "Don't match us again"}
        </button>
        {mark && <button className="secondary" onClick={() => set("none")}>Clear</button>}
      </div>
      {msg && <p className="muted">{msg}</p>}
    </div>
  );
}
