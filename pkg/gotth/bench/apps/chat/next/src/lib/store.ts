import 'server-only';

import {
  MESSAGE_CAP,
  READONLY_NAME,
  ROOMS,
  TYPING_DECAY_MS,
  emptyView,
  isRoom,
  validateBody,
  type Message,
  type RoomId,
  type RoomView,
} from './core';
import { Replay, TICK_MS, load, type FixtureTick } from './fixture';

/*
 * The chat server's authority: rooms, messages, presence, typing, per-session
 * unread, and the fixture replay that drives all of it.
 *
 * Keyed off a global Symbol for the same reason the counter's store is: Next
 * bundles route handlers and Server Actions into separate server chunks, so a
 * module-level singleton can be instantiated more than once and the bug looks
 * like "the push channel is broken". This is the recipe the Next docs use for a
 * database client and it is not a benchmark-specific trick.
 */

type ChatEvent =
  | { k: 'msg'; room: RoomId; author: string; body: string }
  | { k: 'typing'; room: RoomId; author: string }
  | { k: 'join'; room: RoomId; author: string }
  | { k: 'leave'; room: RoomId; author: string };

interface FixtureBase {
  presence: string[];
  rooms: string[];
}

interface Room {
  messages: Message[];
  seq: number;
  presence: Set<string>;
  /** name -> the epoch ms at which the name stops counting as typing. */
  typing: Map<string, number>;
}

/** One subscriber: a browser tab holding this app's push channel (§3.4). */
interface Session {
  id: string;
  me: string;
  room: RoomId;
  unread: Record<RoomId, number>;
  listener: ((view: RoomView) => void) | null;
  /** Polling clients hold no connection; they hold a TTL instead. */
  pollExpiry: number;
  createdAtMs: number;
}

/**
 * How long a session with no push channel and no live poll TTL is kept.
 *
 * It matters more than it looks. D4 (§3.7) requests the main document with a
 * FRESH session cookie per request, at the highest sustained rate the stack can
 * serve — so every throughput probe mints a session that never subscribes. With
 * no eviction that is an unbounded map, the RPS ceiling would be measuring a
 * leak, and the D3 memory numbers taken after a throughput run would be
 * measuring its residue. Thirty seconds is far longer than the gap between a
 * document render and the stream it opens, and far shorter than any measurement
 * window.
 */
const SESSION_GRACE_MS = 30_000;

interface Shape {
  rooms: Map<RoomId, Room>;
  sessions: Map<string, Session>;
  version: number;
  replay: Replay<ChatEvent> | null;
  sha256: string;
}

const KEY = Symbol.for('gotth-live-bench.chat.store.v1');

function shape(): Shape {
  const g = globalThis as unknown as Record<symbol, Shape | undefined>;
  let s = g[KEY];
  if (s) return s;

  const fixture = load<FixtureBase, ChatEvent>('chat');

  s = {
    rooms: new Map(),
    sessions: new Map(),
    version: 0,
    replay: null,
    sha256: fixture.sha256,
  };
  for (const id of ROOMS) {
    s.rooms.set(id, {
      messages: [],
      seq: 0,
      presence: new Set(fixture.base.presence),
      typing: new Map(),
    });
  }
  g[KEY] = s;

  s.replay = new Replay<ChatEvent>(fixture.ticks, (tick) => applyTick(s!, tick), TICK_MS);
  return s;
}

/* ------------------------------------------------------------- replay ---- */

