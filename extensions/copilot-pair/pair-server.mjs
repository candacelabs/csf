import { readFile } from "node:fs/promises";
import { createServer } from "node:http";
import { networkInterfaces } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

import { GrowOnlyEventSet } from "./event-set.mjs";

const ASSET_DIRECTORY = join(dirname(fileURLToPath(import.meta.url)), "public");
const MAX_BODY_BYTES = 5 * 1024 * 1024;

// Sent once, ahead of the first browser prompt, so the model knows it is in a
// shared group session rather than a one-on-one conversation. Single line: the
// browser UI strips everything before the first blank line when displaying
// that message. The "[Copilot Pair]" marker must match GROUP_PREAMBLE_MARKER
// in public/app.js.
export const GROUP_CHAT_PREAMBLE = "[Copilot Pair] Heads up: this session is a shared group chat, not a"
  + " conversation with one person. Several people are connected to this one Copilot session from their"
  + " browsers. Each relayed message starts with the sender's name, like \"Ada: can you profile this?\";"
  + " that name prefix is metadata added by the relay, not text the sender typed. Messages without a name"
  + " prefix come from the session owner typing in the terminal. Different messages may come from"
  + " different people with different goals, so do not assume consecutive messages share one author or"
  + " intent, and address people by name when replying to a specific request.";

export class PairShare {
  #session;
  #options;
  #events = new GrowOnlyEventSet();
  #clients = new Set();
  #actionResults = new Map();
  #actionTail = Promise.resolve();
  #server;
  #keepAlive;
  #link;
  #groupContextSent = false;

  constructor(session, options = {}) {
    if (!session || typeof session.send !== "function") {
      throw new TypeError("PairShare requires a Copilot session");
    }
    this.#session = session;
    this.#options = { ...defaultPairOptions(), ...options };
  }

  get running() {
    return Boolean(this.#server?.listening);
  }

  get link() {
    return this.#link;
  }

  get connectedClients() {
    return this.#clients.size;
  }

  get sessionId() {
    return this.#session.sessionId;
  }

  get eventCount() {
    return this.#events.size;
  }

  ingest(event) {
    const normalized = toPlainJson(event);
    if (!normalized || typeof normalized !== "object" || typeof normalized.id !== "string") {
      return false;
    }

    if (normalized.ephemeral === true) {
      this.#broadcast("session-event", { durable: false, event: normalized });
      return true;
    }

    const added = this.#events.add(normalized);
    if (added) {
      this.#broadcast("session-event", { durable: true, event: normalized });
    }
    return added;
  }

  merge(events) {
    let added = 0;
    for (const event of events ?? []) {
      const normalized = toPlainJson(event);
      if (this.#events.add(normalized)) {
        added += 1;
        this.#broadcast("session-event", { durable: true, event: normalized });
      }
    }
    return added;
  }

  async start() {
    if (this.running) {
      return this.status();
    }

    this.merge(await readSessionEvents(this.#session));
    this.#server = createServer((request, response) => {
      void this.#handleRequest(request, response).catch((error) => {
        if (response.headersSent) {
          response.destroy();
          return;
        }
        const status = error instanceof HttpError ? error.status : 500;
        const code = error instanceof HttpError ? error.code : "internal_error";
        sendJson(response, status, { error: { code, message: error.message } });
      });
    });

