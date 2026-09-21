import type { SessionEvent, TranscriptItem } from "./api/client";

type TurnStatus = Extract<SessionEvent, { kind: "turnCompleted" }>["payload"]["status"];
type ToolTerminalStatus = "completed" | "aborted" | "failed";
const partialTerminalStatuses = new Set<TurnStatus>(["aborted", "failed"]);

function toolTerminalStatus(status: TurnStatus): ToolTerminalStatus | undefined {
  switch (status) {
    case "completed":
    case "aborted":
    case "failed":
      return status;
    default:
      return undefined;
  }
}

// A rendered transcript row. Assistant token deltas collapse into ONE growing
// message and a toolCall/toolResult pair collapses into ONE card, so the row
// list is what the screen shows rather than what the wire carried.
export type TranscriptEntry =
  | {
      kind: "message";
      key: string;
      seq: number;
      // Required on live assistant rows; optional for persisted turnless items.
      turnId?: string;
      role: "user" | "assistant" | "system";
      text: string;
      author?: string;
      occurredAt: string;
      streaming: boolean;
    }
  | {
      kind: "tool";
      key: string;
      seq: number;
      turnId?: string;
      toolName: string;
      // The CLI's identifier for this invocation. A result pairs on it, so two
      // concurrent calls to the same tool cannot claim each other's output.
      toolCallId?: string;
      args: string;
      result?: string;
      terminalStatus?: ToolTerminalStatus;
      occurredAt: string;
    };

export type TranscriptState = {
  items: TranscriptEntry[];
  // The seq of the newest SESSION EVENT folded in. Sent as Last-Event-ID on
  // reconnect, so the stream resumes immediately after it. Session events and
  // transcript items are numbered by two independent counters, so this is
  // never advanced from a transcript item's seq.
  lastEventId: number;
  // The seq of the newest TRANSCRIPT ITEM folded in. The snapshot and the
  // replayed stream carry the same items, so this is the watermark that makes
  // the fold idempotent: an item at or below it has already been shown.
  lastItemSeq: number;
};

export const emptyTranscript: TranscriptState = { items: [], lastEventId: 0, lastItemSeq: 0 };

function isStreamingAssistantForTurn(
  entry: TranscriptEntry,
  turnId: string | undefined,
): entry is Extract<TranscriptEntry, { kind: "message" }> {
  return turnId !== undefined &&
    entry.kind === "message" &&
    entry.role === "assistant" &&
    entry.streaming &&
    entry.turnId === turnId;
}

function roleOf(kind: TranscriptItem["kind"]): "user" | "assistant" | "system" {
  if (kind === "userMessage") return "user";
  if (kind === "assistantMessage") return "assistant";
  return "system";
}

// Folds one persisted transcript item into the state. Exported because the
// first page load replays GET /transcript through exactly the same collapsing
// rules the live stream uses.
//
// The fold is idempotent by item seq. The stream replays stored events, so it
// re-delivers items the snapshot already installed; appending them a second
// time is what duplicated messages and tool cards, and an item at or below
// lastItemSeq is therefore dropped rather than folded again.
export function applyTranscriptItem(
  state: TranscriptState,
  item: TranscriptItem,
): TranscriptState {
  if (item.seq <= state.lastItemSeq) {
    // Already shown. One repeat still carries information: a persisted
    // assistantMessage means the assistant deltas replayed just before it are
    // stale, so the streaming row they rebuilt is discarded in favour of the
    // settled body the snapshot already holds.
    if (item.kind !== "assistantMessage") return state;
    const settled = state.items.filter(
      (entry) => !isStreamingAssistantForTurn(entry, item.turnId),
    );
    return settled.length === state.items.length ? state : { ...state, items: settled };
  }
  return { ...state, lastItemSeq: item.seq, items: foldItem(state.items, item) };
}

