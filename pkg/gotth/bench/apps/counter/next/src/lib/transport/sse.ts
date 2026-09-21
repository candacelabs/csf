'use client';

import useSWRSubscription from 'swr/subscription';

import type { Snapshot } from '../core';
import type { LiveState, UseCounterLive } from './types';

/*
 * Variant "sse" — the primary, per §5.4: SSE from a streaming Route Handler,
 * consumed with SWR's useSWRSubscription. No extra process, no custom server,
 * entirely inside Next.js.
 *
 * EventSource is used rather than fetch+ReadableStream because it is the
 * idiomatic client for text/event-stream and it reconnects on its own with the
 * server's retry hint — writing that by hand would be more code for the same
 * behaviour, and any bug in it would show up as a Next.js latency loss that is
 * really an author's loss.
 */

export const useCounterLive: UseCounterLive = (tabId, initial) => {
  const key = `/api/counter/stream?tab=${encodeURIComponent(tabId)}`;

  const { data } = useSWRSubscription<LiveState, Error, string>(key, (url, { next }) => {
    let current: LiveState = { snapshot: initial, status: 'connecting', received: 0 };

    const push = (patch: Partial<LiveState>) => {
      current = { ...current, ...patch };
      next(null, current);
    };

    const source = new EventSource(url);

    source.onopen = () => push({ status: 'live' });

    source.onmessage = (event: MessageEvent<string>) => {
      let snapshot: Snapshot;
      try {
        snapshot = JSON.parse(event.data) as Snapshot;
      } catch {
        return;
      }
      // Out-of-order or duplicated delivery repairs itself: a snapshot older
      // than the one already held is dropped, exactly as applySync does in
      // examples/counter/counter.go.
      if (snapshot.version < current.snapshot.version) return;
      push({ snapshot, status: 'live', received: current.received + 1 });
    };

    source.onerror = () => {
      // readyState 2 == CLOSED: EventSource has given up. Anything else means
      // it is retrying on its own.
      push({ status: source.readyState === 2 ? 'closed' : 'reconnecting' });
    };

    return () => source.close();
  });

  return data ?? { snapshot: initial, status: 'connecting', received: 0 };
};