    await listen(this.#server, this.#options.port, this.#options.listenHost);
    const address = this.#server.address();
    if (!address || typeof address === "string") {
      await this.stop();
      throw new Error("Copilot Pair did not receive a TCP listen address");
    }

    const baseUrl = this.#options.publicUrl
      ? this.#options.publicUrl.replace(/\/$/, "")
      : `http://${formatUrlHost(this.#options.publicHost)}:${address.port}`;
    this.#link = `${baseUrl}/`;

    this.#keepAlive = setInterval(() => {
      this.#broadcastComment("keepalive");
    }, 15_000);
    this.#keepAlive.unref?.();

    return this.status();
  }

  async stop() {
    this.#link = undefined;
    this.#actionResults.clear();

    if (this.#keepAlive) {
      clearInterval(this.#keepAlive);
      this.#keepAlive = undefined;
    }

    for (const response of this.#clients) {
      writeSse(response, "share-stopped", {});
      response.end();
    }
    this.#clients.clear();

    const server = this.#server;
    this.#server = undefined;
    if (server?.listening) {
      await close(server);
    }
  }

  status() {
    return {
      running: this.running,
      link: this.#link,
      connectedClients: this.connectedClients,
      eventCount: this.eventCount,
      sessionId: this.sessionId,
    };
  }

  async #handleRequest(request, response) {
    response.setHeader("Cache-Control", "no-store");
    const url = new URL(request.url ?? "/", "http://copilot-pair.local");

    if (request.method === "GET" && url.pathname === "/healthz") {
      return sendJson(response, 200, { status: "ok" });
    }
    if (request.method === "GET" && url.pathname === "/") {
      return serveAsset(response, "index.html", "text/html; charset=utf-8");
    }
    if (request.method === "GET" && url.pathname === "/app.js") {
      return serveAsset(response, "app.js", "text/javascript; charset=utf-8");
    }
    if (request.method === "GET" && url.pathname === "/renderer.js") {
      return serveAsset(response, "renderer.js", "text/javascript; charset=utf-8");
    }
    if (request.method === "GET" && url.pathname === "/styles.css") {
      return serveAsset(response, "styles.css", "text/css; charset=utf-8");
    }
    if (request.method === "GET" && url.pathname === "/api/meta") {
      return sendJson(response, 200, {
        connectedClients: this.connectedClients,
        eventCount: this.eventCount,
        sessionId: this.sessionId,
      });
    }
    if (request.method === "GET" && url.pathname === "/api/models") {
      return this.#listModels(response);
    }
    if (request.method === "GET" && url.pathname === "/api/events") {
      return this.#subscribe(request, response);
    }
    if (request.method === "POST" && url.pathname === "/api/events/merge") {
      return this.#mergeReplica(request, response);
    }
    if (request.method === "POST" && url.pathname === "/api/actions") {
      return this.#acceptAction(request, response);
    }

    throw new HttpError(404, "not_found", "Not found");
  }

  async #listModels(response) {
    // Hosts without any model catalog source get an empty catalog, telling the
    // browser to fall back to a free-text model field instead of failing.
    const rpcModel = this.#session.rpc?.model;
    let models = [];
    let current;
    try {
      models = (await this.#modelCatalogEntries())
        .filter((entry) => entry && typeof entry === "object" && typeof entry.id === "string" && entry.id)
        .map((entry) => ({
          id: entry.id,
          name: typeof entry.name === "string" && entry.name ? entry.name : entry.id,
        }));
      const selected = rpcModel?.getCurrent ? await rpcModel.getCurrent() : undefined;
      if (typeof selected?.modelId === "string" && selected.modelId) {
        current = selected.modelId;
      }
    } catch {
      models = [];
      current = undefined;
    }
    sendJson(response, 200, { models, ...(current ? { current } : {}) });
  }

  async #modelCatalogEntries() {
    const rpcModel = this.#session.rpc?.model;
    if (typeof rpcModel?.list === "function") {
      const catalog = await rpcModel.list();
      return catalog?.list ?? [];
    }
    // Copilot CLI 1.0.34 does not implement session.model.list; its own model
    // picker uses the client-level models.list RPC, reachable through the
    // session's connection. Drop this once shipped CLIs expose rpc.model.list.
    const connection = this.#session.connection;
    if (connection && typeof connection.sendRequest === "function") {
      const result = await connection.sendRequest("models.list", {});
      return result?.models ?? [];
    }
    return [];
  }

  #subscribe(request, response) {
    response.writeHead(200, {
      "Cache-Control": "no-store",
      Connection: "keep-alive",
      "Content-Type": "text/event-stream; charset=utf-8",
      "X-Accel-Buffering": "no",
    });
    response.write("retry: 1000\n\n");
    writeSse(response, "snapshot", {
      sessionId: this.sessionId,
      durableEvents: this.#events.values(),
    });

    this.#clients.add(response);
    this.#broadcastPresence();
    request.on("close", () => {
      if (this.#clients.delete(response)) {
        this.#broadcastPresence();
      }
    });
  }

  async #mergeReplica(request, response) {
    const body = await readJson(request);
    if (!Array.isArray(body.events)) {
      throw new HttpError(400, "invalid_replica", "events must be an array");
    }
    const events = body.events.filter(isReplicableEvent);
    const added = this.merge(events);
    sendJson(response, 200, { added, eventCount: this.eventCount });
  }

  async #acceptAction(request, response) {
    const action = await readJson(request);
    validateActionId(action.id);

    let task = this.#actionResults.get(action.id);
    if (!task) {
      task = this.#enqueueAction(() => this.#performAction(action));
      this.#actionResults.set(action.id, task);
      void task.catch(() => {});
    }

    const result = await task;
    sendJson(response, 200, result);
  }

  #enqueueAction(action) {
    const result = this.#actionTail.then(action);
    this.#actionTail = result.catch(() => {});
    return result;
  }

  async #performAction(action) {
    switch (action.type) {
      case "prompt": {
        const prompt = requiredText(action.prompt, "prompt", 64 * 1024);
        const actor = optionalText(action.actor, "actor", 64) || "Guest";
        const delivery = action.delivery === "immediate" ? "immediate" : "enqueue";
        const attributed = `${actor}: ${prompt}`;
        const messageId = await this.#session.send({
          prompt: this.#groupContextSent ? attributed : `${GROUP_CHAT_PREAMBLE}\n\n${attributed}`,
          mode: delivery,
        });
        this.#groupContextSent = true;
        return { ok: true, actionId: action.id, messageId };
      }
      case "abort":
        await this.#session.abort();
        return { ok: true, actionId: action.id };
      case "permission": {
        const requestId = requiredText(action.requestId, "requestId", 256);
        const result = action.decision === "approve"
          ? { kind: "approved" }
          : {
              kind: "denied-interactively-by-user",
              ...(action.feedback
                ? { feedback: optionalText(action.feedback, "feedback", 2_000) }
                : {}),
            };
        const handled = await this.#session.rpc.permissions
          .handlePendingPermissionRequest({ requestId, result });
        return { ok: true, actionId: action.id, handled };
      }
      case "model": {
        const model = requiredText(action.model, "model", 128);
        await this.#session.setModel(model);
        return { ok: true, actionId: action.id };
      }
      default:
        throw new HttpError(400, "invalid_action", "Unknown action type");
    }
  }

  #broadcastPresence() {
    this.#broadcast("presence", { connectedClients: this.connectedClients });
  }

  #broadcast(eventName, payload) {
    for (const response of this.#clients) {
      try {
        writeSse(response, eventName, payload);
      } catch {
        this.#clients.delete(response);
        response.destroy();
      }
    }
  }

  #broadcastComment(comment) {
    for (const response of this.#clients) {
      try {
        response.write(`: ${comment}\n\n`);
      } catch {
        this.#clients.delete(response);
        response.destroy();
      }
    }
  }
}