function foldItem(items: TranscriptEntry[], item: TranscriptItem): TranscriptEntry[] {
  if (item.kind === "toolCall") {
    return [
      ...items,
      {
        kind: "tool",
        key: `tool-${item.seq}`,
        seq: item.seq,
        ...(item.turnId === undefined ? {} : { turnId: item.turnId }),
        toolName: item.toolName ?? "tool",
        ...(item.toolCallId === undefined ? {} : { toolCallId: item.toolCallId }),
        args: item.text,
        occurredAt: item.occurredAt,
      },
    ];
  }

  if (item.kind === "toolResult") {
    // The CLI's toolCallId is the stable pairing key: two concurrent calls to
    // the same tool share a name but never an id. Falling back to the name
    // keeps a CLI that reports no id working, at the old ambiguity.
    const target = lastIndexWhere(items, (entry) => {
      if (entry.kind !== "tool" || entry.result !== undefined) return false;
      if (item.toolCallId !== undefined) return entry.toolCallId === item.toolCallId;
      return entry.toolCallId === undefined && entry.toolName === (item.toolName ?? "tool");
    });
    if (target < 0) {
      return [
        ...items,
        {
          kind: "tool",
          key: `tool-${item.seq}`,
          seq: item.seq,
          ...(item.turnId === undefined ? {} : { turnId: item.turnId }),
          toolName: item.toolName ?? "tool",
          ...(item.toolCallId === undefined ? {} : { toolCallId: item.toolCallId }),
          args: "",
          result: item.text,
          occurredAt: item.occurredAt,
        },
      ];
    }
    const paired = items.slice();
    const call = paired[target] as Extract<TranscriptEntry, { kind: "tool" }>;
    paired[target] = { ...call, result: item.text };
    return paired;
  }

  if (item.kind === "assistantMessage") {
    // The persisted item is the authoritative collapsed body: it replaces the
    // deltas that were streaming into place rather than appending after them.
    const streaming = lastIndexWhere(
      items,
      (entry) => isStreamingAssistantForTurn(entry, item.turnId),
    );
    if (streaming >= 0) {
      const settled = items.slice();
      const growing = settled[streaming] as Extract<TranscriptEntry, { kind: "message" }>;
      settled[streaming] = {
        ...growing,
        seq: item.seq,
        key: `item-${item.seq}`,
        text: item.text,
        occurredAt: item.occurredAt,
        streaming: false,
      };
      return settled;
    }
  }

  return [
    ...items,
    {
      kind: "message",
      key: `item-${item.seq}`,
      seq: item.seq,
      ...(item.turnId === undefined ? {} : { turnId: item.turnId }),
      role: roleOf(item.kind),
      text: item.text,
      ...(item.author === undefined ? {} : { author: item.author }),
      occurredAt: item.occurredAt,
      streaming: false,
    },
  ];
}

function lastIndexWhere(
  items: TranscriptEntry[],
  match: (entry: TranscriptEntry) => boolean,
): number {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    const entry = items[index];
    if (entry !== undefined && match(entry)) return index;
  }
  return -1;
}

// The pure reducer. Every SSE frame and every replayed page goes through it, so
// the collapsing rules cannot drift between the two paths.
export function applyEvent(state: TranscriptState, event: SessionEvent): TranscriptState {
  const lastEventId = Math.max(state.lastEventId, event.seq);

  if (event.kind === "transcriptAppended") {
    return { ...applyTranscriptItem(state, event.payload), lastEventId };
  }

  if (event.kind === "assistantDelta") {
    const chunk = event.payload.text;
    if (chunk === "") return { ...state, lastEventId };
    const growingAt = lastIndexWhere(
      state.items,
      (entry) => isStreamingAssistantForTurn(entry, event.payload.turnId),
    );
    if (growingAt < 0) {
      return {
        ...state,
        lastEventId,
        items: [
          ...state.items,
          {
            kind: "message",
            key: `delta-${event.seq}`,
            seq: event.seq,
            turnId: event.payload.turnId,
            role: "assistant",
            text: chunk,
            occurredAt: event.occurredAt,
            streaming: true,
          },
        ],
      };
    }
    const grown = state.items.slice();
    const growing = grown[growingAt] as Extract<TranscriptEntry, { kind: "message" }>;
    grown[growingAt] = { ...growing, text: growing.text + chunk };
    return { ...state, items: grown, lastEventId };
  }

  if (event.kind === "turnCompleted") {
    let changed = false;
    const settlePartial = partialTerminalStatuses.has(event.payload.status);
    const settledToolStatus = toolTerminalStatus(event.payload.status);
    const settled = state.items.map((entry) => {
      if (settlePartial && isStreamingAssistantForTurn(entry, event.payload.id)) {
        changed = true;
        return { ...entry, streaming: false };
      }
      if (
        settledToolStatus !== undefined &&
        entry.kind === "tool" &&
        entry.turnId === event.payload.id &&
        entry.result === undefined
      ) {
        changed = true;
        return { ...entry, terminalStatus: settledToolStatus };
      }
      return entry;
    });
    return changed ? { ...state, items: settled, lastEventId } : { ...state, lastEventId };
  }

  // heartbeat, sessionUpdated, turnStarted, requestOpened and requestResolved
  // change no transcript row; they only advance the resume point, and the
  // screens refetch their own state off them.
  return { ...state, lastEventId };
}
