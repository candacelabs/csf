'use client';

import useSWRSubscription from 'swr/subscription';

import type { RoomView } from '../core';
import type { LiveState, UseChatLive } from './types';

/*
 * Variant "sse" — the primary, per §5.4: SSE from a streaming Route Handler,
 * consumed with SWR's useSWRSubscription. No extra process, no custom server,
 * entirely inside Next.js.
 *
 * EventSource rather than fetch+ReadableStream, for the same reason as the
 * counter: it is the idiomatic client for text/event-stream and it reconnects
 * on its own with the server's retry hint. Hand-rolling that would be more code
 * for the same behaviour, and any bug in it would be measured as a Next.js
 * latency loss that is really an author's loss.
 */

export const useChatLive: UseChatLive = (sessionKey, initial) => {
  const key = `/api/chat/stream?k=${encodeURIComponent(sessionKey)}`;

  const { data } = useSWRSubscription<LiveState, Error, string>(key, (url, { next }) => {
    let current: LiveState = { view: initial, status: 'connecting', received: 0 };

    const push = (patch: Partial<LiveState>) => {
      current = { ...current, ...patch };
      next(null, current);
    };

    const source = new EventSource(url);

    source.onopen = () => push({ status: 'live' });

    source.onmessage = (event: MessageEvent<string>) => {
      let view: RoomView;
      try {
        view = JSON.parse(event.data) as RoomView;
      } catch {
        return;
      }
      // Out-of-order or duplicated delivery repairs itself: a view older than
      // the one already held is dropped.
      if (view.version < current.view.version) return;
      push({ view, status: 'live', received: current.received + 1 });
    };

    source.onerror = () => {
      push({ status: source.readyState === 2 ? 'closed' : 'reconnecting' });
    };

    return () => source.close();
  });

  return data ?? { view: initial, status: 'connecting', received: 0 };
};
