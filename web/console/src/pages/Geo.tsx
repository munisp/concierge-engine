import { useEffect, useState } from "react";
import { api } from "../api";

// Phase 11 geo console: service-area CRUD, serviceability tester, partner
// proximity — operator UI over the Phase 6 geo routes.

type ServiceArea = {
  id: string;
  name: string;
  polygon: [number, number][];
  created_at: string;
};

type PartnerDistance = {
  partner: { id: string; name: string; status: string };
  distance_km: number;
};

export default function Geo() {
  const [areas, setAreas] = useState<ServiceArea[]>([]);
  const [name, setName] = useState("");
  const [ring, setRing] = useState("6.41,3.39\n6.41,3.44\n6.45,3.44\n6.45,3.39");
  const [err, setErr] = useState("");

  const [checkLat, setCheckLat] = useState("6.4281");
  const [checkLng, setCheckLng] = useState("3.4219");
  const [checkResult, setCheckResult] = useState("");

  const [nearest, setNearest] = useState<PartnerDistance[]>([]);

  const load = () => api.get<ServiceArea[]>("/v1/geo/areas").then(setAreas).catch((e) => setErr(String(e)));
  useEffect(() => { load(); }, []);

  const create = async () => {
    setErr("");
    try {
      const polygon = ring.split("\n").map((l) => l.trim()).filter(Boolean)
        .map((l) => {
          const [lat, lng] = l.split(",").map(Number);
          if (Number.isNaN(lat) || Number.isNaN(lng)) throw new Error(`bad vertex: ${l}`);
          return [lat, lng] as [number, number];
        });
      await api.post("/v1/geo/areas", { name, polygon });
      setName("");
      await load();
    } catch (e) { setErr(String(e)); }
  };

  const check = async () => {
    setErr("");
    try {
      const res = await api.get<{ serviceable: boolean; areas: string[] }>(
        `/v1/geo/check?lat=${checkLat}&lng=${checkLng}`);
      setCheckResult(res.serviceable
        ? `serviceable — inside: ${res.areas.join(", ") || "(unnamed)"}`
        : "outside all service areas");
      const ranked = await api.get<PartnerDistance[]>(
        `/v1/geo/nearest-partners?lat=${checkLat}&lng=${checkLng}&limit=5`);
      setNearest(ranked);
    } catch (e) { setErr(String(e)); }
  };

  return (
    <section>
      <h1>Service areas</h1>
      {err && <p className="error">{err}</p>}

      <h2>Areas ({areas.length})</h2>
      <table>
        <thead><tr><th>Name</th><th>Vertices</th><th>Created</th></tr></thead>
        <tbody>
          {areas.map((a) => (
            <tr key={a.id}>
              <td>{a.name}</td>
              <td>{a.polygon.length}</td>
              <td>{new Date(a.created_at).toLocaleString()}</td>
            </tr>
          ))}
          {areas.length === 0 && <tr><td colSpan={3}>No service areas defined yet.</td></tr>}
        </tbody>
      </table>

      <h2>Define an area</h2>
      <p className="muted">One vertex per line as <code>lat,lng</code> (at least 3).</p>
      <input placeholder="Area name (e.g. Victoria Island)" value={name}
        onChange={(e) => setName(e.target.value)} />
      <textarea rows={5} value={ring} onChange={(e) => setRing(e.target.value)} />
      <button onClick={create} disabled={!name.trim()}>Create area</button>

      <h2>Serviceability check</h2>
      <input value={checkLat} onChange={(e) => setCheckLat(e.target.value)} placeholder="lat" />
      <input value={checkLng} onChange={(e) => setCheckLng(e.target.value)} placeholder="lng" />
      <button onClick={check}>Check point</button>
      {checkResult && <p><strong>{checkResult}</strong></p>}

      {nearest.length > 0 && (
        <>
          <h2>Nearest vetted partners</h2>
          <table>
            <thead><tr><th>Partner</th><th>Distance (km)</th></tr></thead>
            <tbody>
              {nearest.map((p) => (
                <tr key={p.partner.id}>
                  <td>{p.partner.name}</td>
                  <td>{Number.isFinite(p.distance_km) ? p.distance_km.toFixed(1) : "unlocated"}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
    </section>
  );
}
