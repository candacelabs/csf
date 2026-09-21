'use client';

import { useRef } from 'react';
import useSWR from 'swr';

import type { DashView } from '../core';
import type { LiveState, UseDashboardLive } from './types';

/*
 * Variant "poll" — SWR refreshInterval (§5.4), the third labelled column.
 *
 * Measured for D3/D4 only, because that is where it changes the trade
 * fundamentally: no per-connection server state, and the cost moves into
 * request overhead, proxy connection churn and CPU. §3.4 requires every memory
 * row to be published beside a server-CPU row for exactly this reason, so this
 * variant existing is what stops the polling column from looking free.
 *
 * The polling endpoint returns the WHOLE view rather than a patch, and that is
 * not a pessimization — it is what polling is. A poller has no session-scoped
 * cursor the server can patch against without holding per-client state, and
 * holding that state is precisely the thing polling is chosen to avoid. The
 * resulting byte cost is the polling column's real cost and belongs in §4.6.
 */

const INTERVAL_MS = Number(process.env.NEXT_PUBLIC_BENCH_POLL_INTERVAL_MS ?? 1000);

async function fetchView(url: string): Promise<DashView> {
  const res = await fetch(url, { cache: 'no-store' });
  if (!res.ok) throw new Error(`snapshot ${res.status}`);
  return (await res.json()) as DashView;
}

export const useDashboardLive: UseDashboardLive = (sessionKey, initial) => {
  const key = `/api/dashboard/snapshot?k=${encodeURIComponent(sessionKey)}&ttl=${INTERVAL_MS * 2}`;
  const received = useRef(0);

  const { data, error } = useSWR<DashView, Error>(key, fetchView, {
    refreshInterval: INTERVAL_MS,
    fallbackData: initial,
    revalidateOnFocus: false,
    keepPreviousData: true,
    onSuccess: () => {
      received.current += 1;
    },
  });

  const status: LiveState['status'] = error ? 'reconnecting' : data ? 'live' : 'connecting';
  return { view: data ?? initial, status, received: received.current };
};
