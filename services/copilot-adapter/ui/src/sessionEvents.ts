import type {
  Session,
  SessionEvent,
  SessionRequest,
  Subagent,
  SubagentActivity,
} from "./api/client";
import { compareInstants } from "./format";
import { applyEvent, emptyTranscript } from "./transcript";
import type { TranscriptState } from "./transcript";

export type SessionLiveState = {
  session: Session | null;
  transcript: TranscriptState;
  requests: SessionRequest[];
  subagents: Subagent[];
  activityBySubagent: Record<string, SubagentActivity[]>;
  worktreeRevision: number;
};

export function newSessionLiveState(session: Session | null = null): SessionLiveState {
  return {
    session,
    transcript: emptyTranscript,
    requests: [],
    subagents: [],
    activityBySubagent: {},
    worktreeRevision: 0,
  };
}

function upsert<T>(items: T[], item: T, idOf: (candidate: T) => string): T[] {
  const index = items.findIndex((candidate) => idOf(candidate) === idOf(item));
  if (index < 0) return [item, ...items];
  const next = items.slice();
  next[index] = item;
  return next;
}

function appendActivity(
  activity: Record<string, SubagentActivity[]>,
  item: SubagentActivity,
): Record<string, SubagentActivity[]> {
  const current = activity[item.subagentId] ?? [];
  if (current.some((candidate) => candidate.seq === item.seq)) return activity;
  return {
    ...activity,
    [item.subagentId]: [...current, item].sort((left, right) => left.seq - right.seq),
  };
}

function unreachable(event: never): never {
  throw new Error(`unknown session event: ${JSON.stringify(event)}`);
}

// The one live-state fold for the workbench. SessionEvent is generated as a
// discriminated union, so every contract event must be handled here before the
// UI can compile. Transcript, requests and subagents therefore share exactly
// one resume watermark and cannot race independent EventSource connections.
export function applySessionEvent(state: SessionLiveState, event: SessionEvent): SessionLiveState {
  const next = { ...state, transcript: applyEvent(state.transcript, event) };
  switch (event.kind) {
    case "sessionUpdated":
      // Replay still advances the watermark, but must not roll back the REST snapshot.
      return state.session !== null && compareInstants(event.payload.updatedAt, state.session.updatedAt) < 0
        ? next
        : { ...next, session: event.payload };
    case "turnStarted":
      return next;
    case "turnCompleted":
      return { ...next, worktreeRevision: state.worktreeRevision + 1 };
    case "transcriptAppended":
    case "assistantDelta":
      return next;
    case "requestOpened":
      return {
        ...next,
        requests: upsert(state.requests, event.payload, (request) => request.id),
      };
    case "requestResolved":
      return {
        ...next,
        requests: state.requests.filter((request) => request.id !== event.payload.id),
      };
    case "subagentUpdated":
      return {
        ...next,
        subagents: upsert(state.subagents, event.payload, (subagent) => subagent.id),
      };
    case "subagentActivityAppended":
      return {
        ...next,
        activityBySubagent: appendActivity(state.activityBySubagent, event.payload),
      };
    case "heartbeat":
      return next;
    default:
      return unreachable(event);
  }
}

const eventKinds = new Set<SessionEvent["kind"]>([
  "sessionUpdated",
  "turnStarted",
  "turnCompleted",
  "transcriptAppended",
  "assistantDelta",
  "requestOpened",
  "requestResolved",
  "subagentUpdated",
  "subagentActivityAppended",
  "heartbeat",
]);

export function decodeSessionEvent(data: string): SessionEvent | null {
  try {
    const candidate: unknown = JSON.parse(data);
    if (candidate === null || typeof candidate !== "object") return null;
    const envelope = candidate as Record<string, unknown>;
    if (
      typeof envelope["seq"] !== "number" ||
      typeof envelope["sessionId"] !== "string" ||
      typeof envelope["kind"] !== "string" ||
      typeof envelope["occurredAt"] !== "string" ||
      !eventKinds.has(envelope["kind"] as SessionEvent["kind"])
    ) {
      return null;
    }
    return candidate as SessionEvent;
  } catch {
    return null;
  }
}

export function mergeSubagentActivity(
  snapshot: SubagentActivity[],
  live: SubagentActivity[],
): SubagentActivity[] {
  const bySeq = new Map(snapshot.map((item) => [item.seq, item]));
  live.forEach((item) => bySeq.set(item.seq, item));
  return [...bySeq.values()].sort((left, right) => left.seq - right.seq);
}

export type CoalescedCallback = {
  run: () => void;
  cancel: () => void;
};

// Runs immediately once, then at most once per interval while calls continue.
// A final queued call is retained, so a burst of replayed session events does
// not fan out into one global reload per frame and does not leave the sidebar
// stale when the burst ends.
export function coalesceCallback(callback: () => void, intervalMillis: number): CoalescedCallback {
  let timer: ReturnType<typeof setTimeout> | null = null;
  let queued = false;

  const arm = () => {
    timer = setTimeout(() => {
      timer = null;
      if (!queued) return;
      queued = false;
      callback();
      arm();
    }, intervalMillis);
  };

  return {
    run: () => {
      if (timer === null) {
        callback();
        arm();
        return;
      }
      queued = true;
    },
    cancel: () => {
      if (timer !== null) clearTimeout(timer);
      timer = null;
      queued = false;
    },
  };
}
