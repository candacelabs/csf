import { identityFrom } from '@/lib/session';
import { setTyping } from '@/lib/store';

/*
 * F-CHT-6's typing heartbeat, and the one place this app uses a Route Handler
 * where §5.4's table says "Mutations ... Server Actions".
 *
 * -----------------------------------------------------------------------------
 * The deviation, and why it is the pro-Next.js choice
 *
 * React serialises Server Actions: a second action dispatched while the first
 * is in flight waits for it. A typing heartbeat as a Server Action would
 * therefore sit in the same queue as the user's Send, and CHT-2 — the headline
 * chat latency — would be measuring how long a keystroke heartbeat took to
 * drain, not how long a send took. That is not Next.js losing; it is the
 * harness measuring the wrong thing.
 *
 * A fire-and-forget POST with keepalive is what a competent team ships for a
 * presence ping, it is what the Next docs' own guidance implies for a signal
 * that is not a data mutation the UI depends on, and it keeps CHT-1's
 * "MUST NOT round-trip on either side" honest: the composer's own value is
 * local state and this request is not on its paint path.
 *
 * Listed in bench/audit/nextjs-pessimization-checklist.md under "every
 * deviation from the Next.js docs' recommended pattern is listed with a reason"
 * (§5.4), so the audit rules on it rather than discovering it.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export async function POST(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const key = url.searchParams.get('k');
  if (!key) return new Response('bad request', { status: 400 });
  setTyping(key, identityFrom(request).me);
  return new Response(null, { status: 204 });
}
