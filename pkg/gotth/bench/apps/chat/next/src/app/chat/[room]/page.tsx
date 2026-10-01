import ChatLive from '@/components/ChatLive';
import { ROOMS } from '@/lib/core';
import { identity, newSessionId, roomOr } from '@/lib/session';
import { snapshot } from '@/lib/store';

/*
 * The measured route (§2.3), `/chat/[room]` with three fixed rooms.
 *
 * `force-dynamic` is §5.5's fairness constraint made explicit: the equivalent
 * gotth-live route renders current session state and cannot be served from a
 * cache, so this one may not be either. It is a constraint on the COMPARISON,
 * not a claim that Next.js cannot cache — the cached variant is a separate
 * measured row (§4.5, AS-6) and it is not this file.
 *
 * This is a Server Component (AS-7). It renders the static shell and the first
 * paint of the room, so the document that arrives already contains the
 * messages — the same property the gotth-live page has, and the reason D5's TTI
 * is about becoming interactive rather than about becoming legible.
 */
export const dynamic = 'force-dynamic';

export default async function ChatPage({ params }: { params: Promise<{ room: string }> }) {
  const { room } = await params;
  const who = await identity();

  /*
   * The session key is minted HERE, on the server, and once per page load.
   *
   * One tab is one page load, so a per-render key is the right lifetime — the
   * same lifetime as a gotth-live session, which is one connection bound to one
   * tab (§3.4). Minting it on the server also keeps SSR and hydration rendering
   * the same string; minting it in the browser would produce a hydration
   * mismatch, a bug class the competing stack structurally does not have
   * (§8.2 G-8) and therefore not one to hand it for free.
   */
  const sessionKey = newSessionId();
  const initial = snapshot(sessionKey, who.me, roomOr(room));

  return (
    <main>
      <h1>Next.js chat</h1>
      <p className="lede">
        The rooms live in the Node process. Sending calls a Server Action, the
        server validates and appends, and every subscribed tab is pushed the
        result — including yours.
      </p>

      <ChatLive initial={initial} sessionKey={sessionKey} />

      <p className="status">
        connection: <span className="dot"></span>
      </p>
      <p className="hint">
        Three rooms ({ROOMS.join(', ')}), 200 messages kept, no virtualization.
        Switching rooms is a server round trip, not a client-side navigation.
      </p>
    </main>
  );
}
