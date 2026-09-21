/*
 * The chat app's shared vocabulary and its pure derivations (§2.3).
 *
 * Everything a message renders is computed here, from server numbers only, for
 * the same reason the counter's core.ts is written that way: E1/E3 are checked
 * by comparing rendered DOM between the two stacks, so every visible string is
 * part of the equivalence claim and none of it may depend on the browser's
 * clock or locale.
 */

/** §2.3: "three fixed rooms (`alpha`, `beta`, `gamma`)". */
export const ROOMS = ['alpha', 'beta', 'gamma'] as const;
export type RoomId = (typeof ROOMS)[number];

export function isRoom(v: unknown): v is RoomId {
  return typeof v === 'string' && (ROOMS as readonly string[]).includes(v);
}

/**
 * F-CHT-1: "hard cap 200 rendered messages (oldest dropped). No virtualization
 * on either side — forbidden, so DOM size is identical."
 *
 * The cap is applied on the SERVER, so the client never holds more than it
 * renders and the two stacks' DOM node counts are equal by construction rather
 * than by two clients agreeing to trim the same amount.
 */
export const MESSAGE_CAP = 200;

/** F-CHT-4: "body length 1..500". */
export const BODY_MIN = 1;
export const BODY_MAX = 500;

/**
 * F-CHT-6's decay window. A name stops counting as "typing" 3 s after its last
 * keystroke signal, on the server, so every viewer agrees on the count.
 */
export const TYPING_DECAY_MS = 3_000;

/**
 * The designated read-only participant (F-CHT-9).
 *
 * Identity is a cookie, not a login: the harness sets it before CHT-8 and the
 * server refuses that name's sends. Keeping the rejection server-side is the
 * point of the feature — a disabled button would prove nothing, because the
 * thing under test is that the server says no.
 */
export const READONLY_NAME = 'readonly';

export type MessageState = 'confirmed' | 'pending';

export interface Message {
  /** Monotonic per room. Also the React key and the DOM's data-bench-seq. */
  seq: number;
  author: string;
  body: string;
  atMs: number;
  /**
   * Echoed back from the client's send so an optimistic entry can be replaced
   * by its confirmation rather than rendered twice (CHT-2b -> CHT-2).
   * Empty for fixture peers.
   */
  clientId: string;
}

/** One session's view of one room. Everything the page renders comes from here. */
export interface RoomView {
  room: RoomId;
  messages: Message[];
  /** F-CHT-5 — participants currently in the room. */
  presence: string[];
  /** F-CHT-6 — names typing right now, already decayed by the server. */
  typing: string[];
  /** F-CHT-7 — per-room unread counts for THIS session. */
  unread: Record<RoomId, number>;
  /** Monotonic; a view older than the one held is dropped by the client. */
  version: number;
  /** Server clock at snapshot time, so relative timestamps use server numbers. */
  nowMs: number;
  /** This session's identity, for "you" marks and for the read-only case. */
  me: string;
  readonly: boolean;
}

export function emptyView(room: RoomId, me: string): RoomView {
  return {
    room,
    messages: [],
    presence: [],
    typing: [],
    unread: { alpha: 0, beta: 0, gamma: 0 },
    version: 0,
    nowMs: 0,
    me,
    readonly: me === READONLY_NAME,
  };
}

/** F-CHT-2 — the avatar initial. A CSS circle, no image (§2.3, and §2's no-images bound). */
export function initial(author: string): string {
  return author ? author.slice(0, 1).toUpperCase() : '?';
}

/**
 * F-CHT-2 — the absolute timestamp.
 *
 * Formatted from the server's epoch milliseconds with explicit arithmetic
 * rather than toLocaleTimeString, because the container's locale and timezone
 * are not part of the equivalence contract and Go's side formats UTC HH:MM:SS.
 */
export function clockLabel(atMs: number): string {
  const total = Math.floor(atMs / 1000);
  const h = Math.floor(total / 3600) % 24;
  const m = Math.floor(total / 60) % 60;
  const s = total % 60;
  return `${pad(h)}:${pad(m)}:${pad(s)}`;
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/** F-CHT-2 — the relative timestamp, from two server numbers. */
export function ageLabel(atMs: number, nowMs: number): string {
  const ms = Math.max(0, nowMs - atMs);
  if (ms < 2000) return 'just now';
  if (ms < 60_000) return `${Math.trunc(ms / 1000)}s ago`;
  if (ms < 3_600_000) return `${Math.trunc(ms / 60_000)}m ago`;
  return `${Math.trunc(ms / 3_600_000)}h ago`;
}

/** F-CHT-6 — "N people are typing", or nothing at all. */
export function typingLabel(names: string[]): string {
  if (names.length === 0) return '';
  if (names.length === 1) return `${names[0]} is typing`;
  return `${names.length} people are typing`;
}

/**
 * F-CHT-4's validation, as a pure function.
 *
 * It runs on the server (that is what the feature asks for) and the client does
 * NOT pre-empt it: a client-side length guard would make CHT-5 measure a local
 * paint on this stack and a round trip on the other, which is a category error
 * of exactly the kind §2.2 exists to keep out of the tables.
 */
export function validateBody(body: string): string {
  if (body.length < BODY_MIN) return 'Say something first.';
  if (body.length > BODY_MAX) return `Too long by ${body.length - BODY_MAX} characters (max ${BODY_MAX}).`;
  return '';
}
