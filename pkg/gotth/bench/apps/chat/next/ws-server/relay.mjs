#!/usr/bin/env node
/*
 * Chat's WebSocket sidecar (§5.4 secondary variant). The design, the option not
 * taken, and why the upstream is per socket are all in
 * bench/scripts/ws-session-relay.mjs — this file exists so start-app.mjs finds a
 * relay at the same path in every app.
 */
import { startRelay } from '../../../../scripts/ws-session-relay.mjs';

startRelay({ streamPath: '/api/chat/stream', name: 'chat' });
