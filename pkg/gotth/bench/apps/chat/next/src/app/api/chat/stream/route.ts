import { identityFrom, newSessionId, roomOr, sessionCookie } from '@/lib/session';
import { subscribe } from '@/lib/store';
import type { RoomView } from '@/lib/core';

/*
 * The SSE channel — §5.4's primary live-data variant.
 *
 * `force-dynamic` and the nodejs runtime are required rather than chosen: a
 * cached or edge-rendered stream is not a stream, and the authority this reads
 * lives in the Node process.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

/**
 * Heartbeat interval. A stream that sends nothing for minutes is a stream some
 * proxy will close, and an SSE comment is the cheapest keep-alive there is.
 * These bytes are real and are counted in §4.6's wire-byte accounting exactly
 * like gotth-live's heartbeats are.
 */
const HEARTBEAT_MS = 15_000;

const encoder = new TextEncoder();

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const key = url.searchParams.get('k') ?? newSessionId();
  const room = roomOr(url.searchParams.get('room') ?? undefined);
  const who = identityFrom(request);

  let cleanup: (() => void) | null = null;

  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      let closed = false;

      const send = (bytes: Uint8Array) => {
        if (closed) return;
        try {
          controller.enqueue(bytes);
        } catch {
          closed = true;
        }
      };

      const push = (view: RoomView) => send(encoder.encode(`data: ${JSON.stringify(view)}\n\n`));

      /*
       * subscribe() broadcasts on attach, so the first frame is this session's
       * current view — a client that connects between two ticks is correct
       * immediately rather than at the next one, and that first frame is what
       * §3.3's "first message applied" refers to.
       */
      const unsubscribe = subscribe(key, who.me, room, push);

      const heartbeat = setInterval(() => send(encoder.encode(': hb\n\n')), HEARTBEAT_MS);

      cleanup = () => {
        if (closed) return;
        closed = true;
        clearInterval(heartbeat);
        unsubscribe();
        try {
          controller.close();
        } catch {
          // Already closed by the runtime.
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
  if (who.fresh) headers.append('Set-Cookie', sessionCookie(who.sid));

  return new Response(stream, { headers });
}
