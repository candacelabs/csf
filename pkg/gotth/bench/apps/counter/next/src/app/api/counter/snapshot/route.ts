import { cookies } from 'next/headers';

import { newSessionId, roomForSession, SESSION_COOKIE } from '@/lib/session';
import { snapshot, touchPoller } from '@/lib/store';

/*
 * The polling variant's endpoint (§5.4's third column), and the WebSocket
 * sidecar's cold-start read.
 *
 * A polling client holds no server-side connection state, which is exactly
 * what makes its memory number incomparable with the other two variants'
 * unless a CPU figure is published beside it (§3.4). What it does hold is a
 * TTL entry, so the page can still say "3 tabs sharing this counter" — the
 * feature (F-CTR-5) survives the transport change, which is the point of
 * measuring three transports of one app rather than three apps.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const tab = url.searchParams.get('tab');
  const ttl = Number(url.searchParams.get('ttl') ?? 2000);

  const jar = await cookies();
  let sid = jar.get(SESSION_COOKIE)?.value;
  const freshCookie = sid === undefined;
  if (freshCookie) sid = newSessionId();
  const room = roomForSession(sid);

  if (tab) touchPoller(room, tab, Number.isFinite(ttl) ? Math.max(1000, ttl) : 2000);

  const headers = new Headers({
    'Content-Type': 'application/json; charset=utf-8',
    'Cache-Control': 'no-store',
  });
  if (freshCookie && sid) {
    headers.append(
      'Set-Cookie',
      `${SESSION_COOKIE}=${sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400`,
    );
  }

  return new Response(JSON.stringify(snapshot(room)), { headers });
}
