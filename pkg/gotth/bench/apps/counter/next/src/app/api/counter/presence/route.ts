import { GLOBAL_ROOM } from '@/lib/session';
import { setExternalTabs } from '@/lib/store';

/*
 * The WebSocket sidecar telling the authority how many browsers it holds.
 *
 * Only the ws variant uses this. The sidecar is a second process in the same
 * container (§5.4), so the process that owns the counter cannot see the
 * sockets the sidecar owns, and F-CTR-5's "N tabs sharing this counter" would
 * read 0 without this call.
 *
 * It is guarded by a shared token rather than left open, because a bench app is
 * still an app: an unauthenticated endpoint that lets any caller rewrite a
 * number the page displays is the kind of thing a reviewer should not have to
 * find. The sidecar and the Next server are started by the same script with
 * the same BENCH_RELAY_TOKEN, and the endpoint is only reachable on the bench
 * project's internal network (§5.3).
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

const TOKEN = process.env.BENCH_RELAY_TOKEN ?? '';

export async function POST(request: Request): Promise<Response> {
  if (TOKEN === '' || request.headers.get('x-bench-relay-token') !== TOKEN) {
    return new Response('forbidden', { status: 403 });
  }

  let body: unknown;
  try {
    body = await request.json();
  } catch {
    return new Response('bad request', { status: 400 });
  }

  const { room, tabs } = (body ?? {}) as { room?: unknown; tabs?: unknown };
  if (typeof tabs !== 'number' || !Number.isFinite(tabs) || tabs < 0) {
    return new Response('bad request', { status: 400 });
  }

  setExternalTabs(typeof room === 'string' && room !== '' ? room : GLOBAL_ROOM, Math.trunc(tabs));
  return new Response(null, { status: 204 });
}
