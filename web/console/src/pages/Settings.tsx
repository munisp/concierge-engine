import { useEffect, useState } from "react";
import { api, getTenantId } from "../api";
import type { Tenant } from "../types";

// Tenant settings (Phase 24): every config object that was API-only now has
// an editor. Each card PUTs ONLY its own object — the API applies patches
// selectively, so saving the operator roster can never wipe the tax engine.

function useTenant() {
  const [tenant, setTenant] = useState<Tenant | null>(null);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!getTenantId()) return;
    api.get<Tenant>("/v1/tenant").then(setTenant).catch((e) => setError(String(e)));
  }, []);
  return { tenant, setTenant, error };
}

async function save(patch: Record<string, unknown>): Promise<string> {
  await api.put("/v1/tenant", patch);
  return "saved";
}

function SaveButton({ onSave }: { onSave: () => Promise<string> }) {
  const [msg, setMsg] = useState("");
  return (
    <>
      <button onClick={() => { onSave().then(setMsg).catch((e) => setMsg(String(e))); }}>
        Save
      </button>
      {msg && <span className="muted" style={{ marginLeft: 8 }}>{msg}</span>}
    </>
  );
}

type FollowUpResult = {
  post_trip_sent: number;
  win_back_sent: number;
  win_back_held: number;
  clients_scanned: number;
};

function RunFollowUps() {
  const [msg, setMsg] = useState("");
  async function run() {
    try {
      const r = await api.post<FollowUpResult>("/v1/followups/run");
      setMsg(`${r.post_trip_sent} post-trip sent, ${r.win_back_sent} win-back sent, ` +
        `${r.win_back_held} held (no consent), ${r.clients_scanned} clients scanned`);
    } catch (e) {
      setMsg(String(e));
    }
  }
  return (
    <>
      <button onClick={() => void run()}>Run sweep now</button>
      {msg && <p className="muted">{msg}</p>}
    </>
  );
}