function applyTick(s: Shape, tick: FixtureTick<ChatEvent>): void {
  const dirty = new Set<RoomId>();
  const now = Date.now();

  for (const event of tick.e) {
    if (!isRoom(event.room)) continue;
    const room = s.rooms.get(event.room)!;
    switch (event.k) {
      case 'msg':
        append(room, { author: event.author, body: event.body, atMs: now, clientId: '' });
        room.presence.add(event.author);
        room.typing.delete(event.author);
        dirty.add(event.room);
        break;
      case 'typing':
        room.typing.set(event.author, now + TYPING_DECAY_MS);
        dirty.add(event.room);
        break;
      case 'join':
        room.presence.add(event.author);
        dirty.add(event.room);
        break;
      case 'leave':
        room.presence.delete(event.author);
        room.typing.delete(event.author);
        dirty.add(event.room);
        break;
    }
  }

  /*
   * F-CHT-6's 3 s decay, swept here rather than on a timer of its own. A name
   * whose window has closed changes what every viewer of that room renders, so
   * the room is dirty and gets a push — otherwise "2 people are typing" would
   * stay on screen until the next unrelated event, which is the visible bug the
   * decay exists to prevent.
   */
  for (const [id, room] of s.rooms) {
    let expired = false;
    for (const [name, until] of room.typing) {
      if (until <= now) {
        room.typing.delete(name);
        expired = true;
      }
    }
    if (expired) dirty.add(id);
  }

  /* Once a second, drop sessions that hold neither a stream nor a live poll. */
  if (tick.n % 10 === 0) sweepSessions(s, now);

  if (dirty.size > 0) broadcast(s, dirty);
}

function sweepSessions(s: Shape, now: number): void {
  for (const [id, session] of s.sessions) {
    if (session.listener !== null) continue;
    if (session.pollExpiry > now) continue;
    if (now - Math.max(session.createdAtMs, session.pollExpiry) < SESSION_GRACE_MS) continue;
    s.sessions.delete(id);
  }
}

function append(room: Room, m: Omit<Message, 'seq'>): Message {
  room.seq += 1;
  const full: Message = { seq: room.seq, ...m };
  room.messages.push(full);
  /* F-CHT-1's hard cap, applied server-side so both stacks render 200 nodes. */
  if (room.messages.length > MESSAGE_CAP) {
    room.messages.splice(0, room.messages.length - MESSAGE_CAP);
  }
  return full;
}

/* ------------------------------------------------------------- views ----- */

function view(s: Shape, session: Session): RoomView {
  const now = Date.now();
  const room = s.rooms.get(session.room)!;
  const typing: string[] = [];
  for (const [name, until] of room.typing) {
    /* A session never counts itself as typing; nobody needs telling. */
    if (until > now && name !== session.me) typing.push(name);
  }
  typing.sort();

  return {
    room: session.room,
    messages: room.messages,
    presence: [...room.presence, session.me].sort(),
    typing,
    unread: { ...session.unread },
    version: s.version,
    nowMs: now,
    me: session.me,
    readonly: session.me === READONLY_NAME,
  };
}

/**
 * Push to every session a set of dirty rooms affects.
 *
 * A message in room X repaints X's viewers (new message) AND everybody else
 * (F-CHT-7's unread badge), so the unread bump happens here, once, rather than
 * at each call site.
 */
function broadcast(s: Shape, dirty: Set<RoomId>): void {
  s.version += 1;

  for (const session of s.sessions.values()) {
    let touched = dirty.has(session.room);
    for (const id of dirty) {
      if (id === session.room) continue;
      if (lastMessageIsNew(s.rooms.get(id)!, session, id)) {
        session.unread[id] += 1;
        touched = true;
      }
    }
    if (touched && session.listener) session.listener(view(s, session));
  }
}

/*
 * Unread bookkeeping needs a per-session high-water mark or every dirty tick
 * would increment the badge, including a typing event. The mark is the seq of
 * the newest message the session has been told about in that room.
 */
const seen = new WeakMap<Session, Record<RoomId, number>>();

function marks(session: Session): Record<RoomId, number> {
  let m = seen.get(session);
  if (!m) {
    m = { alpha: 0, beta: 0, gamma: 0 };
    seen.set(session, m);
  }
  return m;
}

function lastMessageIsNew(room: Room, session: Session, id: RoomId): boolean {
  const m = marks(session);
  if (room.seq <= m[id]) return false;
  m[id] = room.seq;
  return true;
}

/* ---------------------------------------------------------- lifecycle ---- */

