import type { SessionStatus } from "./api/client";

// Presentation data only. The OpenAPI SessionStatus remains the wire contract.
export const sessionLanes = {
  starting: { label: "Starting", color: "orange", description: "The session is starting." },
  running: { label: "Running", color: "teal", description: "The session reports active execution." },
  idle: { label: "Idle", color: "blue", description: "The session is waiting; task completion is unknown." },
  failed: { label: "Failed", color: "red", description: "The session failed; inspect its conversation for details." },
  ended: { label: "Ended", color: "gray", description: "The session ended. This does not mean its task was accepted." },
} satisfies Record<SessionStatus, { label: string; color: string; description: string }>;

// One registry renders both the hover hints and the keyboard/touch detail dialog.
// These are wiring plans, not fabricated API fields or functioning integrations.
export const kanbanScaffolds = [
  {
    label: "Task planning",
    summary: "Browser-local planning moves work. Shared planning and ticket links remain scaffolded; session status is observed independently.",
    reuse: "Existing WorkStatus, Checkpoint and ResumeRecord contracts; GitHub issues are the current task authority.",
    plan: "Associate sessions with explicit task and checkpoint IDs. Project task status, owner and next action into OpenAPI; add a checked transition before enabling card moves. Never infer an issue from a session title.",
    tools: "Liquid Proto → Go bindings; OpenAPI → Go handlers and TypeScript client; SQLC for durable associations. Dispatch stays in the existing Go host.",
    acceptance: "A move updates the authoritative checkpoint, survives refresh and links its receipt. Session runtime state remains independent.",
  },
  {
    label: "Langfuse traces",
    summary: "Per-session trace links are scaffolded. Existing trace-delivery totals are not navigable trace records.",
    reuse: "The existing OTLP exporter and retained trace deliveries already associate observations with session and optional turn IDs.",
    plan: "Expose recorded trace identity and configured provider URLs through a per-session OpenAPI projection. Link the returned identity; never invent a trace URL from a session ID.",
    tools: "Existing OpenTelemetry/Langfuse exporter; SQLC delivery queries → OpenAPI → generated TypeScript client.",
    acceptance: "Opening a trace from a card reaches the recorded session/turn observation. Missing or undelivered observations stay explicit.",
  },
  {
    label: "Distributed trace",
    summary: "Cross-service trace navigation is scaffolded. A shared session ID alone does not prove causal linkage.",
    reuse: "Existing TraceContext carries trace_id, span_id and trace_flags; work checkpoints can retain it.",
    plan: "Propagate the recorded context at existing service boundaries and expose trace/span IDs with evidence links in the generated browser contract.",
    tools: "Liquid Proto TraceContext; OpenTelemetry propagation; OpenAPI browser projection; existing log and trace sinks.",
    acceptance: "Following the recorded trace connects the originating action, service operation and receipt; absent context is marked unknown.",
  },
  {
    label: "Shared context",
    summary: "Cross-session context handoff is scaffolded. Shared knowledge storage does not prove an agent received the same context.",
    reuse: "Existing Search/GetDocument and symbolic graph contracts identify source revisions and content hashes.",
    plan: "Define a versioned context manifest referencing those sources and task checkpoints. Associate it with sessions and record delivery/consumption receipts.",
    tools: "Liquid Proto manifest → generated validators/bindings; existing knowledge retrieval; OpenAPI session projection.",
    acceptance: "Two sessions can show the exact manifest revision each received, with source links and explicit missing acknowledgements.",
  },
] as const;
