import { identityFrom, newSessionId, roomOr, sessionCookie } from '@/lib/session';
import { snapshot, touchPoller } from '@/lib/store';

/*
 * The polling variant's endpoint (§5.4's third column), and the WebSocket
 * sidecar's cold-start read.
 *
 * A polling client holds no server-side connection state, which is exactly what
 * makes its memory number incomparable with the other two variants' unless a
 * CPU figure is published beside it (§3.4). What it does hold is a TTL entry, so
 * F-CHT-5's presence list still shows it — the feature survives the transport
 * change, which is the point of measuring three transports of one app rather
 * than three apps.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const key = url.searchParams.get('k') ?? newSessionId();
  const room = roomOr(url.searchParams.get('room') ?? undefined);
  const ttlRaw = Number(url.searchParams.get('ttl') ?? 2000);
  const ttl = Number.isFinite(ttlRaw) ? Math.max(1000, ttlRaw) : 2000;
  const who = identityFrom(request);

  touchPoller(key, who.me, room, ttl);

  const headers = new Headers({
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
  });
  if (who.fresh) headers.append('Set-Cookie', sessionCookie(who.sid));

  return new Response(JSON.stringify(snapshot(key, who.me, room)), { headers });
}
