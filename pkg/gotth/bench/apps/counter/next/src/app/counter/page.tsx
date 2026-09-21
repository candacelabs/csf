import CounterLive from '@/components/CounterLive';
import { currentRoom, newSessionId } from '@/lib/session';
import { snapshot } from '@/lib/store';

/*
 * The measured route (§2.1).
 *
 * `force-dynamic` is §5.5's fairness constraint made explicit: the equivalent
 * gotth-live route renders current session state and cannot be served from a
 * cache, so this one may not be either. It is a constraint on the COMPARISON,
 * not a claim that Next.js cannot cache — the cached variant is a separate
 * measured row (§4.5, AS-6) and it is not this file.
 *
 * This is a Server Component (AS-7). It renders the static shell and the first
 * paint of the live values, so the document that arrives already contains the
 * number — the same property the gotth-live page has, and the reason D5's TTI
 * is about becoming interactive rather than about becoming legible.
 */
export const dynamic = 'force-dynamic';

export default async function CounterPage() {
  const initial = snapshot(await currentRoom());

  /*
   * The tab id is minted HERE, on the server, and not in the client component.
   *
   * One tab is one page load, so a per-render id is the right lifetime — it is
   * the same lifetime as a gotth-live session, which is one connection bound to
   * one tab (§3.4). Minting it on the server also means the "this tab" /
   * "another tab" line renders identically during SSR and after hydration;
   * minting it in the browser would produce a hydration mismatch, which is a
   * bug class the competing stack structurally does not have (§8.2 G-8) and
   * therefore not one to hand it for free.
   */
  const tabId = newSessionId();

  return (
    <main>
      <h1>Next.js counter</h1>
      <p className="lede">
        The value lives in the Node process. Clicking calls a Server Action, the
        server applies the operation, and every open tab is pushed the result.
      </p>

      <CounterLive initial={initial} tabId={tabId} />

      <p className="status">
        connection: <span className="dot"></span>
      </p>
      <p className="hint">
        Open this page in a second tab. Both show the same number, both repaint
        when either changes, and a reload keeps the value &mdash; none of it is
        in the browser.
      </p>
    </main>
  );
}
