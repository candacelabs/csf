import assert from "node:assert/strict";
import test from "node:test";

import { registerPairExtension } from "../runtime.mjs";

test("the extension registers /pair and forwards startup events without a race", async () => {
  const logs = [];
  const history = [{ id: "history", timestamp: "1", type: "user.message", data: {} }];
  const session = {
    sessionId: "session-1",
    async getEvents() { return history; },
    async log(message) { logs.push(message); },
    send() {},
  };
  const calls = { merged: [], ingested: [], started: 0, stopped: 0 };
  const share = {
    merge(events) { calls.merged.push(...events); },
    ingest(event) { calls.ingested.push(event); },
    async start() {
      calls.started += 1;
      return {
        link: "http://127.0.0.1:1234/",
        connectedClients: 0,
      };
    },
    async stop() { calls.stopped += 1; },
    status() {
      return {
        running: calls.started > calls.stopped,
        link: "http://127.0.0.1:1234/",
        connectedClients: 2,
      };
    },
  };

  let joinConfig;
  const joinSession = async (config) => {
    joinConfig = config;
    config.onEvent({ id: "startup", timestamp: "2", type: "session.start", data: {} });
    return session;
  };

  const extension = await registerPairExtension(joinSession, {
    environment: {},
    createShare: () => share,
  });
  assert.equal(extension.session, session);
  assert.equal(joinConfig.streaming, true);
  assert.equal(joinConfig.commands.length, 1);
  assert.equal(joinConfig.commands[0].name, "pair");
  assert.deepEqual(calls.merged, history);
  assert.deepEqual(calls.ingested.map((event) => event.id), ["startup"]);

  await joinConfig.commands[0].handler({ args: "start" });
  await joinConfig.commands[0].handler({ args: "status" });
  await joinConfig.commands[0].handler({ args: "stop" });
  assert.equal(calls.started, 1);
  assert.equal(calls.stopped, 1);
  assert.match(logs[0], /http:\/\/127\.0\.0\.1:1234\//);
  assert.match(logs[1], /2 connected/);
  assert.match(logs[2], /stopped/);
});

test("the extension hydrates history from CLI hosts that expose getMessages", async () => {
  const history = [{ id: "message-history", timestamp: "1", type: "user.message", data: {} }];
  const session = {
    sessionId: "session-with-getMessages",
    async getMessages() { return history; },
    async log() {},
    send() {},
  };
  const merged = [];
  const share = {
    merge(events) { merged.push(...events); },
    ingest() {},
    async start() { return { link: "http://127.0.0.1/", connectedClients: 0 }; },
    async stop() {},
    status() { return { running: false }; },
  };

  await registerPairExtension(async () => session, {
    environment: {},
    createShare: () => share,
  });
  assert.deepEqual(merged, history);
});
