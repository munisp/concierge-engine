import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { api, getTenantId } from "../api";

// Tenant onboarding wizard (Phase 27): the setup journey as a live checklist.
// Step statuses are DERIVED server-side from real resources (catalog, areas,
// brand, roster, promos, bookings) — this page never stores progress, it
// reads it. Two playbooks: "new" (build from scratch) and "existing"
// (brownfield: import what you already run, integrate the rest).
// Phase 32: steps arrive GROUPED (essentials → operations → presence →
// growth) and each pending step links straight to the console surface that
// completes it.

type Step = {
  id: string; group: string; title: string;
  status: string; detail: string; action: string;
};

type Report = {
  track: string;
  steps: Step[];
  done: number;
  total: number;
  progress_pct: number;
  next_step?: string;
  complete: boolean;
};

type ImportResult = { created: number; skipped: number; errors?: string[] };

type WaLinks = { configured: boolean; setup?: string; links?: { service_name: string; url: string }[] };

const STATUS_LABEL: Record<string, string> = { done: "DONE", pending: "TO DO", skipped: "SKIPPED" };

// Group presentation order + blurbs. Ids come from the server; unknown
// groups fall through to the end untouched.
const GROUPS: [string, string, string][] = [
  ["essentials", "Essentials", "What you sell, where, and how you get paid."],
  ["operations", "Operations", "The channels, people and vehicles that deliver the service."],
  ["presence", "Presence", "Your public website, design and social footprint."],
  ["growth", "Growth", "Campaigns, follow-ups and the first bookings."],
];

const CATALOG_EXAMPLE = JSON.stringify(
  [{ vertical: "ground", name: "Airport Transfer", base_minor: 9500, per_km_minor: 210 }],
  null, 2);

const CLIENTS_EXAMPLE = JSON.stringify(
  [{ full_name: "Ada Lovelace", phone: "+447700900000", email: "ada@example.com" }],
  null, 2);

