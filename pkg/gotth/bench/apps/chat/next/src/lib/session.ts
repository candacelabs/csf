import 'server-only';

import { cookies } from 'next/headers';

import { ROOMS, isRoom, type RoomId } from './core';

/*
 * Identity and session id, both cookies, both read in RSCs and Server Actions.
 *
 * §3.4 defines a Next.js active session as a tab holding the push channel
 * "plus its session cookie", so the cookie has to exist for D3's workload to
 * mean what the spec says. They are set by the route handlers rather than by
 * middleware, for the same reason the counter does it: middleware would run in
 * its own runtime on every request to the measured route, and paying that on a
 * route that does not need it is a self-inflicted Next.js cost — the kind the
 * §5.4 pessimization audit exists to catch.
 *
 * `bench_who` is the participant name. It defaults to `you` and the harness
 * sets it to `readonly` before CHT-8 (F-CHT-9). It is an identity claim with no
 * authentication behind it, which is correct for a bench app and stated so a
 * reviewer does not read it as an auth bug: the only thing the name can do is
 * make the server REFUSE more, never less.
 */

export const SESSION_COOKIE = 'bench_sid';
export const WHO_COOKIE = 'bench_who';
export const DEFAULT_NAME = 'you';

export function newSessionId(): string {
  return crypto.randomUUID().replace(/-/g, '');
}

/** Sanitised participant name: short, printable, and never empty. */
export function normalizeName(raw: string | undefined): string {
  const name = (raw ?? '').trim().slice(0, 24);
  return /^[a-z0-9_-]+$/i.test(name) ? name : DEFAULT_NAME;
}

export interface Identity {
  sid: string;
  me: string;
  /** True when the sid was minted for this request and must be Set-Cookie'd. */
  fresh: boolean;
}

export async function identity(): Promise<Identity> {
  const jar = await cookies();
  const existing = jar.get(SESSION_COOKIE)?.value;
  return {
    sid: existing ?? newSessionId(),
    me: normalizeName(jar.get(WHO_COOKIE)?.value),
    fresh: existing === undefined,
  };
}

/** Reads identity from a Request's cookie header, for route handlers. */
export function identityFrom(request: Request): Identity {
  const header = request.headers.get('cookie') ?? '';
  const jar = new Map<string, string>();
  for (const part of header.split(';')) {
    const eq = part.indexOf('=');
    if (eq === -1) continue;
    jar.set(part.slice(0, eq).trim(), decodeURIComponent(part.slice(eq + 1).trim()));
  }
  const existing = jar.get(SESSION_COOKIE);
  return {
    sid: existing ?? newSessionId(),
    me: normalizeName(jar.get(WHO_COOKIE)),
    fresh: existing === undefined,
  };
}

export function sessionCookie(sid: string): string {
  return `${SESSION_COOKIE}=${sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400`;
}

export function roomOr(raw: string | undefined, fallback: RoomId = ROOMS[0]): RoomId {
  return isRoom(raw) ? raw : fallback;
}
