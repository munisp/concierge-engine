import { useCallback, useEffect, useState } from "react";
import { api } from "../api";

// Cases (Phase 4): next-generation case management on top of the CRM —
// owner, priority, SLA clock, tasks, timeline, and AI assist with engine
// provenance shown on every AI-produced field.

type Case = {
  id: string;
  title: string;
  kind: string;
  priority: string;
  state: string;
  owner_id?: string;
  summary: string;
  triage_engine: string;
  sla_due_at: string;
  escalated: boolean;
  created_at: string;
};

type Task = { id: string; title: string; done: boolean };
type Note = { id: string; author: string; body: string; created_at: string };
type Similar = { case_id: string; title: string; kind: string; state: string; score: number };

type Metrics = {
  open: number;
  triaged: number;
  in_progress: number;
  waiting: number;
  resolved: number;
  closed: number;
  sla_breached: number;
  avg_resolution_hours: number;
  ai_triage_share: number;
};

const NEXT_STATES: Record<string, string[]> = {
  OPEN: ["TRIAGED", "IN_PROGRESS", "CLOSED"],
  TRIAGED: ["IN_PROGRESS", "WAITING_CLIENT", "WAITING_PARTNER", "CLOSED"],
  IN_PROGRESS: ["WAITING_CLIENT", "WAITING_PARTNER", "RESOLVED"],
  WAITING_CLIENT: ["IN_PROGRESS", "RESOLVED", "CLOSED"],
  WAITING_PARTNER: ["IN_PROGRESS", "RESOLVED"],
  RESOLVED: ["CLOSED"],
  CLOSED: [],
};

