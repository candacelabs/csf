import { cookies } from 'next/headers';

import { roomForSession, SESSION_COOKIE, newSessionId } from '@/lib/session';
import { snapshot, subscribe, subscribeRelay } from '@/lib/store';
import type { Snapshot } from '@/lib/core';

/*
 * The SSE channel — §5.4's primary live-data variant: "SSE via a streaming
 * Route Handler (ReadableStream in a GET route handler), consumed with SWR
 * (useSWRSubscription). Fully inside Next.js, no extra process, no custom
 * server."
 *
 * `force-dynamic` and the nodejs runtime are both required rather than chosen:
 * a cached or edge-rendered stream is not a stream, and the store this reads
 * lives in the Node process.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

/**
 * Heartbeat interval.
 *
 * A stream that sends nothing for minutes is a stream some proxy will close,
 * and an SSE comment is the cheapest keep-alive there is (2 bytes plus the
 * frame). 15 s is well inside any default idle timeout the shared bench proxy
 * (§3.6) is likely to carry. These bytes are real and are counted in §4.6's
 * wire-byte accounting exactly like gotth-live's heartbeats are.
 */
const HEARTBEAT_MS = 15_000;

const encoder = new TextEncoder();

function frame(snapshot: Snapshot): Uint8Array {
  return encoder.encode(`data: ${JSON.stringify(snapshot)}\n\n`);
}

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const tab = url.searchParams.get('tab') ?? newSessionId();

  /*
   * The WebSocket sidecar consumes this same stream as its upstream (see
   * ws-server/relay.mjs). It passes relay=1 so it is not counted as a tab:
   * the browsers it is holding are reported separately through
   * /api/counter/presence, and double-counting them would make the ws variant
   * claim one more session than exists.
   */
  const isRelay = url.searchParams.get('relay') === '1';

  const jar = await cookies();
  let sid = jar.get(SESSION_COOKIE)?.value;
  const freshCookie = sid === undefined;
  if (freshCookie) sid = newSessionId();
  const room = roomForSession(sid);

  let cleanup: (() => void) | null = null;

  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      let closed = false;

      const send = (bytes: Uint8Array) => {
        if (closed) return;
        try {
          controller.enqueue(bytes);
        } catch {
          // The consumer went away between the abort signal and this write.
          closed = true;
        }
      };

      // The first frame is the current state, so a client that connects
      // between two changes is correct immediately rather than at the next
      // one. It is also what §3.3's "first message applied" refers to.
      send(frame(snapshot(room)));

      const push = (next: Snapshot) => send(frame(next));
      const unsubscribe = isRelay
        ? subscribeRelay(room, push)
        : subscribe(room, tab, push);

      const heartbeat = setInterval(() => send(encoder.encode(': hb\n\n')), HEARTBEAT_MS);

      cleanup = () => {
        if (closed) return;
        closed = true;
        clearInterval(heartbeat);
        unsubscribe();
        try {
          controller.close();
        } catch {
          // Already closed by the runtime; nothing to do.
        }
      };

      request.signal.addEventListener('abort', () => cleanup?.());
    },
    cancel() {
      cleanup?.();
    },
  });

  const headers = new Headers({
    'Content-Type': 'text/event-stream; charset=utf-8',
    // no-transform matters: a compressing proxy that buffers the stream would
    // turn a push measurement into a buffering measurement.
    'Cache-Control': 'no-cache, no-store, no-transform',
    Connection: 'keep-alive',
    'X-Accel-Buffering': 'no',
  });
  if (freshCookie && sid) {
    headers.append(
      'Set-Cookie',
      `${SESSION_COOKIE}=${sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400`,
    );
  }

  return new Response(stream, { headers });
}
