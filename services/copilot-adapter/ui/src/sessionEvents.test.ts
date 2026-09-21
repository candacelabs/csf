import { afterEach, describe, expect, it, vi } from "vitest";
import type { Session, SessionEvent, SessionRequest, Subagent, SubagentActivity } from "./api/client";
import { applySessionEvent, coalesceCallback, decodeSessionEvent, mergeSubagentActivity, newSessionLiveState } from "./sessionEvents";

afterEach(() => vi.useRealTimers());

const sessionId = "11111111-1111-4111-8111-111111111111";

function subagent(overrides: Partial<Subagent> = {}): Subagent {
  return {
    id: "agent-one",
    sessionId,
    displayName: "Component export audit",
    status: "active",
    activityCount: 1,
    startedAt: "2026-09-05T00:00:00Z",
    updatedAt: "2026-09-05T00:00:01Z",
    ...overrides,
  };
}

function activity(seq: number, text: string): SubagentActivity {
  return {
    seq,
    sessionId,
    subagentId: "agent-one",
    kind: "progress",
    occurredAt: `2026-09-05T00:00:0${seq}Z`,
    text,
  };
}

function request(): SessionRequest {
  return {
    id: "22222222-2222-4222-8222-222222222222",
    sessionId,
    kind: "permission",
    status: "pending",
    prompt: "Run tests?",
    createdAt: "2026-09-05T00:00:00Z",
  };
}

function envelope(event: { kind: SessionEvent["kind"]; payload?: unknown }): SessionEvent {
  return {
    seq: 1,
    sessionId,
    occurredAt: "2026-09-05T00:00:01Z",
    ...event,
  } as unknown as SessionEvent;
}

describe("applySessionEvent", () => {
  it("advances replay without replacing a newer snapshot, preserving submillisecond ordering", () => {
    const snapshot: Session = {
      id: sessionId, displayName: "Audit", model: "model-a", worktreeId: "worktree-one",
      workingDirectory: "/workspace", status: "idle", turnCount: 1,
      createdAt: "2026-09-05T00:00:00Z", updatedAt: "2026-09-05T00:00:01.000002Z",
    };
    const historical = { ...snapshot, status: "running" as const, updatedAt: "2026-09-05T00:00:01.000001Z" };
    const replayed = applySessionEvent(newSessionLiveState(snapshot), envelope({ kind: "sessionUpdated", payload: historical }));
    expect(replayed.session).toBe(snapshot);
    expect(replayed.transcript.lastEventId).toBe(1);
    const current = { ...historical, updatedAt: "2026-09-05T00:00:01.000003Z" };
    const live = applySessionEvent(replayed, { ...envelope({ kind: "sessionUpdated", payload: current }), seq: 2 });
    expect(live.session).toBe(current);
    expect(live.transcript.lastEventId).toBe(2);
  });

  it("folds subagent identity and activity without duplicating replayed rows", () => {
    let state = applySessionEvent(newSessionLiveState(), envelope({ kind: "subagentUpdated", payload: subagent() }));
    state = applySessionEvent(state, envelope({ kind: "subagentActivityAppended", payload: activity(1, "Auditing") }));
    state = applySessionEvent(state, envelope({ kind: "subagentActivityAppended", payload: activity(1, "Auditing") }));

    expect(state.subagents).toHaveLength(1);
    expect(state.activityBySubagent["agent-one"]).toEqual([activity(1, "Auditing")]);
    expect(state.transcript.lastEventId).toBe(1);
  });

  it("adds an opened request and removes it when resolved", () => {
    const opened = applySessionEvent(newSessionLiveState(), envelope({ kind: "requestOpened", payload: request() }));
    expect(opened.requests).toHaveLength(1);
    const resolved = applySessionEvent(opened, envelope({ kind: "requestResolved", payload: { ...request(), status: "approved", resolvedAt: "2026-09-05T00:00:02Z" } }));
    expect(resolved.requests).toEqual([]);
  });
});

describe("decodeSessionEvent", () => {
  it("accepts a generated envelope and rejects malformed or unknown kinds", () => {
    expect(decodeSessionEvent(JSON.stringify(envelope({ kind: "heartbeat" })))?.kind).toBe("heartbeat");
    expect(decodeSessionEvent('{"kind":"heartbeat"}')).toBeNull();
    expect(decodeSessionEvent('{"seq":1,"sessionId":"x","kind":"surprise","occurredAt":"now"}')).toBeNull();
  });
});

describe("mergeSubagentActivity", () => {
  it("orders snapshot and live rows by sequence and lets the live copy win", () => {
    expect(mergeSubagentActivity([activity(2, "old")], [activity(1, "first"), activity(2, "new")]).map((item) => item.text)).toEqual(["first", "new"]);
  });
});

describe("coalesceCallback", () => {
  it("bounds a replay burst to one leading and one trailing callback per interval", () => {
    vi.useFakeTimers();
    const callback = vi.fn();
    const coalesced = coalesceCallback(callback, 500);

    for (let index = 0; index < 200; index += 1) coalesced.run();
    expect(callback).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(499);
    expect(callback).toHaveBeenCalledTimes(1);
    vi.advanceTimersByTime(1);
    expect(callback).toHaveBeenCalledTimes(2);

    coalesced.run();
    coalesced.cancel();
    vi.advanceTimersByTime(1_000);
    expect(callback).toHaveBeenCalledTimes(2);
  });
});
