import DashboardLive from '@/components/DashboardLive';
import { identity, newSessionId } from '@/lib/session';
import { snapshot } from '@/lib/store';

/*
 * The measured route (§2.4). This is the demanding app: the headline
 * push-latency number, the heavy memory workload and the wire-byte comparison
 * all come from here.
 *
 * `force-dynamic` is §5.5's fairness constraint made explicit: the equivalent
 * gotth-live route renders current session state and cannot be served from a
 * cache, so this one may not be either. It is a constraint on the COMPARISON,
 * not a claim that Next.js cannot cache — the cached variant is a separate
 * measured row (§4.5, AS-6) and it is not this file.
 *
 * This is a Server Component (AS-7). It renders the static shell AND the first
 * paint of all five regions — 200 table rows, 8 sparklines, 240 chart points —
 * so the document that arrives is already the dashboard rather than a skeleton.
 * That is the same property the gotth-live page has, and it is why D5's TTI is
 * about becoming interactive rather than about becoming legible.
 */
export const dynamic = 'force-dynamic';

export default async function DashboardPage() {
  await identity();

  /*
   * The session key is minted HERE, on the server, and once per page load — one
   * tab is one page load, which is the same lifetime as a gotth-live session
   * (§3.4). Minting it in the browser would produce a hydration mismatch, a bug
   * class the competing stack structurally does not have (§8.2 G-8) and
   * therefore not one to hand it for free.
   */
  const sessionKey = newSessionId();
  const initial = snapshot(sessionKey);

  return (
    <main>
      <h1>Next.js live dashboard</h1>
      <p className="lede">
        Five regions driven by one committed fixture at 10 Hz, six
        server-authoritative controls, and one push channel. Nothing in this
        browser polls anything and nothing in it filters anything.
      </p>

      <DashboardLive initial={initial} sessionKey={sessionKey} />

      <p className="status">
        connection: <span className="dot"></span>
      </p>
    </main>
  );
}
