'use client';

import { useRef } from 'react';
import useSWR from 'swr';

import type { Snapshot } from '../core';
import type { LiveState, UseCounterLive } from './types';

/*
 * Variant "poll" — SWR refreshInterval (§5.4), the third labelled column.
 *
 * It is measured for D3/D4 only, because that is where it changes the trade
 * fundamentally: no per-connection server state, and the cost moves into
 * request overhead, proxy connection churn and CPU. §3.4 requires every memory
 * row to be published beside a server-CPU row for exactly this reason, so this
 * variant existing is what stops the polling column from looking free.
 *
 * `refreshInterval` alone would not poll while the tab is hidden or the
 * network is reported down; both of those defaults are LEFT ON, because
 * turning them off would inflate the polling variant's cost with traffic a
 * real deployment would not send. The interval is the only knob (§5.4 names no
 * value; see lib/variant.ts for the default taken and why).
 */

const INTERVAL_MS = Number(process.env.NEXT_PUBLIC_BENCH_POLL_INTERVAL_MS ?? 1000);

async function fetchSnapshot(url: string): Promise<Snapshot> {
  const res = await fetch(url, { cache: 'no-store' });
  if (!res.ok) throw new Error(`snapshot ${res.status}`);
  return (await res.json()) as Snapshot;
}

export const useCounterLive: UseCounterLive = (tabId, initial) => {
  const key = `/api/counter/snapshot?tab=${encodeURIComponent(tabId)}&ttl=${INTERVAL_MS * 2}`;
  const received = useRef(0);

  const { data, error } = useSWR<Snapshot, Error>(key, fetchSnapshot, {
    refreshInterval: INTERVAL_MS,
    fallbackData: initial,
    revalidateOnFocus: false,
    keepPreviousData: true,
    onSuccess: () => {
      received.current += 1;
    },
  });

  const status: LiveState['status'] = error ? 'reconnecting' : data ? 'live' : 'connecting';
  return { snapshot: data ?? initial, status, received: received.current };
};