export default function Cases() {
  const [cases, setCases] = useState<Case[]>([]);
  const [metrics, setMetrics] = useState<Metrics | null>(null);
  const [selected, setSelected] = useState<Case | null>(null);
  const [tasks, setTasks] = useState<Task[]>([]);
  const [notes, setNotes] = useState<Note[]>([]);
  const [similar, setSimilar] = useState<Similar[]>([]);
  const [aiSummary, setAiSummary] = useState("");
  const [nextAction, setNextAction] = useState("");
  const [filter, setFilter] = useState("");
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const [newTask, setNewTask] = useState("");
  const [newNote, setNewNote] = useState("");
  const [error, setError] = useState("");

  const load = useCallback(async () => {
    try {
      setCases(await api.get<Case[]>(`/v1/cases${filter ? `?state=${filter}` : ""}`));
      setMetrics(await api.get<Metrics>("/v1/cases/metrics"));
      setError("");
    } catch (e) {
      setError(e instanceof Error ? e.message : "load failed");
    }
  }, [filter]);

  useEffect(() => {
    void load();
  }, [load]);

  async function open(c: Case) {
    setSelected(c);
    setAiSummary("");
    setNextAction("");
    try {
      setTasks(await api.get<Task[]>(`/v1/cases/${c.id}/tasks`));
      setNotes(await api.get<Note[]>(`/v1/cases/${c.id}/notes`));
      setSimilar(await api.get<Similar[]>(`/v1/cases/${c.id}/similar?limit=5`) ?? []);
    } catch (e) {
      setError(e instanceof Error ? e.message : "detail load failed");
    }
  }

  async function createCase() {
    try {
      const c = await api.post<Case>("/v1/cases", {
        title, text, idempotency_key: `console-${Date.now()}`,
      });
      setTitle(""); setText("");
      await load();
      await open(c);
    } catch (e) {
      setError(e instanceof Error ? e.message : "create failed");
    }
  }

  async function transition(to: string) {
    if (!selected) return;
    try {
      const c = await api.post<Case>(`/v1/cases/${selected.id}/transition`, { to });
      setSelected(c);
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "transition failed");
    }
  }

  async function addTask() {
    if (!selected || !newTask) return;
    try {
      await api.post(`/v1/cases/${selected.id}/tasks`, { title: newTask });
      setNewTask("");
      setTasks(await api.get<Task[]>(`/v1/cases/${selected.id}/tasks`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "task failed");
    }
  }

  async function completeTask(taskID: string) {
    if (!selected) return;
    try {
      await api.post(`/v1/cases/${selected.id}/tasks/${taskID}/complete`, {});
      setTasks(await api.get<Task[]>(`/v1/cases/${selected.id}/tasks`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "complete failed");
    }
  }

  async function addNote() {
    if (!selected || !newNote) return;
    try {
      await api.post(`/v1/cases/${selected.id}/notes`, { author: "operator", body: newNote });
      setNewNote("");
      setNotes(await api.get<Note[]>(`/v1/cases/${selected.id}/notes`));
    } catch (e) {
      setError(e instanceof Error ? e.message : "note failed");
    }
  }

  async function summarize() {
    if (!selected) return;
    try {
      const res = await api.get<{ summary: string; engine: string }>(`/v1/cases/${selected.id}/summary`);
      setAiSummary(`${res.summary}  [engine: ${res.engine}]`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "summarize failed");
    }
  }

  async function suggestNext() {
    if (!selected) return;
    try {
      const res = await api.get<{ action: string; rationale: string; engine: string }>(
        `/v1/cases/${selected.id}/next-action`);
      setNextAction(`${res.action} — ${res.rationale}  [engine: ${res.engine}]`);
    } catch (e) {
      setError(e instanceof Error ? e.message : "next-action failed");
    }
  }

  const overdue = (c: Case) =>
    !["RESOLVED", "CLOSED"].includes(c.state) && new Date(c.sla_due_at) < new Date();

  return (
    <div>
      <h1>Cases</h1>
      {error && <p className="error">{error}</p>}
      {metrics && (
        <p className="kpis">
          Open {metrics.open + metrics.triaged + metrics.in_progress + metrics.waiting} ·
          Resolved {metrics.resolved} · Closed {metrics.closed} ·
          SLA breached {metrics.sla_breached} ·
          Avg resolution {metrics.avg_resolution_hours.toFixed(1)}h ·
          AI triage {(metrics.ai_triage_share * 100).toFixed(0)}%
        </p>
      )}

      <section>
        <h2>Open a case</h2>
        <div className="form-row">
          <input placeholder="title" value={title} onChange={(e) => setTitle(e.target.value)} />
          <input
            placeholder="describe the client request (AI triages kind/priority/SLA)"
            value={text} onChange={(e) => setText(e.target.value)}
          />
          <button onClick={() => void createCase()} disabled={!title || !text}>Open</button>
        </div>
      </section>

      <section>
        <h2>Board</h2>
        <div className="form-row">
          <select value={filter} onChange={(e) => setFilter(e.target.value)}>
            <option value="">all states</option>
            {Object.keys(NEXT_STATES).map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
        <table>
          <thead>
            <tr><th>Title</th><th>Kind</th><th>Priority</th><th>State</th><th>SLA</th><th>Triaged by</th></tr>
          </thead>
          <tbody>
            {cases.map((c) => (
              <tr key={c.id} onClick={() => void open(c)} className="clickable">
                <td>{c.escalated ? "SLA! " : ""}{c.title}</td>
                <td>{c.kind}</td>
                <td>{c.priority}</td>
                <td>{c.state}</td>
                <td className={overdue(c) ? "error" : ""}>
                  {overdue(c) ? "OVERDUE" : new Date(c.sla_due_at).toLocaleString()}
                </td>
                <td><code>{c.triage_engine}</code></td>
              </tr>
            ))}
          </tbody>
        </table>
      </section>

      {selected && (
        <section>
          <h2>{selected.title} <code>{selected.priority}</code> <code>{selected.state}</code></h2>
          <p>{selected.summary}</p>
          <div className="form-row">
            {NEXT_STATES[selected.state]?.map((s) => (
              <button key={s} onClick={() => void transition(s)}>{s}</button>
            ))}
            <button onClick={() => void summarize()}>AI summary</button>
            <button onClick={() => void suggestNext()}>Next action</button>
          </div>
          {aiSummary && <p className="notice">{aiSummary}</p>}
          {nextAction && <p className="notice">{nextAction}</p>}

          <h3>Tasks</h3>
          <div className="form-row">
            <input placeholder="new task" value={newTask} onChange={(e) => setNewTask(e.target.value)} />
            <button onClick={() => void addTask()} disabled={!newTask}>Add</button>
          </div>
          <ul>
            {tasks.map((t) => (
              <li key={t.id}>
                {t.done ? "[done]" : <button onClick={() => void completeTask(t.id)}>done</button>} {t.title}
              </li>
            ))}
          </ul>

          <h3>Timeline</h3>
          <div className="form-row">
            <input placeholder="add a note" value={newNote} onChange={(e) => setNewNote(e.target.value)} />
            <button onClick={() => void addNote()} disabled={!newNote}>Note</button>
          </div>
          <ul>
            {notes.map((n) => (
              <li key={n.id}><b>{n.author}</b>: {n.body} <i>({new Date(n.created_at).toLocaleString()})</i></li>
            ))}
          </ul>

          {similar.length > 0 && (
            <>
              <h3>Similar past cases</h3>
              <ul>
                {similar.map((s) => (
                  <li key={s.case_id}>
                    {(s.score * 100).toFixed(0)}% — {s.title} <code>{s.state}</code>
                  </li>
                ))}
              </ul>
            </>
          )}
        </section>
      )}
    </div>
  );
}
