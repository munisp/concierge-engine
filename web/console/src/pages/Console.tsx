import { useEffect, useState } from "react";
import { api, getTenantId } from "../api";
import type { HandoffItem, Message } from "../types";

// Concierge console v0 (AI spec 5): thread view + handoff brief +
// approve / edit / reject. The human replies as the tenant from the same
// thread, then can hand back to the AI ("return to AI").
export default function Console() {
  const [queue, setQueue] = useState<HandoffItem[]>([]);
  const [selected, setSelected] = useState<HandoffItem | null>(null);
  const [timeline, setTimeline] = useState<Message[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");

  async function refresh() {
    try {
      setQueue(await api.get<HandoffItem[]>("/v1/handoffs"));
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    refresh();
    const t = setInterval(refresh, 5000);
    return () => clearInterval(t);
  }, []);

  async function open(item: HandoffItem) {
    setSelected(item);
    try {
      setTimeline(await api.get<Message[]>(`/v1/conversations/${item.conversation_id}/timeline`));
    } catch {
      setTimeline([]);
    }
  }

  async function resolve(id: string, resolution: string) {
    await api.post(`/v1/handoffs/${id}/resolve`, { resolution, owner: "staff:console" });
    setSelected(null);
    await refresh();
  }

  async function sendStaffReply() {
    if (!selected || !draft.trim()) return;
    await api.post(`/v1/conversations/${selected.conversation_id}/messages`, {
      author: "HUMAN_STAFF", body: draft,
    });
    setDraft("");
    await open(selected);
  }

  return (
    <div>
      <h1>Concierge console</h1>
      {!getTenantId() && <p className="error">Set a tenant in Admin first.</p>}
      {error && <p className="error">{error}</p>}
      <div className="row">
        <div>
          <h2>Handoff queue</h2>
          {queue.length === 0 && <p className="muted">Queue clear.</p>}
          {queue.map((h) => (
            <div key={h.id} className="card" onClick={() => open(h)} style={{ cursor: "pointer" }}>
              <span className={`badge ${h.status}`}>{h.trigger}</span>{" "}
              <strong>{h.reason}</strong>
              <p className="muted">{new Date(h.created_at).toLocaleString()}</p>
            </div>
          ))}
        </div>
        <div style={{ flex: 2 }}>
          {selected ? (
            <div className="card">
              <h2>Handoff brief</h2>
              <p><strong>Trigger:</strong> {selected.trigger} · {selected.reason}</p>
              <p className="muted">{selected.brief.message_count} messages in thread</p>
              <ul>
                {selected.brief.recent_messages.map((m, i) => <li key={i}>{m}</li>)}
              </ul>
              <h3>Thread</h3>
              <div className="thread">
                {timeline.map((m) => (
                  <div key={m.id} className={`msg ${m.author}`}>
                    <div className="who">{m.author} · {new Date(m.sent_at).toLocaleTimeString()}</div>
                    <div>{m.body}</div>
                  </div>
                ))}
              </div>
              <p />
              <textarea rows={2} value={draft} onChange={(e) => setDraft(e.target.value)}
                placeholder="Reply as the tenant…" />
              <p />
              <div className="row">
                <button onClick={sendStaffReply}>Send reply</button>
                <button className="secondary" onClick={() => resolve(selected.id, "RESOLVED")}>
                  Return to AI
                </button>
                <button className="danger" onClick={() => resolve(selected.id, "REJECTED")}>
                  Reject action
                </button>
              </div>
            </div>
          ) : (
            <p className="muted">Select a handoff to view the structured brief.</p>
          )}
        </div>
      </div>
    </div>
  );
}
