'use client';

import { useCallback, useEffect, useOptimistic, useRef, useState, useTransition } from 'react';

import { useChatLive } from '@transport';
import { selectRoom, sendMessage } from '@/app/chat/[room]/actions';
import {
  ROOMS,
  ageLabel,
  clockLabel,
  initial as avatarInitial,
  typingLabel,
  type Message,
  type RoomId,
  type RoomView,
} from '@/lib/core';

/*
 * The whole interactive surface of the chat room, and the only 'use client' on
 * the measured route.
 *
 * Why one boundary and not five: the message list, the composer, the presence
 * list, the typing indicator and the room switcher are five views of ONE
 * subscription. Splitting them into separate client components would give each
 * its own EventSource/WebSocket — five connections per tab where gotth-live
 * opens one — and D3 would then charge Next.js for an architecture no competent
 * team would ship. The static shell stays in the Server Component that renders
 * this one (§5.4: client boundaries as deep as the interactivity requires, and
 * no deeper than it permits).
 */

export interface ChatLiveProps {
  initial: RoomView;
  sessionKey: string;
}

/** A send that has not been confirmed by the server yet (CHT-2b, AS-2). */
interface Pending {
  clientId: string;
  body: string;
  author: string;
  atMs: number;
}

/**
 * F-CHT-6's outbound heartbeat interval.
 *
 * A signal per keystroke would be 8 requests a second from one composer for a
 * feature whose decay window is 3 s. One per second is the coarsest rate that
 * keeps "N people are typing" continuously true while somebody types, which is
 * what the feature promises.
 */
const TYPING_PING_MS = 1000;

