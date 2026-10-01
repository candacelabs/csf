import assert from "node:assert/strict";
import test from "node:test";

import { flush, loadApp } from "./browser-env.mjs";

const CATALOG = {
  models: [
    { id: "gpt-5.4", name: "GPT-5.4" },
    { id: "claude-sonnet-4.6", name: "Claude Sonnet 4.6" },
  ],
  current: "claude-sonnet-4.6",
};

function lastAction(app) {
  return app.fetchCalls.filter((call) => call.url === "/api/actions").at(-1);
}

test("actions send valid ids on plain-http origins without crypto.randomUUID", async () => {
  const app = await loadApp({ catalog: CATALOG });
  // Regression guard: the share link is an insecure context, so this API must
  // not exist in the test environment — and the page must still work.
  assert.equal(globalThis.crypto.randomUUID, undefined);

  app.document.querySelector("#actor").value = "Billy";
  app.document.querySelector("#prompt").value = "Fix the bug";
  app.document.querySelector("#send").dispatch("click");
  await flush();

  const first = lastAction(app);
  assert.ok(first, "the action request was sent");
  assert.equal(first.body.type, "prompt");
  assert.equal(first.body.prompt, "Fix the bug");
  assert.match(first.body.id, /^[0-9a-f]{32}$/);
  assert.match(first.body.id, /^[A-Za-z0-9._:-]{8,128}$/);
  assert.equal(app.document.querySelector("#action-status").textContent, "Done.");

  app.document.querySelector("#prompt").value = "Second prompt";
  app.document.querySelector("#send").dispatch("click");
  await flush();
  assert.notEqual(lastAction(app).body.id, first.body.id);
});

test("the model dropdown lists the catalog and preselects the current model", async () => {
  const app = await loadApp({ catalog: CATALOG });
  const select = app.document.querySelector("#model");
  assert.equal(select.tagName, "SELECT");
  assert.deepEqual(
    select.options.map((option) => [option.value, option.textContent]),
    [["gpt-5.4", "GPT-5.4"], ["claude-sonnet-4.6", "Claude Sonnet 4.6"]],
  );
  assert.equal(select.value, "claude-sonnet-4.6");

  select.value = "gpt-5.4";
  app.document.querySelector("#change-model").dispatch("click");
  await flush();
  const action = lastAction(app);
  assert.equal(action.body.type, "model");
  assert.equal(action.body.model, "gpt-5.4");
});

test("live model changes update the dropdown and add unknown models", async () => {
  const app = await loadApp({ catalog: CATALOG });
  app.source.emit("session-event", {
    durable: true,
    event: {
      id: "event-model-change",
      timestamp: "2026-08-04T00:00:09.000Z",
      type: "session.model_change",
      data: { newModel: "gpt-6", previousModel: "claude-sonnet-4.6" },
    },
  });
  const select = app.document.querySelector("#model");
  assert.equal(select.value, "gpt-6");
  assert.ok(select.options.some((option) => option.value === "gpt-6"));
});

test("an empty or failing model catalog falls back to a free-text input", async () => {
  for (const options of [{ catalog: { models: [] } }, { catalogStatus: 500 }]) {
    const app = await loadApp(options);
    const input = app.document.querySelector("#model").replacedWith;
    assert.equal(input?.tagName, "INPUT");
    assert.equal(input.placeholder, "model id");

    input.value = "custom-model";
    app.document.querySelector("#change-model").dispatch("click");
    await flush();
    const action = lastAction(app);
    assert.equal(action.body.type, "model");
    assert.equal(action.body.model, "custom-model");
  }
});

test("snapshots render only visible transcript events and persist a replica", async () => {
  const app = await loadApp({ catalog: CATALOG });
  app.source.emit("snapshot", {
    sessionId: "session-1",
    durableEvents: [
      {
        id: "event-01",
        timestamp: "2026-08-04T00:00:01.000Z",
        type: "user.message",
        data: { content: "hello" },
      },
      {
        id: "event-02",
        timestamp: "2026-08-04T00:00:02.000Z",
        type: "assistant.message",
        data: { content: "hi" },
      },
      {
        id: "event-03",
        timestamp: "2026-08-04T00:00:03.000Z",
        type: "session.idle",
        data: {},
      },
    ],
  });

  const transcript = app.document.querySelector("#transcript");
  assert.equal(transcript.children.length, 2);
  assert.equal(app.document.querySelector("#event-count").textContent, 3);
  const replica = JSON.parse(app.storage.get("candace-pair-events:session-1"));
  assert.equal(replica.length, 3);
});

test("permission requests render cards whose buttons resolve the request", async () => {
  const app = await loadApp({ catalog: CATALOG });
  app.source.emit("session-event", {
    durable: true,
    event: {
      id: "event-permission",
      timestamp: "2026-08-04T00:00:04.000Z",
      type: "permission.requested",
      data: {
        requestId: "permission-1",
        permissionRequest: { fullCommandText: "rm -rf build/" },
      },
    },
  });

  const pending = app.document.querySelector("#pending");
  assert.equal(pending.children.length, 1);
  const card = pending.children[0];
  assert.equal(card.querySelector("pre").textContent, "rm -rf build/");

  const buttons = card.children.at(-1).children;
  assert.deepEqual(buttons.map((button) => button.textContent), ["Deny", "Approve once"]);
  buttons[1].dispatch("click");
  await flush();
  const action = lastAction(app);
  assert.equal(action.body.type, "permission");
  assert.equal(action.body.requestId, "permission-1");
  assert.equal(action.body.decision, "approve");
});
