/*
 * The WebSocket sidecar for the two per-session apps (chat, dashboard) —
 * §5.4's secondary live-data variant: "Dedicated WebSocket server (`ws`)
 * alongside the standalone Next server, in the same container."
 *
 * Two processes, one container, both inside the cgroup §3.6 samples. That is
 * not an accident of packaging: §3.6 says "whatever processes the idiomatic
 * architecture requires are inside that one container and are all counted", and
 * this process is what a Next.js team pays for a WebSocket. Its RSS is part of
 * the ws variant's memory number, exactly as gotth-live's single Go binary is
 * the whole of its own.
 *
 * -----------------------------------------------------------------------------
 * Why one upstream per socket, and not one upstream fanned out
 *
 * The counter's sidecar (apps/counter/next/ws-server/relay.mjs) holds ONE
 * upstream stream and broadcasts it, because the counter's view is global: every
 * tab sees the same number. Chat and the dashboard are not like that. Their
 * views are per session — unread badges, active room, the filter/sort/pause
 * state of this tab — so a single fanned-out stream would deliver one session's
 * view to every socket, which is not a slower version of the feature, it is a
 * different and wrong feature.
 *
 * Two designs give the right answer:
 *
 *   (a) one upstream per socket, this process piping bytes through;
 *   (b) move the authority into this process and have Server Actions call it.
 *
 * (b) was rejected for the same reason the counter's sidecar rejected it: the
 * server-rendered first paint (D5's TTI, and every reload) becomes a loopback
 * round trip instead of an in-process read, and the measured click path ties.
 * (a) is what is implemented. Its cost — one loopback HTTP connection per
 * browser — is real, is inside the container, and is therefore inside the ws
 * variant's memory and CPU numbers where it belongs. Recording the option not
 * taken is what §5.4's audit is entitled to see.
 */
import http from 'node:http';
import { WebSocketServer } from 'ws';

export function startRelay({
  port = Number(process.env.BENCH_WS_PORT ?? 3101),
  host = process.env.BENCH_WS_HOST ?? '0.0.0.0',
  upstream = process.env.BENCH_UPSTREAM ?? 'http://127.0.0.1:3000',
  path = process.env.BENCH_WS_PATH ?? '/ws',
  streamPath,
  name,
}) {
  const keepAlive = new http.Agent({ keepAlive: true, maxSockets: Infinity });
  let open = 0;

  const server = http.createServer((req, res) => {
    // The sidecar serves exactly one thing over plain HTTP: a liveness probe.
    if (req.url === '/healthz') {
      res.writeHead(200, { 'content-type': 'text/plain' });
      res.end(`${name} sockets=${open}\n`);
      return;
    }
    res.writeHead(404).end();
  });

  const wss = new WebSocketServer({ server, path });

  wss.on('connection', (socket, request) => {
    open++;
    const url = new URL(request.url ?? '/', 'http://relay.invalid');
    const key = url.searchParams.get('k') ?? '';
    const room = url.searchParams.get('room');

    const target = new URL(`${upstream}${streamPath}`);
    if (key) target.searchParams.set('k', key);
    if (room) target.searchParams.set('room', room);

    const headers = { accept: 'text/event-stream' };
    /*
     * The browser's cookies are forwarded verbatim. Identity (bench_who) and
     * the session cookie are what the upstream route reads, and a sidecar that
     * dropped them would silently serve every socket the default identity —
     * which would make F-CHT-9's read-only case pass on the sse variant and
     * fail on the ws variant for a reason that is not the transport.
     */
    if (request.headers.cookie) headers.cookie = request.headers.cookie;

    const upstreamReq = http.get(target, { agent: keepAlive, headers }, (res) => {
      if (res.statusCode !== 200) {
        res.resume();
        socket.close(1011, 'upstream');
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
          if (socket.readyState === socket.OPEN) socket.send(data);
        }
      });
      res.on('end', () => socket.close(1011, 'upstream closed'));
      res.on('error', () => socket.close(1011, 'upstream error'));
    });

    upstreamReq.on('error', () => socket.close(1011, 'upstream unreachable'));

    const drop = () => {
      open--;
      upstreamReq.destroy();
    };
    socket.on('close', drop);
    socket.on('error', () => {
      socket.terminate();
    });
  });

  server.listen(port, host, () => {
    process.stdout.write(
      `bench ws sidecar (${name}): ws://${host}:${port}${path} upstream=${upstream}${streamPath}\n`,
    );
  });

  for (const signal of ['SIGTERM', 'SIGINT']) {
    process.on(signal, () => {
      wss.close();
      server.close(() => process.exit(0));
    });
  }

  return server;
}
