'use client';

import { useEffect, useRef, useState } from 'react';

import type { RoomView } from '../core';
import type { LiveState, UseChatLive } from './types';

/*
 * Variant "ws" — the secondary, per §5.4: a dedicated WebSocket server (`ws`)
 * alongside the standalone Next server, in the same container.
 *
 * useSWRSubscription is deliberately not used here: SWR's subscription hook
 * gives cache sharing and revalidation semantics a single always-on socket does
 * not need, and wrapping the socket in it would add a cache layer to the
 * measured push path for no behaviour. The reconnect/backoff loop below is what
 * the hook would otherwise have wrapped.
 *
 * The socket URL defaults to same-origin /ws, which is how the bench topology
 * serves it: the shared reverse proxy (§3.6) routes /ws to the sidecar and
 * everything else to the standalone Next server, so the browser sees one origin
 * and the harness sees one URL (§4).
 */

const WS_URL = process.env.NEXT_PUBLIC_BENCH_WS_URL ?? '';
const WS_PATH = process.env.NEXT_PUBLIC_BENCH_WS_PATH ?? '/ws';

function socketUrl(sessionKey: string): string {
  const base =
    WS_URL !== ''
      ? WS_URL
      : `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}${WS_PATH}`;
  const sep = base.includes('?') ? '&' : '?';
  return `${base}${sep}k=${encodeURIComponent(sessionKey)}`;
}

export const useChatLive: UseChatLive = (sessionKey, initial) => {
  const [state, setState] = useState<LiveState>({
    view: initial,
    status: 'connecting',
    received: 0,
  });
  const version = useRef(initial.version);

  useEffect(() => {
    let closed = false;
    let socket: WebSocket | null = null;
    let retry: ReturnType<typeof setTimeout> | null = null;
    let attempt = 0;

    const connect = () => {
      if (closed) return;
      socket = new WebSocket(socketUrl(sessionKey));

      socket.onopen = () => {
        attempt = 0;
        setState((s) => ({ ...s, status: 'live' }));
      };

      socket.onmessage = (event: MessageEvent<string>) => {
        let view: RoomView;
        try {
          view = JSON.parse(event.data) as RoomView;
        } catch {
          return;
        }
        if (view.version < version.current) return;
        version.current = view.version;
        setState((s) => ({ view, status: 'live', received: s.received + 1 }));
      };

      socket.onclose = () => {
        if (closed) return;
        setState((s) => ({ ...s, status: 'reconnecting' }));
        const delay = Math.min(5000, 100 * 2 ** attempt);
        attempt += 1;
        retry = setTimeout(connect, delay);
      };

      socket.onerror = () => {
        // 'close' always follows; the status change belongs there so a
        // transient error does not flicker the indicator.
      };
    };

    connect();

    return () => {
      closed = true;
      if (retry !== null) clearTimeout(retry);
      if (socket) {
        socket.onclose = null;
        socket.close();
      }
    };
  }, [sessionKey]);

  return state;
};
