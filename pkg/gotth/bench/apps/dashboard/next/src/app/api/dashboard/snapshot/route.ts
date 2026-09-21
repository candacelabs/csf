import { identityFrom, newSessionId, sessionCookie } from '@/lib/session';
import { snapshot, touchPoller } from '@/lib/store';

/*
 * The polling variant's endpoint (§5.4's third column), and the WebSocket
 * sidecar's cold-start read.
 *
 * A polling client holds no server-side connection state, which is exactly what
 * makes its memory number incomparable with the other two variants' unless a
 * CPU figure is published beside it (§3.4). It returns the whole view, because
 * a poller has no cursor the server can patch against without holding the very
 * state polling is chosen to avoid — that byte cost is the polling column's
 * real cost and belongs in §4.6.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const key = url.searchParams.get('k') ?? newSessionId();
  const ttlRaw = Number(url.searchParams.get('ttl') ?? 2000);
  const ttl = Number.isFinite(ttlRaw) ? Math.max(1000, ttlRaw) : 2000;
  const who = identityFrom(request);

  touchPoller(key, ttl);

  const headers = new Headers({
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
  });
  if (who.fresh) headers.append('Set-Cookie', sessionCookie(who.sid));

  return new Response(JSON.stringify(snapshot(key)), { headers });
}
