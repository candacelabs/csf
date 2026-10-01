import assert from "node:assert/strict";
import test from "node:test";

import { GROUP_CHAT_PREAMBLE, PairShare, defaultPairOptions } from "../pair-server.mjs";

function sdkEvent(id, type, data = {}, ephemeral = false) {
  return {
    id,
    parentId: null,
    timestamp: `2026-08-04T00:00:${id.slice(-2)}.000Z`,
    type,
    data,
    ...(ephemeral ? { ephemeral: true } : {}),
  };
}

class FakeSession {
  sessionId = "11111111-2222-4333-8444-555555555555";
  history = [sdkEvent("event-01", "user.message", { content: "existing" })];
  sent = [];
  aborted = 0;
  models = [];
  permissionDecisions = [];

  rpc = {
    permissions: {
      handlePendingPermissionRequest: async (request) => {
        this.permissionDecisions.push(request);
        return { handled: true };
      },
    },
    model: {
      list: async () => ({
        list: [
          { id: "gpt-5.4", name: "GPT-5.4", capabilities: {} },
          { id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6", capabilities: {} },
          { name: "malformed-entry-without-id" },
        ],
      }),
      getCurrent: async () => ({ modelId: "gpt-5.4" }),
    },
  };

  async getMessages() {
    return this.history;
  }

  async send(options) {
    this.sent.push(options);
    return `message-${this.sent.length}`;
  }

  async abort() {
    this.aborted += 1;
  }

  async setModel(model) {
    this.models.push(model);
  }
}

test("default options share the session with the network", () => {
  const options = defaultPairOptions({});
  assert.equal(options.listenHost, "0.0.0.0");
  assert.equal(options.port, 0);
  assert.notEqual(options.publicHost, "0.0.0.0");
  assert.ok(options.publicHost.length > 0);
});

test("environment variables override the listen and public hosts", () => {
  const options = defaultPairOptions({
    COPILOT_PAIR_LISTEN: "127.0.0.1",
    COPILOT_PAIR_PORT: "7331",
    COPILOT_PAIR_PUBLIC_URL: "https://pair.example.test",
  });
  assert.equal(options.listenHost, "127.0.0.1");
  assert.equal(options.publicHost, "127.0.0.1");
  assert.equal(options.port, 7331);
  assert.equal(options.publicUrl, "https://pair.example.test");
});

async function startShare(t) {
  const session = new FakeSession();
  const share = new PairShare(session, {
    listenHost: "127.0.0.1",
    publicHost: "127.0.0.1",
    port: 0,
    publicUrl: undefined,
  });
  const status = await share.start();
  t.after(() => share.stop());
  return { session, share, baseUrl: status.link };
}

async function postAction(baseUrl, action) {
  const response = await fetch(new URL("api/actions", baseUrl), {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(action),
  });
  return { response, payload: await response.json() };
}

test("the share URL exposes the browser and API without accounts or auth", async (t) => {
  const { baseUrl } = await startShare(t);
  assert.equal(new URL(baseUrl).hash, "");

  const page = await fetch(baseUrl);
  assert.equal(page.status, 200);
  assert.match(await page.text(), /Copilot Pair/);

  const metadata = await fetch(new URL("api/meta", baseUrl));
  assert.equal(metadata.status, 200);
  assert.equal((await metadata.json()).sessionId, "11111111-2222-4333-8444-555555555555");
});

test("the model catalog lists usable models with the current selection", async (t) => {
  const { baseUrl } = await startShare(t);
  const response = await fetch(new URL("api/models", baseUrl));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    models: [
      { id: "gpt-5.4", name: "GPT-5.4" },
      { id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6" },
    ],
    current: "gpt-5.4",
  });
});