export async function readSessionEvents(session) {
  const read = typeof session?.getEvents === "function"
    ? session.getEvents
    : session?.getMessages;
  return typeof read === "function" ? await read.call(session) : [];
}

export function defaultPairOptions(environment = process.env) {
  const listenHost = environment.COPILOT_PAIR_LISTEN || "0.0.0.0";
  const publicHost = environment.COPILOT_PAIR_PUBLIC_HOST
    || (listenHost === "0.0.0.0" ? findShareableAddress() || "127.0.0.1" : listenHost);
  const port = parsePort(environment.COPILOT_PAIR_PORT);

  return {
    listenHost,
    port,
    publicHost,
    publicUrl: environment.COPILOT_PAIR_PUBLIC_URL,
  };
}

class HttpError extends Error {
  constructor(status, code, message) {
    super(message);
    this.status = status;
    this.code = code;
  }
}

function parsePort(value) {
  if (value === undefined || value === "") {
    return 0;
  }
  const port = Number(value);
  if (!Number.isInteger(port) || port < 0 || port > 65_535) {
    throw new Error("COPILOT_PAIR_PORT must be an integer from 0 through 65535");
  }
  return port;
}

function findShareableAddress() {
  let fallback;
  for (const addresses of Object.values(networkInterfaces())) {
    for (const address of addresses ?? []) {
      if (address.family !== "IPv4" || address.internal) {
        continue;
      }
      if (isPrivateLanIPv4(address.address)) {
        return address.address;
      }
      fallback ??= address.address;
    }
  }
  return fallback;
}

function isPrivateLanIPv4(address) {
  const octets = address.split(".").map(Number);
  if (octets.length !== 4 || !octets.every((octet) => Number.isInteger(octet))) {
    return false;
  }
  return octets[0] === 10
    || (octets[0] === 172 && octets[1] >= 16 && octets[1] <= 31)
    || (octets[0] === 192 && octets[1] === 168);
}

function formatUrlHost(host) {
  return host.includes(":") ? `[${host}]` : host;
}

