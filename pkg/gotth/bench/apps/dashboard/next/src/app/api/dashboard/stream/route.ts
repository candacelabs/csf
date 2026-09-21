import { identityFrom, newSessionId, sessionCookie } from '@/lib/session';
import { subscribe } from '@/lib/store';
import type { Patch } from '@/lib/core';

/*
 * The SSE channel — §5.4's primary live-data variant.
 *
 * `force-dynamic` and the nodejs runtime are required rather than chosen: a
 * cached or edge-rendered stream is not a stream, and the authority this reads
 * lives in the Node process.
 *
 * This is the highest-rate stream in the bench: §2.4's regions produce a patch
 * on every 2nd tick (region D at 5 Hz) and a large one on every 10th (regions A
 * and C at 1 Hz). What goes down the wire is a patch, not a view — see
 * lib/core.ts's Patch for why sending the whole 14 KB view twice a second would
 * make §4.6 a measurement of an author's choice.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

const HEARTBEAT_MS = 15_000;

const encoder = new TextEncoder();

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const key = url.searchParams.get('k') ?? newSessionId();
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

      const push = (patch: Patch) => send(encoder.encode(`data: ${JSON.stringify(patch)}\n\n`));

      /* subscribe() sends the whole view as its first frame, which is §3.3's
         "first message applied". */
      const unsubscribe = subscribe(key, push);

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
