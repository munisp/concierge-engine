import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Integrations (Phase 3): public API keys + webhook subscriptions.
// Secrets are shown once at creation — copy them immediately.

type ApiKey = {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  revoked: boolean;
  created_at: string;
};

type WebhookSub = {
  id: string;
  url: string;
  events: string[];
  active: boolean;
  created_at: string;
};

type Delivery = {
  id: string;
  event_kind: string;
  attempts: number;
  last_status?: number;
  last_error?: string;
  delivered_at?: string;
};

export default function Integrations() {
  const [keys, setKeys] = useState<ApiKey[]>([]);
  const [subs, setSubs] = useState<WebhookSub[]>([]);
  const [deliveries, setDeliveries] = useState<Delivery[]>([]);
  const [keyName, setKeyName] = useState("");
  const [keyScopes, setKeyScopes] = useState("read");
  const [hookURL, setHookURL] = useState("");
  const [hookEvents, setHookEvents] = useState("booking.*");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setKeys(await api.get<ApiKey[]>("/v1/apikeys"));
      setSubs(await api.get<WebhookSub[]>("/v1/webhooks"));
      setDeliveries(await api.get<Delivery[]>("/v1/webhooks/deliveries?limit=20"));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  async function createKey() {
    try {
      const res = await api.post<{ key: ApiKey; secret: string }>("/v1/apikeys", {
        name: keyName,
        scopes: keyScopes.split(",").map((s) => s.trim()).filter(Boolean),
      });
      setSecret(res.secret);
      setKeyName("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "create key failed");
    }
  }

  async function revokeKey(id: string) {
    try {
      await api.del(`/v1/apikeys/${id}`);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "revoke failed");
    }
  }

  async function createHook() {
    try {
      const res = await api.post<{ subscription: WebhookSub; secret: string }>("/v1/webhooks", {
        url: hookURL,
        events: hookEvents.split(",").map((s) => s.trim()).filter(Boolean),
      });
      setSecret(res.secret);
      setHookURL("");
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "subscribe failed");
    }
  }

  async function toggleHook(id: string, active: boolean) {
    try {
      await api.put(`/v1/webhooks/${id}`, { active });
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "toggle failed");
    }
  }

  return (
    <div>
      <h1>Integrations</h1>
      {error && <p className="error">{error}</p>}
      {secret && (
        <p className="notice">
          Secret (shown once — copy now): <code>{secret}</code>
        </p>
      )}

      <section>
        <h2>API keys</h2>
        <div className="form-row">
          <input
            placeholder="name"
            value={keyName}
            onChange={(e) => setKeyName(e.target.value)}
          />
          <input
            placeholder="scopes (read,write,webhooks:manage)"
            value={keyScopes}
            onChange={(e) => setKeyScopes(e.target.value)}
          />
          <button onClick={() => void createKey()} disabled={!keyName}>
            Create key
          </button>
        </div>
        <table>
          <thead>
            <tr><th>Name</th><th>Prefix</th><th>Scopes</th><th>Status</th><th /></tr>
          </thead>
          <tbody>
            {keys.map((k) => (
              <tr key={k.id}>
                <td>{k.name}</td>
                <td><code>{k.prefix}…</code></td>
                <td>{k.scopes.join(", ")}</td>
                <td>{k.revoked ? "revoked" : "active"}</td>
                <td>
                  {!k.revoked && (
                    <button onClick={() => void revokeKey(k.id)}>Revoke</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2>Webhook subscriptions</h2>
        <div className="form-row">
          <input
            placeholder="https://receiver.example/hooks"
            value={hookURL}
            onChange={(e) => setHookURL(e.target.value)}
          />
          <input
            placeholder="events (booking.*, quote.created, *)"
            value={hookEvents}
            onChange={(e) => setHookEvents(e.target.value)}
          />
          <button onClick={() => void createHook()} disabled={!hookURL}>
            Subscribe
          </button>
        </div>
        <table>
          <thead>
            <tr><th>URL</th><th>Events</th><th>Status</th><th /></tr>
          </thead>
          <tbody>
            {subs.map((s) => (
              <tr key={s.id}>
                <td>{s.url}</td>
                <td>{s.events.join(", ")}</td>
                <td>{s.active ? "active" : "paused"}</td>
                <td>
                  <button onClick={() => void toggleHook(s.id, !s.active)}>
                    {s.active ? "Pause" : "Resume"}
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      <section>
        <h2>Recent deliveries</h2>
        <table>
          <thead>
            <tr><th>Event</th><th>Attempts</th><th>Last status</th><th>Delivered</th></tr>
          </thead>
          <tbody>
            {deliveries.map((d) => (
              <tr key={d.id}>
                <td>{d.event_kind}</td>
                <td>{d.attempts}</td>
                <td>{d.last_status ?? d.last_error ?? "—"}</td>
                <td>{d.delivered_at ? new Date(d.delivered_at).toLocaleString() : "pending"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>
    </div>
  );
}