function listen(server, port, host) {
  return new Promise((resolve, reject) => {
    const onError = (error) => {
      server.off("listening", onListening);
      reject(error);
    };
    const onListening = () => {
      server.off("error", onError);
      resolve();
    };
    server.once("error", onError);
    server.once("listening", onListening);
    server.listen(port, host);
  });
}

function close(server) {
  return new Promise((resolve, reject) => {
    server.close((error) => (error ? reject(error) : resolve()));
  });
}

async function serveAsset(response, filename, contentType) {
  const body = await readFile(join(ASSET_DIRECTORY, filename));
  response.writeHead(200, {
    "Content-Length": body.length,
    "Content-Type": contentType,
  });
  response.end(body);
}

async function readJson(request) {
  const contentType = request.headers["content-type"] ?? "";
  if (!contentType.toLowerCase().startsWith("application/json")) {
    throw new HttpError(415, "json_required", "Content-Type must be application/json");
  }

  const chunks = [];
  let size = 0;
  for await (const chunk of request) {
    size += chunk.length;
    if (size > MAX_BODY_BYTES) {
      throw new HttpError(413, "body_too_large", "Request body is too large");
    }
    chunks.push(chunk);
  }

  try {
    return JSON.parse(Buffer.concat(chunks).toString("utf8"));
  } catch {
    throw new HttpError(400, "invalid_json", "Request body is not valid JSON");
  }
}

function validateActionId(value) {
  if (typeof value !== "string" || !/^[A-Za-z0-9._:-]{8,128}$/.test(value)) {
    throw new HttpError(400, "invalid_action_id", "Action id is invalid");
  }
}

function isReplicableEvent(event) {
  return event
    && typeof event === "object"
    && typeof event.id === "string"
    && event.id.length > 0
    && event.id.length <= 256
    && typeof event.type === "string"
    && event.type.length > 0
    && event.type.length <= 256
    && typeof event.timestamp === "string"
    && event.ephemeral !== true;
}

function requiredText(value, field, maximumLength) {
  const text = optionalText(value, field, maximumLength);
  if (!text) {
    throw new HttpError(400, `invalid_${field}`, `${field} is required`);
  }
  return text;
}

function optionalText(value, field, maximumLength) {
  if (value === undefined || value === null || value === "") {
    return undefined;
  }
  if (typeof value !== "string") {
    throw new HttpError(400, `invalid_${field}`, `${field} must be text`);
  }
  const text = value.trim();
  if (text.length > maximumLength) {
    throw new HttpError(400, `invalid_${field}`, `${field} is too long`);
  }
  return text;
}

function sendJson(response, status, payload) {
  const body = Buffer.from(stringifyJson(payload));
  response.writeHead(status, {
    "Content-Length": body.length,
    "Content-Type": "application/json; charset=utf-8",
  });
  response.end(body);
}

function writeSse(response, eventName, payload) {
  return response.write(`event: ${eventName}\ndata: ${stringifyJson(payload)}\n\n`);
}

function stringifyJson(value) {
  const ancestors = [];
  return JSON.stringify(value, function replaceNonJson(_key, item) {
    if (typeof item === "bigint") {
      return item.toString();
    }
    if (!item || typeof item !== "object") {
      return item;
    }
    while (ancestors.length > 0 && ancestors.at(-1) !== this) {
      ancestors.pop();
    }
    if (ancestors.includes(item)) {
      return "[Circular]";
    }
    ancestors.push(item);
    return item;
  });
}

function toPlainJson(value, seen = new WeakSet(), depth = 0) {
  if (value === null || typeof value === "string" || typeof value === "boolean") {
    return value;
  }
  if (typeof value === "number") {
    return Number.isFinite(value) ? value : String(value);
  }
  if (typeof value === "bigint") {
    return value.toString();
  }
  if (typeof value !== "object") {
    return undefined;
  }
  if (depth > 64) {
    return "[Maximum depth]";
  }
  if (seen.has(value)) {
    return "[Circular]";
  }
  seen.add(value);

  if (Array.isArray(value)) {
    return value.map((item) => toPlainJson(item, seen, depth + 1));
  }

  const plain = {};
  for (const key of Object.keys(value)) {
    try {
      const item = toPlainJson(value[key], seen, depth + 1);
      if (item !== undefined) {
        plain[key] = item;
      }
    } catch {
      plain[key] = "[Unavailable]";
    }
  }
  return plain;
}
