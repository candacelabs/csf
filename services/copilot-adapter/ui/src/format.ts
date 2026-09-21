// Presentation-only formatting. Nothing here touches the API client or the
// transcript reducer; it turns values those already produced into the strings
// the screens print.

import type { Worktree } from "./api/client";

export function worktreeLabel(worktree: Worktree): string {
  if (worktree.branch !== "") return worktree.branch;
  const segments = worktree.path.split("/").filter(Boolean);
  return segments.at(-1) ?? "Detached worktree";
}

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;

// A timestamp the way a chat reads it: "just now", "4m ago", "3h ago",
// "yesterday", then an absolute date once relative wording stops helping.
// `now` is a parameter rather than a call to Date.now() so a spec can pin it —
// house rule CS-9's spirit applied to the UI: tests never hand-roll time.
export function relativeTime(timestamp: string, now: Date): string {
  const at = new Date(timestamp);
  if (Number.isNaN(at.getTime())) return timestamp;

  const elapsed = now.getTime() - at.getTime();
  // A clock skew between the adapter and the browser can date an event a few
  // seconds into the future; that is still "just now", not "in 3 seconds".
  if (elapsed < MINUTE) return "just now";
  if (elapsed < HOUR) return `${Math.floor(elapsed / MINUTE)}m ago`;
  if (elapsed < DAY) return `${Math.floor(elapsed / HOUR)}h ago`;
  if (elapsed < 2 * DAY) return "yesterday";
  if (elapsed < 7 * DAY) return `${Math.floor(elapsed / DAY)}d ago`;
  return at.toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

// The exact wall-clock reading, for the title attribute behind the relative one.
export function absoluteTime(timestamp: string): string {
  const at = new Date(timestamp);
  return Number.isNaN(at.getTime()) ? timestamp : at.toLocaleString();
}

// The name printed above a transcript row. A group message carries its author;
// everything else is named by its role, and the assistant is the product's name
// rather than the wire's word.
export function authorLabel(role: "user" | "assistant" | "system", author?: string): string {
  if (author !== undefined && author !== "") return author;
  if (role === "assistant") return "Copilot";
  if (role === "system") return "System";
  return "You";
}

// The one or two letters in an author's avatar.
export function initials(label: string): string {
  const words = label.trim().split(/\s+/).filter((word) => word !== "");
  if (words.length === 0) return "?";
  const first = words[0] ?? "";
  if (words.length === 1) return first.slice(0, 1).toUpperCase();
  const second = words[1] ?? "";
  return (first.slice(0, 1) + second.slice(0, 1)).toUpperCase();
}

export type RequestSummary = { headline: string; detail: string | null };

function readString(source: Record<string, unknown>, key: string): string | null {
  const value = source[key];
  return typeof value === "string" && value !== "" ? value : null;
}

// The first `fullCommandText` or `identifier` inside a list-of-objects field.
function firstIn(source: Record<string, unknown>, key: string): string | null {
  const list = source[key];
  if (!Array.isArray(list)) return null;
  for (const element of list) {
    if (element === null || typeof element !== "object") continue;
    const entry = element as Record<string, unknown>;
    const text = readString(entry, "fullCommandText") ?? readString(entry, "identifier");
    if (text !== null) return text;
  }
  return null;
}

// A permission request's prompt arrives as the CLI's raw JSON payload for the
// tool it wants to run — accurate, and unreadable as a paragraph. This pulls
// out the line a person actually decides on and hands the rest back to be put
// behind a disclosure; nothing is dropped, it is only ordered.
export function summarizeRequestPrompt(prompt: string): RequestSummary {
  const trimmed = prompt.trim();
  if (!trimmed.startsWith("{")) return { headline: prompt, detail: null };
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return { headline: prompt, detail: null };
    }
    const fields = parsed as Record<string, unknown>;
    const headline =
      firstIn(fields, "commandSegments") ??
      firstIn(fields, "commands") ??
      readString(fields, "fullCommandText") ??
      readString(fields, "command") ??
      readString(fields, "intention");
    if (headline === null) return { headline: prompt, detail: null };
    return { headline, detail: JSON.stringify(fields, null, 2) };
  } catch {
    return { headline: prompt, detail: null };
  }
}

// A tool card's collapsed one-liner. Tool arguments arrive as a JSON object
// when the CLI sends one; the interesting field is what the operator scans for,
// so a single-valued object prints its value and anything else prints compactly.
export function summarizeToolArgs(args: string): string {
  const trimmed = args.trim();
  if (trimmed === "") return "";
  try {
    const parsed: unknown = JSON.parse(trimmed);
    if (parsed === null || typeof parsed !== "object" || Array.isArray(parsed)) {
      return String(parsed);
    }
    const values = Object.values(parsed as Record<string, unknown>);
    if (values.length === 1) return String(values[0]);
    return Object.entries(parsed as Record<string, unknown>)
      .map(([key, value]) => `${key}=${String(value)}`)
      .join(" ");
  } catch {
    // Not JSON: the first line of whatever it is.
    return trimmed.split("\n")[0] ?? "";
  }
}

type ParsedInstant = { milliseconds: number; nanoseconds: number };

function parseInstant(source: string): ParsedInstant | null {
  const match = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?Z$/.exec(source);
  if (match === null) return null;
  const milliseconds = Date.parse(`${match[1]}Z`);
  if (!Number.isFinite(milliseconds) || new Date(milliseconds).toISOString().slice(0, 19) !== match[1]) return null;
  return {
    milliseconds,
    nanoseconds: Number((match[2] ?? "").padEnd(9, "0")),
  };
}

export function compareInstants(left: string, right: string): number {
  const parsedLeft = parseInstant(left);
  const parsedRight = parseInstant(right);
  if (parsedLeft !== null && parsedRight !== null) {
    return parsedLeft.milliseconds - parsedRight.milliseconds || parsedLeft.nanoseconds - parsedRight.nanoseconds;
  }
  if (parsedLeft !== null) return 1;
  if (parsedRight !== null) return -1;
  return left.localeCompare(right);
}
