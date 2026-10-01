import 'server-only';

import { cookies } from 'next/headers';

import { SCOPE } from './store';

/*
 * The session cookie, and the room key derived from it.
 *
 * §3.4 defines a Next.js active session as a tab holding the push channel
 * "plus its session cookie", so the cookie has to exist for the D3 workload to
 * mean what the spec says it means. It is set by the route handlers rather
 * than by middleware on purpose: middleware would run on every request to the
 * measured route in its own runtime, and paying that on a route that does not
 * need it would be a self-inflicted cost on the Next.js side — precisely the
 * kind of choice the pessimization audit (§5.4) exists to catch.
 */

export const SESSION_COOKIE = 'bench_sid';
export const GLOBAL_ROOM = 'global';

/** The room a request belongs to, under the configured counter scope. */
export function roomForSession(sessionId: string | undefined): string {
  if (SCOPE === 'session' && sessionId) return sessionId;
  return GLOBAL_ROOM;
}

/** Reads the session cookie in an RSC or Server Action. */
export async function currentRoom(): Promise<string> {
  if (SCOPE !== 'session') return GLOBAL_ROOM;
  const jar = await cookies();
  return roomForSession(jar.get(SESSION_COOKIE)?.value);
}

/** A fresh opaque session id. */
export function newSessionId(): string {
  return crypto.randomUUID().replace(/-/g, '');
}
