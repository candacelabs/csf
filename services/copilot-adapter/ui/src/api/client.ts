import createClient from "openapi-fetch";
import type { components, paths } from "./schema";

// The one typed client in the app. Every call goes through it, so the OpenAPI
// document is the only source of request and response shapes.
//
// Same-origin by construction: in the browser the dev server proxies /v1 and
// /healthz at the adapter, and in production the adapter serves this bundle
// itself. The origin is spelled out rather than left relative because the
// Request constructor rejects a relative URL outside a browser document.
export const api = createClient<paths>({
  baseUrl: typeof window === "undefined" ? "http://127.0.0.1:8090" : window.location.origin,
  // Resolved per call rather than captured at construction, so a test can
  // replace globalThis.fetch after this module has already been imported.
  fetch: (request: Request) => globalThis.fetch(request),
});

type Schemas = components["schemas"];

export type Session = Schemas["Session"];
export type SessionStatus = Schemas["SessionStatus"];
export type Model = Schemas["Model"];
export type TranscriptItem = Schemas["TranscriptItem"];
export type SessionEvent = Schemas["SessionEvent"];
export type SessionRequest = Schemas["SessionRequest"];
export type ResolveDecision = Schemas["ResolveDecision"];
export type PromptMode = Schemas["PromptMode"];
export type Repository = Schemas["Repository"];
export type Worktree = Schemas["Worktree"];
export type WorktreeMode = Schemas["WorktreeMode"];
export type CreateSessionRequest = Schemas["CreateSessionRequest"];
export type WorktreeChanges = Schemas["WorktreeChanges"];
export type GitChange = Schemas["GitChange"];
export type Terminal = Schemas["Terminal"];
export type TerminalEvent = Schemas["TerminalEvent"];
export type ChatSchedule = Schemas["ChatSchedule"];
export type ChatScheduleStatus = Schemas["ChatScheduleStatus"];
export type Subagent = Schemas["Subagent"];
export type SubagentActivity = Schemas["SubagentActivity"];
export type ApiError = Schemas["Error"];

// The adapter answers every failure with the contract's Error body; this turns
// one into something renderable without leaking the envelope into components.
export function describeError(failure: unknown): string {
  if (failure instanceof Error) return failure.message;
  if (failure && typeof failure === "object" && "message" in failure) {
    const envelope = failure as ApiError;
    return `${envelope.code}: ${envelope.message}`;
  }
  return String(failure);
}

// A rejected fetch proves only that the browser received no response. The
// server may already have committed a mutation, so callers must not describe
// the operation as failed or invite a blind duplicate.
export function describeAmbiguousMutation(action: string, failure: unknown): string {
  return `${action} did not receive a response and may have succeeded. Refresh before retrying. ${describeError(failure)}`;
}

// crypto.randomUUID is restricted to secure contexts in browsers. The
// workbench is also served on trusted plain-HTTP tailnet origins, where
// getRandomValues remains available, so build the RFC 4122 v4 value from that
// primitive instead of making prompt delivery depend on TLS termination.
export function newClientUUID(random: Pick<Crypto, "getRandomValues"> = globalThis.crypto): string {
  const bytes = random.getRandomValues(new Uint8Array(16));
  bytes[6] = ((bytes[6] ?? 0) & 0x0f) | 0x40;
  bytes[8] = ((bytes[8] ?? 0) & 0x3f) | 0x80;
  const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
