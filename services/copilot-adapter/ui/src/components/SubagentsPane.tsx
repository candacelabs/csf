import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { api, describeError } from "../api/client";
import type { Subagent, SubagentActivity } from "../api/client";
import { absoluteTime, compareInstants } from "../format";
import { mergeSubagentActivity } from "../sessionEvents";
import { Markdown } from "../markdown";
import { PaneLoading } from "./PaneLoading";

type SubagentsPaneProps = {
  sessionId: string;
  subagents: Subagent[];
  liveActivity: Record<string, SubagentActivity[]>;
  onLoaded: (subagents: Subagent[]) => void;
};

function duration(startedAt: string, finishedAt: string | undefined, now: Date): string {
  const start = new Date(startedAt).getTime();
  const end = finishedAt === undefined ? now.getTime() : new Date(finishedAt).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end)) return "";
  const seconds = Math.max(0, Math.floor((end - start) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.floor(seconds / 60);
  return minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

function durationLabel(subagent: Subagent, now: Date): string {
  const elapsed = duration(subagent.startedAt, subagent.completedAt, now);
  if (subagent.status === "active") return `Working for ${elapsed}`;
  if (subagent.status === "completed") return `Worked for ${elapsed}`;
  return `Failed after ${elapsed}`;
}

function activityIcon(kind: SubagentActivity["kind"]): string {
  if (kind === "toolCall" || kind === "toolResult") return ">_";
  if (kind === "completed") return "✓";
  if (kind === "failed") return "!";
  if (kind === "started") return "✦";
  return "·";
}

function mergeSubagents(snapshot: Subagent[], live: Subagent[]): Subagent[] {
  const liveById = new Map(live.map((subagent) => [subagent.id, subagent]));
  const snapshotIds = new Set(snapshot.map((subagent) => subagent.id));
  return [
    ...snapshot.map((subagent) => {
      const current = liveById.get(subagent.id);
      return current !== undefined && compareInstants(current.updatedAt, subagent.updatedAt) >= 0 ? current : subagent;
    }),
    ...live.filter((subagent) => !snapshotIds.has(subagent.id)),
  ];
}

export function SubagentsPane({ sessionId, subagents, liveActivity, onLoaded }: SubagentsPaneProps) {
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedSnapshot, setSelectedSnapshot] = useState<Subagent | null>(null);
  const [snapshot, setSnapshot] = useState<SubagentActivity[]>([]);
  const [loading, setLoading] = useState(subagents.length === 0);
  const [failure, setFailure] = useState<string | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailFailure, setDetailFailure] = useState<string | null>(null);
  const [detailAttempt, setDetailAttempt] = useState(0);
  const [now, setNow] = useState(() => new Date());
  const loadVersion = useRef(0);
  const latestSubagents = useRef(subagents);
  latestSubagents.current = subagents;

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    try {
      const { data, error } = await api.GET("/v1/sessions/{sessionId}/subagents", {
        params: { path: { sessionId } },
      });
      if (version !== loadVersion.current) return;
      if (error !== undefined || data === undefined) {
        setFailure(error === undefined ? "subagents could not be read" : describeError(error));
        return;
      }
      onLoaded(mergeSubagents(data.data, latestSubagents.current));
      setFailure(null);
    } catch (cause) {
      if (version === loadVersion.current) {
        setFailure(`Subagents could not be refreshed: ${describeError(cause)}. Retry when the connection is available.`);
      }
    } finally {
      if (version === loadVersion.current) setLoading(false);
    }
  }, [onLoaded, sessionId]);

  useEffect(() => {
    void load();
    return () => { loadVersion.current += 1; };
  }, [load]);
  useEffect(() => {
    const timer = window.setInterval(() => setNow(new Date()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  const liveSelected = subagents.find((subagent) => subagent.id === selectedId) ?? null;
  const selected = selectedSnapshot !== null && selectedSnapshot.id === selectedId && (liveSelected === null || compareInstants(selectedSnapshot.updatedAt, liveSelected.updatedAt) > 0) ? selectedSnapshot : liveSelected;
  useEffect(() => {
    if (selectedId === null) {
      setSnapshot([]);
      setSelectedSnapshot(null);
      setDetailLoading(false);
      setDetailFailure(null);
      return;
    }
    let cancelled = false;
    setSnapshot([]);
    setDetailLoading(true);
    setDetailFailure(null);
    void (async () => {
      try {
        const collected: SubagentActivity[] = [];
        const seen = new Set<number>();
        let afterSeq: number | undefined;
        const detail = await api.GET("/v1/sessions/{sessionId}/subagents/{subagentId}", {
          params: { path: { sessionId, subagentId: selectedId } },
        });
        if (cancelled) return;
        if (detail.error !== undefined || detail.data === undefined) {
          throw new Error(detail.error === undefined ? "subagent detail could not be read" : describeError(detail.error));
        }
        setSelectedSnapshot(detail.data);
        do {
          const { data, error } = await api.GET("/v1/sessions/{sessionId}/subagents/{subagentId}/activity", {
            params: {
              path: { sessionId, subagentId: selectedId },
              query: { limit: 200, ...(afterSeq === undefined ? {} : { afterSeq }) },
            },
          });
          if (cancelled) return;
          if (error !== undefined || data === undefined) {
            throw new Error(error === undefined ? "subagent activity could not be read" : describeError(error));
          }
          collected.push(...data.data);
          afterSeq = data.nextAfterSeq;
          if (afterSeq !== undefined && seen.has(afterSeq)) throw new Error("subagent activity repeated a cursor");
          if (afterSeq !== undefined) seen.add(afterSeq);
        } while (afterSeq !== undefined);
        setSnapshot(collected);
      } catch (cause) {
        if (!cancelled) setDetailFailure(`Subagent activity could not be refreshed: ${describeError(cause)}.`);
      } finally {
        if (!cancelled) setDetailLoading(false);
      }
    })();
    return () => { cancelled = true; };
  }, [detailAttempt, selectedId, sessionId]);

  const activity = useMemo(
    () => selectedId === null ? [] : mergeSubagentActivity(snapshot, liveActivity[selectedId] ?? []),
    [liveActivity, selectedId, snapshot],
  );
  const active = subagents.filter((subagent) => subagent.status === "active");
  const completed = subagents.filter((subagent) => subagent.status !== "active");

  if (loading && subagents.length === 0) return <PaneLoading label="Loading subagents" />;
  if (selectedId !== null) {
    return (
      <div className="subagent-detail pane-content">
        <header className="subagent-detail-head">
          <button type="button" className="back-button" onClick={() => setSelectedId(null)} aria-label="Back to subagents">←</button>
          <span className="agent-sun" aria-hidden="true">☀</span>
          <div><strong>{selected?.displayName ?? "Subagent details"}</strong>{selected !== null && <small>{selected.status === "active" ? "Working" : selected.status}</small>}</div>
          {selected !== null && <span className={`state-badge ${selected.status}`}>{selected.status}</span>}
        </header>
        {failure !== null && <p className="error" role="alert">{failure}</p>}
        {detailFailure !== null && <div className="pane-error"><p className="error" role="alert">{detailFailure}</p><button type="button" className="ghost" disabled={detailLoading} onClick={() => setDetailAttempt((current) => current + 1)}>Retry subagent details</button></div>}
        {selected !== null && (
          <>
            <div className="agent-duration">
              <strong>{durationLabel(selected, now)}</strong>
              <span>{selected.activityCount} updates</span>
            </div>
            {selected.summary !== undefined && <div className="agent-summary"><Markdown source={selected.summary} /></div>}
          </>
        )}
        {detailLoading && activity.length === 0 ? <PaneLoading label="Loading subagent activity" /> : (activity.length > 0 || detailFailure === null) && (
          <ol className="activity-list">
            {activity.map((item) => (
              <li key={item.seq} className={`activity-${item.kind}`}>
                <span className="activity-icon" aria-hidden="true">{activityIcon(item.kind)}</span>
                <div>
                  <header>
                    <strong>{item.toolName ?? item.kind.replace(/([A-Z])/g, " $1")}</strong>
                    <time title={absoluteTime(item.occurredAt)}>{new Date(item.occurredAt).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" })}</time>
                  </header>
                  {item.kind === "message" || item.kind === "progress" ? <Markdown source={item.text} /> : <pre>{item.text}</pre>}
                </div>
              </li>
            ))}
            {activity.length === 0 && <li className="activity-empty">No activity has been reported yet.</li>}
          </ol>
        )}
      </div>
    );
  }

  return (
    <div className="subagents-pane pane-content">
      <header className="pane-toolbar">
        <div><strong>Subagents</strong><span className="pane-subtitle">Live delegated work</span></div>
        <button type="button" className="ghost" disabled={loading} onClick={() => void load()}>{loading ? "Refreshing…" : "↻ Refresh"}</button>
      </header>
      {failure !== null && <p className="error" role="alert">{failure}</p>}
      {subagents.length === 0 ? (
        <div className="pane-empty"><span className="agent-empty-icon" aria-hidden="true">☀</span><strong>No subagents yet</strong><small>Delegated work will appear here live.</small></div>
      ) : (
        <>
          <AgentSection title="Active" agents={active} now={now} onSelect={setSelectedId} />
          <AgentSection title="Completed" agents={completed} now={now} onSelect={setSelectedId} />
        </>
      )}
    </div>
  );
}

function AgentSection({ title, agents, now, onSelect }: { title: string; agents: Subagent[]; now: Date; onSelect: (id: string) => void }) {
  if (agents.length === 0) return null;
  return (
    <section className="agent-section">
      <h3>{title} · {agents.length}</h3>
      <ul>
        {agents.map((agent) => (
          <li key={agent.id}>
            <button type="button" className="agent-row" onClick={() => onSelect(agent.id)}>
              <span className="agent-sun" aria-hidden="true">☀</span>
              <span className="agent-row-copy"><strong>{agent.displayName}</strong><small>{agent.summary ?? (agent.status === "active" ? "Working" : agent.status)}</small></span>
              <time>{duration(agent.startedAt, agent.completedAt, now)}</time>
              <span className="row-arrow" aria-hidden="true">›</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}
