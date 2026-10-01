import { describe, expect, it } from "vitest";
import type { SessionEvent, TranscriptItem } from "./api/client";
import { applyEvent, applyTranscriptItem, emptyTranscript } from "./transcript";

const sessionId = "11111111-1111-4111-8111-111111111111";
const firstTurnId = "33333333-3333-4333-8333-333333333333";
const secondTurnId = "44444444-4444-4444-8444-444444444444";
const occurredAt = "2026-09-04T00:00:00Z";

function delta(seq: number, text: string, turnId = firstTurnId): SessionEvent {
  return {
    seq,
    sessionId,
    kind: "assistantDelta",
    occurredAt,
    payload: { turnId, text },
  };
}

function completed(
  seq: number,
  turnId: string,
  status: "completed" | "aborted" | "failed",
): SessionEvent {
  return {
    seq,
    sessionId,
    kind: "turnCompleted",
    occurredAt,
    payload: {
      id: turnId,
      sessionId,
      status,
      createdAt: occurredAt,
      completedAt: occurredAt,
    },
  };
}

function appended(seq: number, item: TranscriptItem): SessionEvent {
  return {
    seq,
    sessionId: item.sessionId,
    kind: "transcriptAppended",
    occurredAt: item.occurredAt,
    payload: item,
  };
}

function item(overrides: Partial<TranscriptItem> & Pick<TranscriptItem, "seq" | "kind">) {
  return {
    sessionId,
    occurredAt,
    text: "",
    ...overrides,
  } as TranscriptItem;
}

