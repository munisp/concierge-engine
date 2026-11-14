import { useState } from "react";
import { api } from "../api";
import type { Quote, ServiceRequest } from "../types";
import { money } from "../types";

// Booking capture surface (M1/M3): instant-quote widget flow —
// capture details -> service request -> instant quote -> payment link.
//
// Phase 14: acquisition params — ?ref=VIP-XXXX / ?channel= / ?campaign=
// (utm_source/utm_campaign accepted as aliases). Tracked as a page_view
// attribution event once the client resolves, so first-touch inheritance
// attributes the request and booking to the source.
function acquisitionParams(): { channel: string; campaign: string } {
  const q = new URLSearchParams(window.location.search);
  const ref = q.get("ref") ?? "";
  const channel = q.get("channel") ?? q.get("utm_source") ?? (ref ? "referral" : "");
  const campaign = ref ? `ref:${ref}` : (q.get("campaign") ?? q.get("utm_campaign") ?? "");
  return { channel, campaign };
}

export default function Capture() {
  const [name, setName] = useState("");
  const [phone, setPhone] = useState("");
  const [pickup, setPickup] = useState("");
  const [dropoff, setDropoff] = useState("");
  const [pickupAt, setPickupAt] = useState("");
  const [passengers, setPassengers] = useState(1);
  const [vehicleClass, setVehicleClass] = useState("business");
  const [quote, setQuote] = useState<Quote | null>(null);
  const [paymentUrl, setPaymentUrl] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit() {
    setBusy(true);
    setError("");
    try {
      const resolved = await api.post<{ client: { id: string } }>("/v1/clients/resolve", {
        phone, name,
      });
      const acq = acquisitionParams();
      if (acq.channel || acq.campaign) {
        // Advisory: attribution never blocks capture.
        api.post("/v1/attribution", {
          kind: "page_view", channel: acq.channel, campaign: acq.campaign,
          client_id: resolved.client.id,
        }).catch(() => {});
      }
      const sr = await api.post<ServiceRequest>("/v1/requests", {
        client_id: resolved.client.id,
        idempotency_key: `web-${Date.now()}`,
        items: [{
          vertical: "CHAUFFEUR_POINT_TO_POINT",
          pickup_address: pickup,
          dropoff_address: dropoff,
          pickup_at: Math.floor(new Date(pickupAt).getTime() / 1000),
          passengers,
          vehicle_class: vehicleClass,
        }],
      });
      const resp = await fetch(`/v1/requests/${sr.id}/quote`, {
        method: "POST",
        headers: { "X-Tenant-ID": localStorage.getItem("ce_tenant_id") ?? "" },
      });
      if (resp.status === 202) {
        setError("This itinerary needs a human quote — a concierge will reply within the hour.");
        return;
      }
      const q = (await resp.json()) as Quote;
      setQuote(q);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }

  async function pay() {
    if (!quote) return;
    const intent = await api.post<{ hosted_url: string }>("/v1/payments/links", {
      quote_id: quote.id, mode: "FULL", idempotency_key: `pay-${quote.id}`,
    });
    setPaymentUrl(intent.hosted_url);
  }

  return (
    <div>
      <h1>Arrange your journey</h1>
      <p className="muted">One price, one payment, one timeline.</p>
      <div className="card">
        <div className="row">
          <div><label>Name</label><input value={name} onChange={(e) => setName(e.target.value)} /></div>
          <div><label>Phone (WhatsApp)</label><input value={phone} onChange={(e) => setPhone(e.target.value)} /></div>
        </div>
        <div className="row">
          <div><label>Pickup</label><input value={pickup} onChange={(e) => setPickup(e.target.value)} /></div>
          <div><label>Destination</label><input value={dropoff} onChange={(e) => setDropoff(e.target.value)} /></div>
        </div>
        <div className="row">
          <div><label>Pickup time</label><input type="datetime-local" value={pickupAt} onChange={(e) => setPickupAt(e.target.value)} /></div>
          <div><label>Passengers</label><input type="number" min={1} value={passengers} onChange={(e) => setPassengers(Number(e.target.value))} /></div>
          <div>
            <label>Vehicle class</label>
            <select value={vehicleClass} onChange={(e) => setVehicleClass(e.target.value)}>
              <option value="business">Business</option>
              <option value="first">First</option>
              <option value="suv">SUV</option>
            </select>
          </div>
        </div>
        <p />
        <button disabled={busy || !name || !phone || !pickup || !pickupAt} onClick={submit}>
          Get instant quote
        </button>
        {error && <p className="error">{error}</p>}
      </div>

      {quote && (
        <div className="card">
          <h2>Your quote</h2>
          {quote.lines.map((l, i) => (
            <p key={i}>{l.description} — <strong>{money(l.amount_minor, quote.currency)}</strong></p>
          ))}
          <h3>Total: {money(quote.total_minor, quote.currency)}</h3>
          <p className="muted">Valid until {new Date(quote.expires_at).toLocaleString()}</p>
          {!paymentUrl
            ? <button onClick={pay}>Confirm & pay</button>
            : <p>Complete payment securely: <a href={paymentUrl}>{paymentUrl}</a></p>}
        </div>
      )}
    </div>
  );
}