export default function ChatLive({ initial, sessionKey }: ChatLiveProps) {
  const { view, status, received } = useChatLive(sessionKey, initial);
  const [draft, setDraft] = useState('');
  const [error, setError] = useState('');
  const [, startTransition] = useTransition();
  const readySignalled = useRef(false);
  const lastPing = useRef(0);
  const composer = useRef<HTMLTextAreaElement | null>(null);

  /*
   * CHT-2b — optimistic send (AS-2, §5.4's "Optimistic feedback | useOptimistic
   * for chat send"). The pending entry renders with data-bench-state="pending";
   * the server's confirmation arrives on the push channel with
   * data-bench-state="confirmed" and the same clientId, and the dedupe below
   * keeps the list from showing both.
   */
  const [pending, addPending] = useOptimistic<Pending[], Pending>([], (current, next) => [
    ...current,
    next,
  ]);

  /* The connection indicator, written onto <html> so the one stylesheet can
     select on it — the same mechanism the gotth-live runtime uses. */
  useEffect(() => {
    document.documentElement.setAttribute('data-bench-status', status);
  }, [status]);

  /* §3.3 — hydration complete for the interactive region (this effect running
     is that), channel open, first message applied. Set exactly once. */
  useEffect(() => {
    if (readySignalled.current) return;
    if (status !== 'live' || received === 0) return;
    readySignalled.current = true;
    const bench = (window as unknown as { __bench?: { ready: boolean } }).__bench;
    if (bench) bench.ready = true;
  }, [status, received]);

  /*
   * CHT-1 — "type one character into the composer | composer value updated |
   * MUST NOT round-trip on either side".
   *
   * The value is local state and paints in the same frame. The typing ping is a
   * throttled fire-and-forget POST that is not on this paint's path; see
   * api/chat/typing/route.ts for why it is a Route Handler and not a Server
   * Action.
   */
  const onDraftChange = useCallback(
    (value: string) => {
      setDraft(value);
      const now = Date.now();
      if (now - lastPing.current < TYPING_PING_MS) return;
      lastPing.current = now;
      void fetch(`/api/chat/typing?k=${encodeURIComponent(sessionKey)}`, {
        method: 'POST',
        keepalive: true,
      }).catch(() => {
        /* Presence is best-effort by construction; a dropped ping decays. */
      });
    },
    [sessionKey],
  );

  const submit = useCallback(() => {
    const body = draft;
    if (body === '') {
      setError('Say something first.');
      return;
    }
    const clientId = `${sessionKey}:${Date.now()}:${Math.random().toString(36).slice(2, 8)}`;
    startTransition(async () => {
      addPending({ clientId, body, author: view.me, atMs: Date.now() });
      const verdict = await sendMessage(sessionKey, view.room, body, clientId);
      setError(verdict.error);
      /*
       * F-CHT-4: "violation renders an inline error next to the composer
       * WITHOUT clearing the input". So the draft survives a rejection, and
       * only an accepted send empties the box.
       */
      if (!verdict.error) setDraft('');
    });
  }, [draft, sessionKey, view.me, view.room, addPending]);

  /* F-CHT-3 — Enter sends, Shift+Enter newlines. */
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent<HTMLTextAreaElement>) => {
      if (event.key !== 'Enter' || event.shiftKey) return;
      event.preventDefault();
      submit();
    },
    [submit],
  );

  const onSelectRoom = useCallback(
    (room: RoomId) => {
      startTransition(async () => {
        await selectRoom(sessionKey, room);
      });
    },
    [sessionKey],
  );

  const confirmed = new Set(view.messages.map((m) => m.clientId).filter(Boolean));
  const stillPending = pending.filter((p) => !confirmed.has(p.clientId));

  return (
    <>
      <section className="card rooms" data-bench-region="D">
        <ul className="room-list" data-bench-id="rooms">
          {ROOMS.map((room) => (
            <li key={room}>
              <button
                type="button"
                className={room === view.room ? 'room current' : 'room'}
                data-bench-id={`room-${room}`}
                aria-current={room === view.room ? 'true' : undefined}
                onClick={() => onSelectRoom(room)}
              >
                {room}
                <span className="badge" data-bench-id={`unread-${room}`}>
                  {view.unread[room]}
                </span>
              </button>
            </li>
          ))}
        </ul>
        <h2 className="room-title" data-bench-id="room-title" data-bench-value={view.room}>
          #{view.room}
        </h2>
      </section>

      <section className="card roster" data-bench-region="C">
        <h2>In the room</h2>
        <ul className="members" data-bench-id="presence">
          {view.presence.map((name) => (
            <li key={name} className={name === view.me ? 'member you' : 'member'}>
              {name}
            </li>
          ))}
        </ul>
        <p className="typing" data-bench-id="typing" data-bench-value={view.typing.length} role="status">
          {typingLabel(view.typing)}
        </p>
      </section>

      <section className="card log" data-bench-region="A">
        <h2>
          Room log <span className="count" data-bench-id="count">{view.messages.length}</span>
        </h2>
        <ol className="messages" data-bench-id="messages">
          {view.messages.map((m) => (
            <MessageRow key={m.seq} m={m} nowMs={view.nowMs} me={view.me} state="confirmed" />
          ))}
          {stillPending.map((p) => (
            <MessageRow
              key={p.clientId}
              m={{ seq: 0, author: p.author, body: p.body, atMs: p.atMs, clientId: p.clientId }}
              nowMs={view.nowMs}
              me={view.me}
              state="pending"
            />
          ))}
        </ol>
      </section>

      <section className="card composer" data-bench-region="B">
        <label htmlFor="chat-body">
          Say something as {view.me}
          {view.readonly ? ' (read-only)' : ''}
        </label>
        <textarea
          id="chat-body"
          ref={composer}
          data-bench-id="composer"
          rows={3}
          value={draft}
          placeholder="type here — Enter sends, Shift+Enter is a newline"
          autoComplete="off"
          aria-invalid={error !== '' ? 'true' : undefined}
          aria-describedby="chat-body-help"
          onChange={(e) => onDraftChange(e.target.value)}
          onKeyDown={onKeyDown}
        />
        <div className="composer-actions">
          <button type="button" data-bench-id="send" onClick={submit}>
            Send
          </button>
          <span className="hint" id="chat-body-help">
            {500 - draft.length} characters left
          </span>
        </div>
        {error !== '' ? (
          <p className="error" data-bench-id="error" data-bench-value={error} role="alert">
            {error}
          </p>
        ) : null}
      </section>
    </>
  );
}

/*
 * F-CHT-2 — author name, avatar initial (CSS circle, no image), body, absolute
 * timestamp, relative timestamp. Six elements per message including the <li>,
 * so 200 messages is 1200 nodes and §2.3's "≤ 200 message nodes x ≤ 8 elements"
 * bound holds with room to spare.
 *
 * data-bench-value carries the BODY on the <li>, because CHT-2's predicate is
 * "last message node's data-bench-value === sent body" (§2.3) rather than
 * §2.0's textContent form. Both readings are satisfied: the attribute holds the
 * body and the body span's textContent is the same string.
 */
function MessageRow({
  m,
  nowMs,
  me,
  state,
}: {
  m: Message;
  nowMs: number;
  me: string;
  state: 'confirmed' | 'pending';
}) {
  return (
    <li
      className={m.author === me ? 'message mine' : 'message'}
      data-bench-id="message"
      data-bench-value={m.body}
      data-bench-state={state}
      data-bench-seq={m.seq}
    >
      <span className="avatar" aria-hidden="true">
        {avatarInitial(m.author)}
      </span>
      <span className="author">{m.author}</span>
      <span className="body">{m.body}</span>
      <span className="at">{clockLabel(m.atMs)}</span>
      <span className="ago">{ageLabel(m.atMs, nowMs)}</span>
    </li>
  );
}
