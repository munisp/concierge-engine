import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Phase 53: visual campaign builder (Mautic feature-borrow A1).
// The canvas edits the drip engine directly — every counter on a node is
// derived live from the sends ledger, the same records the hourly sweep
// acts on. No parallel state, no invented numbers.

type Step = {
  after_hours: number;
  body: string;
  label?: string;
  template_id?: string;
  channel?: string; // whatsapp (default) | email
  subject?: string;
  require_engagement?: boolean;
  engagement_timeout_hours?: number;
};

type Sequence = {
  name: string;
  purpose?: string;
  steps: Step[];
  segment?: Record<string, unknown>;
};

type StepStats = {
  index: number;
  label?: string;
  sent: number;
  scheduled: number;
  held: number;
  failed: number;
  halted: number;
  waiting: number;
};

type Progress = {
  sequence: Sequence;
  enrolled: number;
  steps: StepStats[];
};

type Template = { id: string; name: string; purpose: string; body: string };

const emptyStep = (afterHours: number): Step => ({ after_hours: afterHours, body: "" });

export default function Sequences() {
  const [rows, setRows] = useState<Progress[]>([]);
  const [templates, setTemplates] = useState<Template[]>([]);
  const [selected, setSelected] = useState<string>("");
  const [draft, setDraft] = useState<Sequence | null>(null);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [creating, setCreating] = useState(false);
  const [newName, setNewName] = useState("");

  const load = useCallback(async () => {
    const [p, t] = await Promise.all([
      api.get<{ sequences: Progress[] }>("/v1/outreach/sequences"),
      api.get<Template[]>("/v1/outreach/templates"),
    ]);
    setRows(p.sequences ?? []);
    setTemplates(t ?? []);
  }, []);

  useEffect(() => {
    load().catch((e) => setError(String(e)));
  }, [load]);

  const current = rows.find((r) => r.sequence.name === selected);

  const select = (name: string) => {
    setSelected(name);
    setDirty(false);
    const row = rows.find((r) => r.sequence.name === name);
    setDraft(row ? structuredClone(row.sequence) : null);
  };

  const save = async () => {
    if (!draft) return;
    setError("");
    try {
      await api.put(`/v1/outreach/sequences/${encodeURIComponent(draft.name)}`, draft);
      setNotice(`Saved "${draft.name}" — the next hourly sweep picks it up.`);
      setDirty(false);
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  const remove = async (name: string) => {
    if (!window.confirm(`Delete sequence "${name}"? Ledger history is kept; re-creating the same name resumes it.`)) return;
    setError("");
    try {
      await api.del(`/v1/outreach/sequences/${encodeURIComponent(name)}`);
      if (selected === name) {
        setSelected("");
        setDraft(null);
      }
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  const runNow = async () => {
    setError("");
    try {
      const res = await api.post<{ sent: number; held: number; failed: number; clients_scanned: number }>(
        "/v1/outreach/sequences/run",
      );
      setNotice(`Sweep run: ${res.sent} sent, ${res.held} held, ${res.failed} failed across ${res.clients_scanned} clients.`);
      await load();
    } catch (e) {
      setError(String(e));
    }
  };

  const create = async () => {
    const name = newName.trim();
    if (!name) return;
    setError("");
    try {
      await api.put(`/v1/outreach/sequences/${encodeURIComponent(name)}`, {
        name,
        purpose: "marketing",
        steps: [emptyStep(1)],
      });
      setCreating(false);
      setNewName("");
      await load();
      select(name);
    } catch (e) {
      setError(String(e));
    }
  };

  const mutate = (fn: (d: Sequence) => void) => {
    if (!draft) return;
    const d = structuredClone(draft);
    fn(d);
    setDraft(d);
    setDirty(true);
  };

  const moveStep = (i: number, dir: -1 | 1) =>
    mutate((d) => {
      const j = i + dir;
      if (j < 0 || j >= d.steps.length) return;
      [d.steps[i], d.steps[j]] = [d.steps[j], d.steps[i]];
    });

  const statsFor = (i: number): StepStats | undefined => current?.steps[i];

  return (
    <div>
      <h2>
        Campaign Builder <span className="muted">— visual editor for drip sequences</span>
      </h2>
      <p className="muted">
        Node counters are read live from the sends ledger — the same records the hourly sweep acts on.
        A gated step waits for a real open, click or reply; on timeout it halts visibly, never silently.
      </p>
      {error && <p className="error">{error}</p>}
      {notice && <p className="notice">{notice}</p>}

      <div className="row" style={{ gap: 16, alignItems: "flex-start", flexWrap: "wrap" }}>
        {/* sequence list */}
        <div className="card" style={{ minWidth: 240 }}>
          <h3>Sequences</h3>
          {rows.length === 0 && <p className="muted">None yet — build the first one.</p>}
          {rows.map((r) => (
            <div key={r.sequence.name} className="row" style={{ justifyContent: "space-between", gap: 8 }}>
              <button className="secondary" style={{ flex: 1, textAlign: "left" }} onClick={() => select(r.sequence.name)}>
                {r.sequence.name} <span className="muted">{r.sequence.steps.length} steps · {r.enrolled} enrolled</span>
              </button>
              <button className="secondary" onClick={() => void remove(r.sequence.name)}>✕</button>
            </div>
          ))}
          {creating ? (
            <div className="row" style={{ gap: 8, marginTop: 8 }}>
              <input
                placeholder="sequence-name"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                onKeyDown={(e) => e.key === "Enter" && void create()}
              />
              <button onClick={() => void create()}>Create</button>
            </div>
          ) : (
            <button style={{ marginTop: 8 }} onClick={() => setCreating(true)}>New sequence</button>
          )}
        </div>

        {/* canvas */}
        <div style={{ flex: 1, minWidth: 420 }}>
          {!draft && <div className="card"><p className="muted">Select a sequence to open its canvas.</p></div>}
          {draft && (
            <>
              <div className="row" style={{ gap: 8, marginBottom: 8 }}>
                <button onClick={() => void save()} disabled={!dirty}>Save</button>
                <button className="secondary" onClick={() => select(draft.name)} disabled={!dirty}>Discard</button>
                <button className="secondary" onClick={() => void runNow()}>Run sweep now</button>
                <span className="muted">
                  {current?.enrolled ?? 0} enrolled · purpose {draft.purpose || "marketing"}
                </span>
              </div>

              {/* start node */}
              <div className="card" style={{ textAlign: "center", borderStyle: "dashed" }}>
                <strong>Enrolled clients</strong>{" "}
                <span className="muted">
                  {draft.segment ? "segment-filtered audience" : "every client"} — enrolled at signup
                </span>
              </div>

              {draft.steps.map((step, i) => {
                const stats = statsFor(i);
                return (
                  <div key={i}>
                    <div style={{ textAlign: "center", color: "#999", lineHeight: 1.2 }}>
                      │<br />
                      <span className="muted">+{step.after_hours}h</span>
                      <br />▼
                    </div>
                    <div className="card">
                      <div className="row" style={{ justifyContent: "space-between", gap: 8 }}>
                        <strong>
                          {i + 1}. {step.label || "Step " + (i + 1)}
                        </strong>
                        <span>
                          <span className="badge">{step.channel === "email" ? "email" : "whatsapp"}</span>{" "}
                          {step.template_id && <span className="badge">template</span>}{" "}
                          {step.require_engagement && (
                            <span className="badge">
                              gate: engagement{step.engagement_timeout_hours ? ` ≤${step.engagement_timeout_hours}h` : ""}
                            </span>
                          )}
                        </span>
                      </div>
                      {step.channel === "email" && <p className="muted" style={{ margin: "4px 0" }}>Subject: {step.subject}</p>}
                      <p style={{ margin: "6px 0" }}>{step.body || <span className="muted">(template renders the body)</span>}</p>
                      {stats && (
                        <p className="muted" style={{ margin: "4px 0" }}>
                          {stats.sent} sent · {stats.scheduled} scheduled · {stats.held} held · {stats.failed} failed
                          {stats.halted > 0 && <> · <strong>{stats.halted} halted</strong></>}
                          {stats.waiting > 0 && <> · {stats.waiting} waiting</>}
                        </p>
                      )}
                      {/* editor */}
                      <div className="row" style={{ gap: 8, flexWrap: "wrap", marginTop: 6 }}>
                        <input
                          style={{ width: 140 }}
                          placeholder="label"
                          value={step.label ?? ""}
                          onChange={(e) => mutate((d) => { d.steps[i].label = e.target.value; })}
                        />
                        <label className="muted">
                          after
                          <input
                            type="number" min={0} max={8760} style={{ width: 70, margin: "0 4px" }}
                            value={step.after_hours}
                            onChange={(e) => mutate((d) => { d.steps[i].after_hours = Number(e.target.value); })}
                          />
                          h
                        </label>
                        <select
                          value={step.channel || "whatsapp"}
                          onChange={(e) => mutate((d) => { d.steps[i].channel = e.target.value === "whatsapp" ? "" : "email"; })}
                        >
                          <option value="whatsapp">whatsapp</option>
                          <option value="email">email</option>
                        </select>
                        {step.channel === "email" && (
                          <input
                            style={{ flex: 1, minWidth: 160 }}
                            placeholder="email subject (required)"
                            value={step.subject ?? ""}
                            onChange={(e) => mutate((d) => { d.steps[i].subject = e.target.value; })}
                          />
                        )}
                        <select
                          value={step.template_id ?? ""}
                          onChange={(e) => mutate((d) => { d.steps[i].template_id = e.target.value || undefined; })}
                        >
                          <option value="">freeform body</option>
                          {templates.map((t) => (
                            <option key={t.id} value={t.id}>template: {t.name}</option>
                          ))}
                        </select>
                      </div>
                      {!step.template_id && (
                        <textarea
                          rows={2}
                          style={{ width: "100%", marginTop: 6 }}
                          placeholder="Body — {{client_name}} / {{tenant_name}} render server-side"
                          value={step.body}
                          onChange={(e) => mutate((d) => { d.steps[i].body = e.target.value; })}
                        />
                      )}
                      <div className="row" style={{ gap: 12, flexWrap: "wrap", marginTop: 6 }}>
                        {i > 0 && (
                          <label className="muted">
                            <input
                              type="checkbox"
                              checked={step.require_engagement ?? false}
                              onChange={(e) => mutate((d) => { d.steps[i].require_engagement = e.target.checked; })}
                            />{" "}
                            wait for engagement
                          </label>
                        )}
                        {i > 0 && step.require_engagement && (
                          <label className="muted">
                            timeout
                            <input
                              type="number" min={0} max={8760} style={{ width: 70, margin: "0 4px" }}
                              value={step.engagement_timeout_hours ?? 0}
                              onChange={(e) => mutate((d) => { d.steps[i].engagement_timeout_hours = Number(e.target.value); })}
                            />
                            h (0 = wait forever)
                          </label>
                        )}
                        <span style={{ flex: 1 }} />
                        <button className="secondary" onClick={() => moveStep(i, -1)} disabled={i === 0}>↑</button>
                        <button className="secondary" onClick={() => moveStep(i, 1)} disabled={i === draft.steps.length - 1}>↓</button>
                        <button
                          className="secondary"
                          disabled={draft.steps.length <= 1}
                          onClick={() => mutate((d) => { d.steps.splice(i, 1); })}
                        >
                          delete
                        </button>
                      </div>
                    </div>
                  </div>
                );
              })}

              <div style={{ textAlign: "center", marginTop: 8 }}>
                <button
                  className="secondary"
                  disabled={draft.steps.length >= 24}
                  onClick={() =>
                    mutate((d) => {
                      const last = d.steps[d.steps.length - 1];
                      d.steps.push(emptyStep(last ? Math.max(last.after_hours, 24) : 24));
                    })
                  }
                >
                  + add step
                </button>
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