describe("applyEvent", () => {
  it("collapses assistant deltas into one growing message", () => {
    let state = emptyTranscript;
    state = applyEvent(state, delta(1, "Hel"));
    state = applyEvent(state, delta(2, "lo, "));
    state = applyEvent(state, delta(3, "world"));

    expect(state.items).toHaveLength(1);
    const only = state.items[0];
    expect(only?.kind).toBe("message");
    if (only?.kind !== "message") throw new Error("expected a message row");
    expect(only.text).toBe("Hello, world");
    expect(only.streaming).toBe(true);
    expect(only.role).toBe("assistant");
  });

  it("settles the streaming message onto the persisted assistant item", () => {
    let state = applyEvent(emptyTranscript, delta(1, "partial"));
    state = applyEvent(
      state,
      appended(2, item({ seq: 2, turnId: firstTurnId, kind: "assistantMessage", text: "partial answer" })),
    );

    expect(state.items).toHaveLength(1);
    const settled = state.items[0];
    if (settled?.kind !== "message") throw new Error("expected a message row");
    expect(settled.text).toBe("partial answer");
    expect(settled.streaming).toBe(false);
    expect(settled.turnId).toBe(firstTurnId);
  });

  it.each(["aborted", "failed"] as const)(
    "keeps a %s turn's partial response separate from its successor",
    (status) => {
      let state = applyEvent(emptyTranscript, delta(1, "first partial", firstTurnId));
      state = applyEvent(state, completed(2, firstTurnId, status));
      state = applyEvent(state, delta(3, "second ", secondTurnId));
      state = applyEvent(state, delta(4, "stream", secondTurnId));
      state = applyEvent(
        state,
        appended(5, item({
          seq: 1,
          turnId: secondTurnId,
          kind: "assistantMessage",
          text: "second answer",
        })),
      );

      expect(state.items).toHaveLength(2);
      const first = state.items[0];
      const second = state.items[1];
      if (first?.kind !== "message" || second?.kind !== "message") {
        throw new Error("expected message rows");
      }
      expect(first).toMatchObject({
        text: "first partial",
        turnId: firstTurnId,
        streaming: false,
      });
      expect(second).toMatchObject({
        text: "second answer",
        turnId: secondTurnId,
        streaming: false,
      });
    },
  );

  it("keeps a successfully completed row eligible for its canonical item", () => {
    let state = applyEvent(emptyTranscript, delta(1, "partial", firstTurnId));
    state = applyEvent(state, completed(2, firstTurnId, "completed"));

    const growing = state.items[0];
    if (growing?.kind !== "message") throw new Error("expected a message row");
    expect(growing.streaming).toBe(true);

    state = applyEvent(
      state,
      appended(3, item({
        seq: 1,
        turnId: firstTurnId,
        kind: "assistantMessage",
        text: "canonical answer",
      })),
    );

    expect(state.items).toHaveLength(1);
    const settled = state.items[0];
    if (settled?.kind !== "message") throw new Error("expected a message row");
    expect(settled).toMatchObject({
      text: "canonical answer",
      turnId: firstTurnId,
      streaming: false,
    });
  });

  it("settles only the streaming row owned by the persisted item's turn", () => {
    let state = applyEvent(emptyTranscript, delta(1, "first partial", firstTurnId));
    state = applyEvent(state, delta(2, "second partial", secondTurnId));
    state = applyEvent(
      state,
      appended(3, item({
        seq: 1,
        turnId: firstTurnId,
        kind: "assistantMessage",
        text: "first answer",
      })),
    );

    expect(state.items).toHaveLength(2);
    const first = state.items[0];
    const second = state.items[1];
    if (first?.kind !== "message" || second?.kind !== "message") {
      throw new Error("expected message rows");
    }
    expect(first).toMatchObject({ text: "first answer", streaming: false });
    expect(second).toMatchObject({ text: "second partial", streaming: true });
  });

  it("does not correlate a turnless persisted item with a streaming turn", () => {
    let state = applyEvent(emptyTranscript, delta(1, "turn partial"));
    state = applyEvent(
      state,
      appended(2, item({ seq: 1, kind: "assistantMessage", text: "turnless answer" })),
    );

    expect(state.items).toHaveLength(2);
    const partial = state.items[0];
    const turnless = state.items[1];
    if (partial?.kind !== "message" || turnless?.kind !== "message") {
      throw new Error("expected message rows");
    }
    expect(partial).toMatchObject({ text: "turn partial", streaming: true });
    expect(turnless).toMatchObject({ text: "turnless answer", streaming: false });
    expect(turnless.turnId).toBeUndefined();
  });

  it("pairs a tool call and its result into one card", () => {
    let state = applyEvent(
      emptyTranscript,
      appended(1, item({ seq: 1, kind: "toolCall", toolName: "bash", text: "ls -la" })),
    );
    state = applyEvent(
      state,
      appended(2, item({ seq: 2, kind: "toolResult", toolName: "bash", text: "README.md" })),
    );

    expect(state.items).toHaveLength(1);
    const card = state.items[0];
    if (card?.kind !== "tool") throw new Error("expected a tool card");
    expect(card.toolName).toBe("bash");
    expect(card.args).toBe("ls -la");
    expect(card.result).toBe("README.md");
  });

  it("keeps a second call to the same tool on its own card", () => {
    let state = applyEvent(
      emptyTranscript,
      appended(1, item({ seq: 1, kind: "toolCall", toolName: "bash", text: "one" })),
    );
    state = applyEvent(
      state,
      appended(2, item({ seq: 2, kind: "toolCall", toolName: "bash", text: "two" })),
    );
    state = applyEvent(
      state,
      appended(3, item({ seq: 3, kind: "toolResult", toolName: "bash", text: "second done" })),
    );

    expect(state.items).toHaveLength(2);
    const first = state.items[0];
    const second = state.items[1];
    if (first?.kind !== "tool" || second?.kind !== "tool") throw new Error("expected tool cards");
    expect(first.result).toBeUndefined();
    expect(second.result).toBe("second done");
  });

  it("pairs a result with its own call id when two calls to one tool overlap", () => {
    let state = applyEvent(
      emptyTranscript,
      appended(1, item({ seq: 1, kind: "toolCall", toolName: "bash", toolCallId: "a", text: "one" })),
    );
    state = applyEvent(
      state,
      appended(2, item({ seq: 2, kind: "toolCall", toolName: "bash", toolCallId: "b", text: "two" })),
    );
    // The FIRST call finishes second. Pairing on the tool name alone would have
    // handed this to the newer card.
    state = applyEvent(
      state,
      appended(3, item({ seq: 3, kind: "toolResult", toolName: "bash", toolCallId: "a", text: "one done" })),
    );

    expect(state.items).toHaveLength(2);
    const first = state.items[0];
    const second = state.items[1];
    if (first?.kind !== "tool" || second?.kind !== "tool") throw new Error("expected tool cards");
    expect(first.args).toBe("one");
    expect(first.result).toBe("one done");
    expect(second.result).toBeUndefined();
  });

  it.each(["completed", "aborted", "failed"] as const)(
    "settles an unpaired tool card when its turn is %s",
    (status) => {
      let state = applyTranscriptItem(
        emptyTranscript,
        item({
          seq: 1,
          turnId: firstTurnId,
          kind: "toolCall",
          toolName: "task",
          toolCallId: "call-one",
          text: '{"description":"delegate"}',
        }),
      );

      state = applyEvent(state, completed(2, firstTurnId, status));

      const card = state.items[0];
      if (card?.kind !== "tool") throw new Error("expected a tool card");
      expect(card).toMatchObject({ turnId: firstTurnId, terminalStatus: status });
      expect(card.result).toBeUndefined();
    },
  );

  it("advances the Last-Event-ID resume point monotonically, heartbeats included", () => {
    let state = applyEvent(emptyTranscript, delta(7, "x"));
    expect(state.lastEventId).toBe(7);

    state = applyEvent(state, {
      seq: 9,
      sessionId: "11111111-1111-4111-8111-111111111111",
      kind: "heartbeat",
      occurredAt: "2026-09-04T00:00:01Z",
    });
    expect(state.lastEventId).toBe(9);
    expect(state.items).toHaveLength(1);

    // An out-of-order replay never rewinds the resume point.
    state = applyEvent(state, delta(4, "y"));
    expect(state.lastEventId).toBe(9);
  });

  it("leaves the rows untouched for lifecycle kinds", () => {
    const state = applyEvent(emptyTranscript, {
      seq: 3,
      sessionId: "11111111-1111-4111-8111-111111111111",
      kind: "turnStarted",
      occurredAt: "2026-09-04T00:00:00Z",
      payload: {
        id: "33333333-3333-4333-8333-333333333333",
        sessionId: "11111111-1111-4111-8111-111111111111",
        status: "running",
        createdAt: "2026-09-04T00:00:00Z",
      },
    });
    expect(state.items).toEqual([]);
    expect(state.lastEventId).toBe(3);
  });
});

