'use client';

import useSWRSubscription from 'swr/subscription';

import type { Patch } from '../core';
import { applyPatch } from '../patch';
import type { LiveState, UseDashboardLive } from './types';

/*
 * Variant "sse" — the primary, per §5.4: SSE from a streaming Route Handler,
 * consumed with SWR's useSWRSubscription. No extra process, no custom server,
 * entirely inside Next.js.
 *
 * EventSource rather than fetch+ReadableStream: it is the idiomatic client for
 * text/event-stream and it reconnects on its own with the server's retry hint.
 * Hand-rolling that would be more code for the same behaviour, and any bug in
 * it would be measured as a Next.js latency loss that is really an author's
 * loss.
 */

export const useDashboardLive: UseDashboardLive = (sessionKey, initial) => {
  const key = `/api/dashboard/stream?k=${encodeURIComponent(sessionKey)}`;

  const { data } = useSWRSubscription<LiveState, Error, string>(key, (url, { next }) => {
    let current: LiveState = { view: initial, status: 'connecting', received: 0 };

    const push = (patch: Partial<LiveState>) => {
      current = { ...current, ...patch };
      next(null, current);
    };

    const source = new EventSource(url);

    source.onopen = () => push({ status: 'live' });

    source.onmessage = (event: MessageEvent<string>) => {
      let patch: Patch;
      try {
        patch = JSON.parse(event.data) as Patch;
      } catch {
        return;
      }
      // Out-of-order or duplicated delivery repairs itself: a patch older than
      // the view already held is dropped.
      if (patch.version < current.view.version) return;
      push({
        view: applyPatch(current.view, patch),
        status: 'live',
        received: current.received + 1,
      });
    };

    source.onerror = () => {
      push({ status: source.readyState === 2 ? 'closed' : 'reconnecting' });
    };

    return () => source.close();
  });

  return data ?? { view: initial, status: 'connecting', received: 0 };
};