export default function Onboarding() {
  const [rep, setRep] = useState<Report | null>(null);
  const [err, setErr] = useState("");
  const [catalogJSON, setCatalogJSON] = useState("");
  const [clientsJSON, setClientsJSON] = useState("");
  const [importMsg, setImportMsg] = useState("");
  const [wa, setWa] = useState<WaLinks | null>(null);
  const tenantId = getTenantId();

  const load = useCallback(() => {
    if (!tenantId) return;
    api.get<Report>("/v1/onboarding").then(setRep).catch((e) => setErr(String(e)));
    api.get<WaLinks>("/v1/marketing/wa-links").then(setWa).catch(() => setWa(null));
  }, [tenantId]);

  useEffect(load, [load]);

  const setTrack = async (track: string) => {
    await api.put("/v1/onboarding", { track });
    load();
  };
  const skip = async (id: string, skipIt: boolean) => {
    await api.put("/v1/onboarding", skipIt ? { skip: [id] } : { unskip: [id] });
    load();
  };
  const runImport = async (kind: "catalog" | "clients") => {
    setImportMsg("");
    try {
      const raw = kind === "catalog" ? catalogJSON : clientsJSON;
      const items = JSON.parse(raw) as unknown[];
      const r = await api.post<ImportResult>(`/v1/import/${kind}`, { items });
      setImportMsg(`${r.created} imported, ${r.skipped} duplicates skipped` +
        (r.errors?.length ? `, errors: ${r.errors.join("; ")}` : ""));
      load();
    } catch (e) {
      setImportMsg(String(e));
    }
  };

  if (!tenantId) return <p className="muted">Set a tenant id on the capture page first.</p>;
  if (err) return <p className="error">{err}</p>;
  if (!rep) return <p className="muted">Loading…</p>;

  return (
    <section>
      <h1>Setup journey</h1>

      <div className="card">
        <h2>Playbook</h2>
        <p className="muted">
          {rep.track === "existing"
            ? "Existing business — import what you already run, integrate the rest."
            : "New business — build your stack from scratch (starter services included)."}
        </p>
        <button disabled={rep.track === "new"} onClick={() => void setTrack("new")}>New business</button>{" "}
        <button disabled={rep.track === "existing"} onClick={() => void setTrack("existing")}>
          Existing business (import &amp; integrate)
        </button>
        <p style={{ marginTop: 12 }}>
          <strong>{rep.progress_pct}% complete</strong> ({rep.done}/{rep.total} steps)
          {rep.complete && " — fully live."}
          {!rep.complete && rep.next_step && ` — next: ${rep.next_step}`}
        </p>
        <div style={{ background: "#e5e5e5", borderRadius: 4, height: 8, maxWidth: 420 }}>
          <div style={{ background: "#0b5fff", borderRadius: 4, height: 8, width: `${rep.progress_pct}%` }} />
        </div>
      </div>

      <div className="card">
        <h2>Steps</h2>
        {GROUPS.map(([gid, label, blurb]) => {
          const steps = rep.steps.filter((s) => (s.group || "essentials") === gid);
          if (steps.length === 0) return null;
          const done = steps.filter((s) => s.status !== "pending").length;
          return (
            <div key={gid} style={{ marginBottom: 18 }}>
              <h3 style={{ margin: "10px 0 2px" }}>
                {label}{" "}
                <span className="muted" style={{ fontWeight: "normal" }}>
                  — {done}/{steps.length}
                </span>
              </h3>
              <p className="muted" style={{ margin: "0 0 8px" }}>{blurb}</p>
              <table>
                <tbody>
                  {steps.map((s) => (
                    <tr key={s.id}>
                      <td style={{ width: 90 }}>
                        <strong className={s.status === "done" ? "ok" : "muted"}>{STATUS_LABEL[s.status] ?? s.status}</strong>
                      </td>
                      <td style={{ width: 200 }}>{s.title}</td>
                      <td className="muted">{s.detail}</td>
                      <td style={{ width: 230 }}>
                        {s.status === "pending" && s.action && s.action !== "/onboarding" && (
                          <Link to={s.action}>Open →</Link>
                        )}{" "}
                        {s.status === "pending" && (
                          <button onClick={() => void skip(s.id, true)}>Skip</button>
                        )}
                        {s.status === "skipped" && (
                          <button onClick={() => void skip(s.id, false)}>Reinstate</button>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          );
        })}
      </div>

      <div className="card">
        <h2>Import existing business data</h2>
        <p className="muted">
          Paste JSON. Re-running is safe — catalog dedupes by name, clients by phone.
          Imported clients arrive with marketing consent OFF (consent is collected on first
          contact) and never count as platform acquisitions.
        </p>
        <h3>Service catalog</h3>
        <textarea rows={5} style={{ width: "100%" }} placeholder={CATALOG_EXAMPLE}
          value={catalogJSON} onChange={(e) => setCatalogJSON(e.target.value)} />
        <button onClick={() => void runImport("catalog")}>Import catalog</button>
        <h3 style={{ marginTop: 16 }}>Client book</h3>
        <textarea rows={5} style={{ width: "100%" }} placeholder={CLIENTS_EXAMPLE}
          value={clientsJSON} onChange={(e) => setClientsJSON(e.target.value)} />
        <button onClick={() => void runImport("clients")}>Import clients</button>
        {importMsg && <p className="muted">{importMsg}</p>}
      </div>

      <div className="card">
        <h2>Marketing assets</h2>
        <p>
          Printable QR booking codes:{" "}
          <a href="/v1/marketing/qr?target=landing" target="_blank" rel="noreferrer">landing</a>
          {" "}(per-service and per-area codes: <code>/v1/marketing/qr?target=svc:{"{id}"}</code>).
        </p>
        <h3>WhatsApp click-to-chat</h3>
        {wa?.configured ? (
          <ul>
            {wa.links?.map((l) => (
              <li key={l.url}><a href={l.url} target="_blank" rel="noreferrer">{l.service_name}</a></li>
            ))}
          </ul>
        ) : (
          <p className="muted">{wa?.setup ?? "…"}</p>
        )}
        <h3>Embed on your existing website</h3>
        <p className="muted">Paste this where your booking button lives:</p>
        <code style={{ display: "block", padding: 8, background: "#f4f4f4" }}>
          {`<iframe src="${window.location.origin.replace(/\/$/, "")}/embed/YOUR-SLUG" ` +
            `style="width:100%;max-width:480px;height:280px;border:0" title="Book"></iframe>`}
        </code>
        <h3>Link-in-bio</h3>
        <p className="muted">
          Your social bio page: <code>/site/YOUR-SLUG/links</code> — every booking CTA on one
          mobile page, attributed to the social channel.
        </p>
      </div>
    </section>
  );
}