export default function Settings() {
  const { tenant, error } = useTenant();
  const [alertPhones, setAlertPhones] = useState("");
  const [turnaround, setTurnaround] = useState(30);
  const [duration, setDuration] = useState(120);
  const [taxEngine, setTaxEngine] = useState("");
  const [taxCountry, setTaxCountry] = useState("");
  const [vatBP, setVatBP] = useState(0);
  const [taxInclusive, setTaxInclusive] = useState(false);
  const [disableStripe, setDisableStripe] = useState(false);
  const [disablePaystack, setDisablePaystack] = useState(false);
  const [rewardsDisabled, setRewardsDisabled] = useState(false);
  const [referrerPct, setReferrerPct] = useState(5);
  const [refereePct, setRefereePct] = useState(0);
  const [maxReward, setMaxReward] = useState(0);
  const [expiryDays, setExpiryDays] = useState(0);
  const [mktCap, setMktCap] = useState(0);
  const [quoteSLA, setQuoteSLA] = useState(3600);
  const [lcDisabled, setLcDisabled] = useState(false);
  const [lcPostTrip, setLcPostTrip] = useState(24);
  const [lcWinBack, setLcWinBack] = useState(90);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    if (!tenant || loaded) return;
    setAlertPhones((tenant.operator_config?.alert_phones ?? []).join(", "));
    setTurnaround(tenant.dispatch_config?.turnaround_minutes ?? 30);
    setDuration(tenant.dispatch_config?.default_duration_minutes ?? 120);
    setTaxEngine(tenant.tax_config?.engine ?? "");
    setTaxCountry(tenant.tax_config?.country ?? "");
    setVatBP(tenant.tax_config?.vat_rate_bp ?? 0);
    setTaxInclusive(tenant.tax_config?.prices_include_tax ?? false);
    setDisableStripe(tenant.payment_config?.disable_stripe ?? false);
    setDisablePaystack(tenant.payment_config?.disable_paystack ?? false);
    setRewardsDisabled(tenant.reward_program?.disabled ?? false);
    setReferrerPct((tenant.reward_program?.referrer_pct ?? 0.05) * 100);
    setRefereePct((tenant.reward_program?.referee_pct ?? 0) * 100);
    setMaxReward(tenant.reward_program?.max_reward_minor ?? 0);
    setExpiryDays(tenant.reward_program?.expiry_days ?? 0);
    setMktCap(tenant.outreach_config?.marketing_cap_7d ?? 0);
    setQuoteSLA(tenant.sla_config?.quote_sla_seconds ?? 3600);
    setLcDisabled(tenant.lifecycle_config?.disabled ?? false);
    setLcPostTrip(tenant.lifecycle_config?.post_trip_hours ?? 24);
    setLcWinBack(tenant.lifecycle_config?.win_back_days ?? 90);
    setLoaded(true);
  }, [tenant, loaded]);

  if (!getTenantId()) return <p className="muted">Set a tenant on the Admin page first.</p>;
  if (error) return <p className="muted">{error}</p>;
  if (!tenant) return <p className="muted">loading…</p>;

  return (
    <div>
      <h1>Settings — {tenant.name}</h1>

      <div className="card">
        <h2>Operator roster</h2>
        <p className="muted">
          WhatsApp numbers that receive SLA-failure alerts. Empty = platform-global
          operator number. Comma-separated.
        </p>
        <input style={{ width: "100%" }} value={alertPhones}
          onChange={(e) => setAlertPhones(e.target.value)}
          placeholder="+9715…, +234…" />
        <p />
        <SaveButton onSave={() => save({
          operator_config: {
            alert_phones: alertPhones.split(",").map((p) => p.trim()).filter(Boolean),
          },
        })} />
      </div>

      <div className="card">
        <h2>Dispatch scheduling</h2>
        <p className="muted">
          Turnaround buffer pads each trip's end (cleaning/repositioning); default
          duration sizes windows when the booking carries no hours.
        </p>
        <label>Turnaround buffer (minutes)</label>
        <input type="number" value={turnaround}
          onChange={(e) => setTurnaround(Number(e.target.value))} />
        <label>Default service duration (minutes)</label>
        <input type="number" value={duration}
          onChange={(e) => setDuration(Number(e.target.value))} />
        <p />
        <SaveButton onSave={() => save({
          dispatch_config: { turnaround_minutes: turnaround, default_duration_minutes: duration },
        })} />
      </div>

      <div className="card">
        <h2>Client lifecycle (always-on engagement)</h2>
        <p className="muted">
          The lifecycle sweeper runs hourly. Post-trip follow-ups go out as
          service messages (no marketing consent needed); win-backs are
          marketing — clients who never opted in are held, never contacted.
        </p>
        <label>
          <input type="checkbox" checked={lcDisabled}
            onChange={(e) => setLcDisabled(e.target.checked)} /> Disable lifecycle follow-ups
        </label>
        <label>Post-trip follow-up delay (hours)</label>
        <input type="number" value={lcPostTrip}
          onChange={(e) => setLcPostTrip(Number(e.target.value))} />
        <label>Win-back after days without a booking</label>
        <input type="number" value={lcWinBack}
          onChange={(e) => setLcWinBack(Number(e.target.value))} />
        <p />
        <SaveButton onSave={() => save({
          lifecycle_config: { disabled: lcDisabled, post_trip_hours: lcPostTrip, win_back_days: lcWinBack },
        })} />{" "}
        <RunFollowUps />
      </div>

      <div className="card">
        <h2>Tax</h2>
        <label>Engine</label>
        <select value={taxEngine} onChange={(e) => setTaxEngine(e.target.value)}>
          <option value="">none (B2B/exempt)</option>
          <option value="flat">flat rate (VAT/GST)</option>
          <option value="stripe-tax">Stripe Tax (live)</option>
        </select>
        <label>Registration country (ISO alpha-2)</label>
        <input value={taxCountry} maxLength={2} placeholder="NL"
          onChange={(e) => setTaxCountry(e.target.value.toUpperCase())} />
        <label>Rate (basis points — 2100 = 21%)</label>
        <input type="number" value={vatBP} onChange={(e) => setVatBP(Number(e.target.value))} />
        <label>
          <input type="checkbox" checked={taxInclusive}
            onChange={(e) => setTaxInclusive(e.target.checked)} /> Prices include tax (EU B2C)
        </label>
        <p />
        <SaveButton onSave={() => save({
          tax_config: { engine: taxEngine, country: taxCountry, vat_rate_bp: vatBP,
            prices_include_tax: taxInclusive },
        })} />
      </div>

      <div className="card">
        <h2>Payment rails</h2>
        <label>
          <input type="checkbox" checked={disableStripe}
            onChange={(e) => setDisableStripe(e.target.checked)} /> Disable Stripe
        </label>
        <label>
          <input type="checkbox" checked={disablePaystack}
            onChange={(e) => setDisablePaystack(e.target.checked)} /> Disable Paystack
        </label>
        <p />
        <SaveButton onSave={() => save({
          payment_config: { disable_stripe: disableStripe, disable_paystack: disablePaystack },
        })} />
      </div>

      <div className="card">
        <h2>Reward program</h2>
        <label>
          <input type="checkbox" checked={rewardsDisabled}
            onChange={(e) => setRewardsDisabled(e.target.checked)} /> Program disabled (kill-switch)
        </label>
        <label>Referrer reward (% of referee's first booking)</label>
        <input type="number" step="0.5" value={referrerPct}
          onChange={(e) => setReferrerPct(Number(e.target.value))} />
        <label>Referee welcome credit (%)</label>
        <input type="number" step="0.5" value={refereePct}
          onChange={(e) => setRefereePct(Number(e.target.value))} />
        <label>Max reward per accrual (minor units, 0 = uncapped)</label>
        <input type="number" value={maxReward}
          onChange={(e) => setMaxReward(Number(e.target.value))} />
        <label>Credit expiry (days, 0 = never)</label>
        <input type="number" value={expiryDays}
          onChange={(e) => setExpiryDays(Number(e.target.value))} />
        <p />
        <SaveButton onSave={() => save({
          reward_program: { disabled: rewardsDisabled, referrer_pct: referrerPct / 100,
            referee_pct: refereePct / 100, max_reward_minor: maxReward, expiry_days: expiryDays },
        })} />
      </div>

      <div className="card">
        <h2>Outreach &amp; SLA</h2>
        <label>Marketing sends per client per 7 days (0 = platform default of 3)</label>
        <input type="number" value={mktCap} onChange={(e) => setMktCap(Number(e.target.value))} />
        <label>Quote SLA (seconds)</label>
        <input type="number" value={quoteSLA} onChange={(e) => setQuoteSLA(Number(e.target.value))} />
        <p />
        <SaveButton onSave={() => save({ outreach_config: { marketing_cap_7d: mktCap } })} />
        <p />
        <SaveButton onSave={() => save({ sla_config: { quote_sla_seconds: quoteSLA } })} />
      </div>
    </div>
  );
}