export function ensureSession(id: string, me: string, room: RoomId): Session {
  const s = shape();
  let session = s.sessions.get(id);
  if (!session) {
    session = {
      id,
      me,
      room,
      unread: { alpha: 0, beta: 0, gamma: 0 },
      listener: null,
      pollExpiry: 0,
      createdAtMs: Date.now(),
    };
    s.sessions.set(id, session);
    /* The high-water marks start at "already seen", so joining a room mid-hour
       does not show a badge for messages that arrived before the session did. */
    const m = marks(session);
    for (const r of ROOMS) m[r] = s.rooms.get(r)!.seq;
  }
  session.me = me;
  return session;
}

export function snapshot(id: string, me: string, room: RoomId): RoomView {
  const s = shape();
  if (!id) return emptyView(room, me);
  return view(s, ensureSession(id, me, room));
}

export function subscribe(
  id: string,
  me: string,
  room: RoomId,
  listener: (view: RoomView) => void,
): () => void {
  const s = shape();
  const session = ensureSession(id, me, room);
  session.listener = listener;
  s.rooms.get(session.room)!.presence.add(me);
  broadcast(s, new Set([session.room]));
  return () => {
    if (session.listener === listener) session.listener = null;
    s.sessions.delete(id);
    for (const r of s.rooms.values()) r.presence.delete(me);
    broadcast(s, new Set(ROOMS));
  };
}

/** A polling client checking in; `ttlMs` should be ~2x the poll interval. */
export function touchPoller(id: string, me: string, room: RoomId, ttlMs: number): void {
  const session = ensureSession(id, me, room);
  session.pollExpiry = Date.now() + ttlMs;
}

/* ------------------------------------------------------------ mutations -- */

export interface SendResult {
  error: string;
  message: Message | null;
}

/**
 * F-CHT-4 and F-CHT-9, both server-side.
 *
 * The read-only refusal comes first, because F-CHT-9 is about authorization
 * and not about the body: a read-only participant sending a valid message must
 * still be refused, and the error a reader sees must say why.
 */
export function send(
  id: string,
  me: string,
  room: RoomId,
  body: string,
  clientId: string,
): SendResult {
  const s = shape();
  const session = ensureSession(id, me, room);

  if (me === READONLY_NAME) {
    return { error: 'You are a read-only participant in this room.', message: null };
  }

  const invalid = validateBody(body);
  if (invalid) return { error: invalid, message: null };

  const r = s.rooms.get(room)!;
  const message = append(r, { author: me, body, atMs: Date.now(), clientId });
  r.typing.delete(me);
  const m = marks(session);
  m[room] = r.seq;
  broadcast(s, new Set([room]));
  return { error: '', message };
}

/** F-CHT-7 — switching room clears that room's badge for this session. */
export function switchRoom(id: string, me: string, room: RoomId): void {
  const s = shape();
  const session = ensureSession(id, me, room);
  const previous = session.room;
  session.room = room;
  session.unread[room] = 0;
  marks(session)[room] = s.rooms.get(room)!.seq;
  s.rooms.get(previous)!.presence.delete(me);
  s.rooms.get(room)!.presence.add(me);
  broadcast(s, new Set([previous, room]));
}

/** F-CHT-6 — this session says it is typing. Decays on its own after 3 s. */
export function setTyping(id: string, me: string): void {
  const s = shape();
  const session = s.sessions.get(id);
  if (!session) return;
  const room = s.rooms.get(session.room)!;
  room.typing.set(me, Date.now() + TYPING_DECAY_MS);
  broadcast(s, new Set([session.room]));
}

/* ------------------------------------------------------------- bench ----- */

/** §3.2's control channel: T0 and the replay position, for the skew estimate. */
export function clock(): { t0Ms: number; nowMs: number; tick: number; tickMs: number } {
  const s = shape();
  return {
    t0Ms: s.replay?.t0Ms ?? 0,
    nowMs: Date.now(),
    tick: s.replay?.tickNow() ?? 0,
    tickMs: TICK_MS,
  };
}

/** The fixture SHA-256 the run manifest records (§6). */
export function fixtureSha(): string {
  return shape().sha256;
}