test("CLI hosts without session.model.list use the client-level models.list RPC", async (t) => {
  const session = new FakeSession();
  session.rpc.model = {
    getCurrent: async () => ({ modelId: "claude-sonnet-4.6" }),
  };
  const requests = [];
  session.connection = {
    sendRequest: async (method, params) => {
      requests.push([method, params]);
      return {
        models: [
          { id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6", capabilities: {} },
        ],
      };
    },
  };

  const share = new PairShare(session, {
    listenHost: "127.0.0.1",
    publicHost: "127.0.0.1",
    port: 0,
    publicUrl: undefined,
  });
  const status = await share.start();
  t.after(() => share.stop());

  const response = await fetch(new URL("api/models", status.link));
  assert.equal(response.status, 200);
  assert.deepEqual(await response.json(), {
    models: [{ id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6" }],
    current: "claude-sonnet-4.6",
  });
  assert.deepEqual(requests, [["models.list", {}]]);
});

test("hosts without model RPCs degrade to an empty catalog", async (t) => {
  const withoutRpc = new FakeSession();
  delete withoutRpc.rpc.model;
  const failing = new FakeSession();
  failing.rpc.model = {
    list: async () => {
      throw new Error("unsupported");
    },
  };

  for (const session of [withoutRpc, failing]) {
    const share = new PairShare(session, {
      listenHost: "127.0.0.1",
      publicHost: "127.0.0.1",
      port: 0,
      publicUrl: undefined,
    });
    const status = await share.start();
    t.after(() => share.stop());
    const response = await fetch(new URL("api/models", status.link));
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { models: [] });
  }
});

test("any connected peer can prompt, steer, abort, and change model", async (t) => {
  const { baseUrl, session } = await startShare(t);

  const prompt = {
    id: "action-prompt-1",
    type: "prompt",
    actor: "Billy",
    prompt: "Fix the test",
    delivery: "enqueue",
  };
  const first = await postAction(baseUrl, prompt);
  const duplicate = await postAction(baseUrl, prompt);
  assert.equal(first.response.status, 200);
  assert.deepEqual(duplicate.payload, first.payload);
  assert.deepEqual(session.sent, [{
    prompt: `${GROUP_CHAT_PREAMBLE}\n\nBilly: Fix the test`,
    mode: "enqueue",
  }]);

  await postAction(baseUrl, {
    id: "action-steer-1",
    type: "prompt",
    actor: "Billy",
    prompt: "Use the smaller approach",
    delivery: "immediate",
  });
  assert.equal(session.sent[1].mode, "immediate");
  assert.equal(session.sent[1].prompt, "Billy: Use the smaller approach");

  await postAction(baseUrl, { id: "action-abort-1", type: "abort" });
  await postAction(baseUrl, { id: "action-model-1", type: "model", model: "gpt-5.4" });
  assert.equal(session.aborted, 1);
  assert.deepEqual(session.models, ["gpt-5.4"]);
});

test("the group-chat context is seeded exactly once, on the first shared prompt", async (t) => {
  const { baseUrl, session } = await startShare(t);

  await postAction(baseUrl, {
    id: "action-seed-1", type: "prompt", actor: "Ada", prompt: "First question",
  });
  await postAction(baseUrl, {
    id: "action-seed-2", type: "prompt", actor: "Grace", prompt: "Second question",
  });

  assert.equal(session.sent[0].prompt, `${GROUP_CHAT_PREAMBLE}\n\nAda: First question`);
  assert.equal(session.sent[1].prompt, "Grace: Second question");
  assert.ok(GROUP_CHAT_PREAMBLE.startsWith("[Copilot Pair]"));
  assert.ok(!GROUP_CHAT_PREAMBLE.includes("\n"));
});

test("the browser assets include the rich-text renderer module", async (t) => {
  const { baseUrl } = await startShare(t);
  const module = await fetch(new URL("renderer.js", baseUrl));
  assert.equal(module.status, 200);
  assert.match(module.headers.get("content-type"), /text\/javascript/);
  assert.match(await module.text(), /renderRichText/);
});

test("any connected peer can resolve Copilot permission requests", async (t) => {
  const { baseUrl, session } = await startShare(t);

  await postAction(baseUrl, {
    id: "action-permission-1",
    type: "permission",
    requestId: "permission-1",
    decision: "approve",
  });
  await postAction(baseUrl, {
    id: "action-permission-2",
    type: "permission",
    requestId: "permission-2",
    decision: "deny",
    feedback: "Use a read-only command instead",
  });
  assert.deepEqual(session.permissionDecisions, [
    {
      requestId: "permission-1",
      result: { kind: "approved" },
    },
    {
      requestId: "permission-2",
      result: {
        kind: "denied-interactively-by-user",
        feedback: "Use a read-only command instead",
      },
    },
  ]);
});

test("SSE snapshots durable history and streams new durable and transient events", async (t) => {
  const { baseUrl, share } = await startShare(t);
  const response = await fetch(new URL("api/events", baseUrl));
  assert.equal(response.status, 200);
  const events = new SseReader(response.body.getReader());

  const snapshot = await events.next("snapshot");
  assert.deepEqual(snapshot.data.durableEvents.map((event) => event.id), ["event-01"]);
  assert.equal("transientEvents" in snapshot.data, false);

  const durable = sdkEvent("event-02", "assistant.message", { content: "done" });
  const delta = sdkEvent(
    "event-03",
    "assistant.message_delta",
    { deltaContent: "working" },
    true,
  );
  share.ingest(delta);
  share.ingest(durable);

  const first = await events.next("session-event");
  const second = await events.next("session-event");
  assert.equal(first.data.event.id, "event-03");
  assert.equal(first.data.durable, false);
  assert.equal(second.data.event.id, "event-02");
  assert.equal(second.data.durable, true);
  await events.cancel();
});

test("late subscribers reconnect from durable history without stale transients", async (t) => {
  const { baseUrl, share } = await startShare(t);
  share.ingest(sdkEvent(
    "event-03",
    "assistant.message_delta",
    { deltaContent: "working" },
    true,
  ));
  share.ingest(sdkEvent("event-02", "assistant.message", { content: "done" }));

  const response = await fetch(new URL("api/events", baseUrl));
  const events = new SseReader(response.body.getReader());
  const snapshot = await events.next("snapshot");
  assert.deepEqual(
    snapshot.data.durableEvents.map((event) => event.id),
    ["event-01", "event-02"],
  );
  assert.equal("transientEvents" in snapshot.data, false);
  await events.cancel();
});

test("two connected peers receive the identical new durable event", async (t) => {
  const { baseUrl, share } = await startShare(t);
  const firstResponse = await fetch(new URL("api/events", baseUrl));
  const secondResponse = await fetch(new URL("api/events", baseUrl));
  const firstEvents = new SseReader(firstResponse.body.getReader());
  const secondEvents = new SseReader(secondResponse.body.getReader());
  await firstEvents.next("snapshot");
  await secondEvents.next("snapshot");

  const durable = sdkEvent("event-02", "assistant.message", { content: "same event" });
  share.ingest(durable);

  const first = await firstEvents.next("session-event");
  const second = await secondEvents.next("session-event");
  assert.deepEqual(first.data, second.data);
  assert.deepEqual(first.data, { durable: true, event: durable });

  await firstEvents.cancel();
  await secondEvents.cancel();
});

test("browser CRDT replicas merge by event id", async (t) => {
  const { baseUrl, share } = await startShare(t);
  const peerEvent = sdkEvent("peer-event-01", "assistant.message", { content: "from peer" });
  const merge = async () => {
    const response = await fetch(new URL("api/events/merge", baseUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ events: [peerEvent] }),
    });
    return response.json();
  };

  assert.deepEqual(await merge(), { added: 1, eventCount: 2 });
  assert.deepEqual(await merge(), { added: 0, eventCount: 2 });
  assert.equal(share.eventCount, 2);
});

