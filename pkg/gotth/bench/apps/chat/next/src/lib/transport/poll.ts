'use client';

import { useRef } from 'react';
import useSWR from 'swr';

import type { RoomView } from '../core';
import type { LiveState, UseChatLive } from './types';

/*
 * Variant "poll" — SWR refreshInterval (§5.4), the third labelled column.
 *
 * Measured for D3/D4 only, because that is where it changes the trade
 * fundamentally: no per-connection server state, and the cost moves into
 * request overhead, proxy connection churn and CPU. §3.4 requires every memory
 * row to be published beside a server-CPU row for exactly this reason, so this
 * variant existing is what stops the polling column from looking free.
 *
 * SWR's revalidateOnFocus/offline defaults are LEFT ON, because turning them
 * off would inflate the polling variant's cost with traffic a real deployment
 * would not send.
 */

const INTERVAL_MS = Number(process.env.NEXT_PUBLIC_BENCH_POLL_INTERVAL_MS ?? 1000);

async function fetchView(url: string): Promise<RoomView> {
  const res = await fetch(url, { cache: 'no-store' });
  if (!res.ok) throw new Error(`snapshot ${res.status}`);
  return (await res.json()) as RoomView;
}

export const useChatLive: UseChatLive = (sessionKey, initial) => {
  const key = `/api/chat/snapshot?k=${encodeURIComponent(sessionKey)}&ttl=${INTERVAL_MS * 2}`;
  const received = useRef(0);

  const { data, error } = useSWR<RoomView, Error>(key, fetchView, {
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
