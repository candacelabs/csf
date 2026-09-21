import 'server-only';

import { cookies } from 'next/headers';

/*
 * The session cookie.
 *
 * §3.4 defines a Next.js active session as a tab holding the push channel "plus
 * its session cookie", so the cookie has to exist for the D3 workload to mean
 * what the spec says. It is set by the route handlers rather than by
 * middleware: middleware would run in its own runtime on every request to the
 * measured route, and paying that on a route that does not need it is a
 * self-inflicted Next.js cost — the kind the §5.4 pessimization audit exists to
 * catch.
 *
 * The dashboard has no identity beyond the session; every control is per
 * session and nothing in region A..E depends on who you are.
 */

export const SESSION_COOKIE = 'bench_sid';

export function newSessionId(): string {
  return crypto.randomUUID().replace(/-/g, '');
}

export interface Identity {
  sid: string;
  /** True when the sid was minted for this request and must be Set-Cookie'd. */
  fresh: boolean;
}

export async function identity(): Promise<Identity> {
  const jar = await cookies();
  const existing = jar.get(SESSION_COOKIE)?.value;
  return { sid: existing ?? newSessionId(), fresh: existing === undefined };
}

export function identityFrom(request: Request): Identity {
  const header = request.headers.get('cookie') ?? '';
  for (const part of header.split(';')) {
    const eq = part.indexOf('=');
    if (eq === -1) continue;
    if (part.slice(0, eq).trim() === SESSION_COOKIE) {
      return { sid: decodeURIComponent(part.slice(eq + 1).trim()), fresh: false };
    }
  }
  return { sid: newSessionId(), fresh: true };
}

export function sessionCookie(sid: string): string {
  return `${SESSION_COOKIE}=${sid}; Path=/; HttpOnly; SameSite=Lax; Max-Age=86400`;
}