describe("applyTranscriptItem", () => {
  it("replays a page through the same collapsing rules the stream uses", () => {
    const page: TranscriptItem[] = [
      item({ seq: 1, kind: "userMessage", author: "candace", text: "hi" }),
      item({ seq: 2, kind: "toolCall", toolName: "read", text: "main.go" }),
      item({ seq: 3, kind: "toolResult", toolName: "read", text: "package main" }),
      item({ seq: 4, kind: "assistantMessage", text: "done" }),
    ];

    const state = page.reduce(applyTranscriptItem, emptyTranscript);

    expect(state.items.map((row) => row.kind)).toEqual(["message", "tool", "message"]);
    const first = state.items[0];
    if (first?.kind !== "message") throw new Error("expected a message row");
    expect(first.author).toBe("candace");
    expect(state.lastItemSeq).toBe(4);
    // The snapshot never advances the session-event resume point: the two seq
    // counters are independent.
    expect(state.lastEventId).toBe(0);
  });

  it("folds a replayed page idempotently, so a stream replay adds no rows", () => {
    const page: TranscriptItem[] = [
      item({ seq: 1, kind: "userMessage", author: "candace", text: "hi" }),
      item({ seq: 2, kind: "toolCall", toolName: "read", text: "main.go" }),
      item({ seq: 3, kind: "toolResult", toolName: "read", text: "package main" }),
      item({ seq: 4, kind: "assistantMessage", text: "done" }),
    ];
    const snapshot = page.reduce(applyTranscriptItem, emptyTranscript);

    // The stream replays every stored event on top of the installed snapshot.
    let replayed = snapshot;
    page.forEach((entry, index) => {
      replayed = applyEvent(replayed, appended(index + 1, entry));
    });

    expect(replayed.items).toEqual(snapshot.items);
    expect(replayed.lastItemSeq).toBe(4);
    expect(replayed.lastEventId).toBe(4);
  });

  it("discards the streaming row that a stale replayed delta rebuilt", () => {
    const persisted = item({
      seq: 4,
      turnId: firstTurnId,
      kind: "assistantMessage",
      text: "done",
    });
    const snapshot = applyTranscriptItem(emptyTranscript, persisted);

    // Replay hands us the deltas for that same message before the item itself.
    let replayed = applyEvent(snapshot, delta(1, "do"));
    replayed = applyEvent(replayed, delta(2, "ne"));
    expect(replayed.items).toHaveLength(2);

    replayed = applyEvent(replayed, appended(3, persisted));
    expect(replayed.items).toHaveLength(1);
    const only = replayed.items[0];
    if (only?.kind !== "message") throw new Error("expected a message row");
    expect(only.text).toBe("done");
    expect(only.streaming).toBe(false);

    // A live delta after the replay still opens a fresh streaming row.
    replayed = applyEvent(replayed, delta(4, "next", secondTurnId));
    expect(replayed.items).toHaveLength(2);

    // Replaying the older persisted item again may only clean up a streaming
    // row from that item's turn; it must not discard the successor's row.
    replayed = applyEvent(replayed, appended(5, persisted));
    expect(replayed.items).toHaveLength(2);
    const next = replayed.items[1];
    if (next?.kind !== "message") throw new Error("expected a message row");
    expect(next).toMatchObject({ text: "next", turnId: secondTurnId, streaming: true });
  });
});
