#!/usr/bin/env node
/*
 * The WebSocket sidecar — §5.4's secondary live-data variant: "Dedicated
 * WebSocket server (`ws`) alongside the standalone Next server, in the same
 * container."
 *
 * Two processes, one container, both inside the cgroup §3.6 samples. That is
 * not an accident of packaging: §3.6 says "whatever processes the idiomatic
 * architecture requires are inside that one container and are all counted",
 * and this process is what a Next.js team pays for a WebSocket. Its RSS is
 * part of the ws variant's memory number, exactly as gotth-live's single Go
 * binary is the whole of its own.
 *
 * ---------------------------------------------------------------------------
 * Where the state lives, and why it is not here
 *
 * The counter's authority stays in the Next process. This process subscribes
 * to the same SSE stream a browser would (with relay=1, so it is not counted
 * as a tab) and fans every snapshot out to its sockets.
 *
 * The alternative — moving the authority into this process and having Server
 * Actions call it — was considered and rejected on hop count, which is the
 * thing the measurement is sensitive to. Either way a click crosses two
 * boundaries before it paints:
 *
 *     authority here:  browser -> Next (forward) -> sidecar (apply) -> browser
 *     authority there: browser -> Next (apply)   -> sidecar (relay) -> browser
 *
 * They tie on the measured path, and keeping the authority in Next means the
 * server-rendered first paint (§2.1's F-CTR-4 reload, and D5's TTI) is an
 * in-process read rather than a loopback round trip. So this design is the one
 * that is faster for Next.js, not the one that is simpler to write.
 *
 * Recorded here because a reviewer running the pessimization audit (§5.4) is
 * entitled to see the option that was not taken.
 */
import http from 'node:http';
import { WebSocketServer } from 'ws';

const PORT = Number(process.env.BENCH_WS_PORT ?? 3101);
const HOST = process.env.BENCH_WS_HOST ?? '0.0.0.0';
const UPSTREAM = process.env.BENCH_UPSTREAM ?? 'http://127.0.0.1:3000';
const TOKEN = process.env.BENCH_RELAY_TOKEN ?? '';
const ROOM = process.env.BENCH_ROOM ?? 'global';
const PATHNAME = process.env.BENCH_WS_PATH ?? '/ws';

/** The most recent snapshot, so a socket that connects mid-stream is correct at once. */
let latest = null;

/** One pooled connection to the authority, for both the upstream and presence. */
const keepAlive = new http.Agent({ keepAlive: true, maxSockets: 4 });

const server = http.createServer((req, res) => {
  // The sidecar serves exactly one thing over plain HTTP: a liveness probe.
  if (req.url === '/healthz') {
    res.writeHead(200, { 'content-type': 'text/plain' });
    res.end(latest ? 'live\n' : 'connecting\n');
    return;
  }
  res.writeHead(404).end();
});

const wss = new WebSocketServer({ server, path: PATHNAME });

wss.on('connection', (socket) => {
  if (latest !== null) socket.send(latest);
  socket.on('close', reportPresence);
  socket.on('error', () => socket.terminate());
  reportPresence();
});

function broadcast(payload) {
  for (const socket of wss.clients) {
    if (socket.readyState === socket.OPEN) socket.send(payload);
  }
}

/*
 * Presence: how many browsers this process is holding, told to the authority
 * so the page can render F-CTR-5's tab count. Coalesced on a microtask-ish
 * timer so a burst of connects during a 1000-session ramp is one call, not a
 * thousand.
 */
let presenceTimer = null;
let lastReported = -1;

function reportPresence() {
  if (presenceTimer !== null) return;
  presenceTimer = setTimeout(() => {
    presenceTimer = null;
    const tabs = wss.clients.size;
    if (tabs === lastReported) return;
    lastReported = tabs;
    const body = JSON.stringify({ room: ROOM, tabs });
    const req = http.request(
      `${UPSTREAM}/api/counter/presence`,
      {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'content-length': Buffer.byteLength(body),
          'x-bench-relay-token': TOKEN,
        },
        agent: keepAlive,
      },
      (res) => res.resume(),
    );
    req.on('error', () => {
      // The authority is restarting; the next connect/disconnect retries.
      lastReported = -1;
    });
    req.end(body);
  }, 50);
}

/*
 * The upstream: the same SSE endpoint a browser uses in the sse variant. Kept
 * open forever, reconnected with backoff, and parsed with the smallest
 * event-stream reader that is actually correct (events are separated by a
 * blank line; `data:` lines concatenate; `:` lines are comments/heartbeats).
 */
function connectUpstream(attempt = 0) {
  const url = `${UPSTREAM}/api/counter/stream?relay=1&tab=relay`;
  const req = http.get(url, { agent: keepAlive, headers: { accept: 'text/event-stream' } }, (res) => {
    if (res.statusCode !== 200) {
      res.resume();
      retry(attempt + 1);
      return;
    }
    res.setEncoding('utf8');
    let buffer = '';
    res.on('data', (chunk) => {
      buffer += chunk;
      let split;
      while ((split = buffer.indexOf('\n\n')) !== -1) {
        const block = buffer.slice(0, split);
        buffer = buffer.slice(split + 2);
        const data = block
          .split('\n')
          .filter((line) => line.startsWith('data:'))
          .map((line) => line.slice(5).trimStart())
          .join('\n');
        if (data === '') continue; // heartbeat comment
        latest = data;
        broadcast(data);
      }
    });
    res.on('end', () => retry(0));
    res.on('error', () => retry(0));
  });
  req.on('error', () => retry(attempt + 1));
}

function retry(attempt) {
  const delay = Math.min(5000, 100 * 2 ** Math.min(attempt, 6));
  setTimeout(() => connectUpstream(attempt), delay);
}

server.listen(PORT, HOST, () => {
  process.stdout.write(`bench ws sidecar: ws://${HOST}:${PORT}${PATHNAME} upstream=${UPSTREAM}\n`);
  connectUpstream();
});

for (const signal of ['SIGTERM', 'SIGINT']) {
  process.on(signal, () => {
    wss.close();
    server.close(() => process.exit(0));
  });
}