test("real SDK values such as bigint do not break the live stream", async (t) => {
  const { baseUrl, share } = await startShare(t);
  const data = { inputTokens: 12n };
  data.self = data;
  share.ingest(sdkEvent("event-bigint", "assistant.usage", data));

  const response = await fetch(new URL("api/events", baseUrl));
  const events = new SseReader(response.body.getReader());
  const snapshot = await events.next("snapshot");
  const usage = snapshot.data.durableEvents.find((event) => event.id === "event-bigint");
  assert.equal(usage.data.inputTokens, "12");
  assert.equal(usage.data.self, "[Circular]");
  await events.cancel();
});

test("large session snapshots are buffered instead of disconnecting the peer", async (t) => {
  const { baseUrl, share } = await startShare(t);
  const content = "session context ".repeat(20_000);
  share.ingest(sdkEvent("event-large", "system.message", { content }));

  const response = await fetch(new URL("api/events", baseUrl));
  const events = new SseReader(response.body.getReader());
  const snapshot = await events.next("snapshot");
  const system = snapshot.data.durableEvents.find((event) => event.id === "event-large");
  assert.equal(system.data.content, content);
  await events.cancel();
});

test("invalid actions fail without invoking the session", async (t) => {
  const { baseUrl, session } = await startShare(t);
  const { response, payload } = await postAction(baseUrl, {
    id: "action-bad-1",
    type: "launch-missiles",
  });
  assert.equal(response.status, 400);
  assert.equal(payload.error.code, "invalid_action");
  assert.deepEqual(session.sent, []);
});

class SseReader {
  constructor(reader) {
    this.reader = reader;
    this.buffer = "";
    this.decoder = new TextDecoder();
  }

  async next(expectedName) {
    for (;;) {
      const boundary = this.buffer.indexOf("\n\n");
      if (boundary >= 0) {
        const block = this.buffer.slice(0, boundary);
        this.buffer = this.buffer.slice(boundary + 2);
        const parsed = parseSse(block);
        if (parsed?.event === expectedName) return parsed;
        continue;
      }
      const { value, done } = await this.reader.read();
      if (done) throw new Error(`SSE closed before ${expectedName}`);
      this.buffer += this.decoder.decode(value, { stream: true });
    }
  }

  async cancel() {
    await this.reader.cancel();
  }
}

function parseSse(block) {
  if (block.startsWith(":")) return undefined;
  let event;
  let data;
  for (const line of block.split("\n")) {
    if (line.startsWith("event: ")) event = line.slice(7);
    if (line.startsWith("data: ")) data = JSON.parse(line.slice(6));
  }
  return { event, data };
}
