import assert from "node:assert/strict";
import test from "node:test";

import { GrowOnlyEventSet } from "../event-set.mjs";

const first = {
  id: "00000000-0000-4000-8000-000000000001",
  parentId: null,
  timestamp: "2026-08-04T00:00:01.000Z",
  type: "user.message",
  data: { content: "hello" },
};
const second = {
  id: "00000000-0000-4000-8000-000000000002",
  parentId: first.id,
  timestamp: "2026-08-04T00:00:02.000Z",
  type: "assistant.message",
  data: { content: "hi" },
};

test("grow-only replicas converge by event-id union", () => {
  const left = new GrowOnlyEventSet();
  const right = new GrowOnlyEventSet();

  left.add(first);
  right.add(second);
  left.merge(right.values());
  right.merge(left.values());

  assert.deepEqual(left.values(), [first, second]);
  assert.deepEqual(right.values(), [first, second]);
  assert.equal(left.size, 2);
  assert.equal(left.add(first), false);
});

test("ephemeral streaming events are not retained in the CRDT", () => {
  const events = new GrowOnlyEventSet();
  const delta = {
    ...second,
    id: "00000000-0000-4000-8000-000000000003",
    type: "assistant.message_delta",
    ephemeral: true,
  };

  assert.equal(events.add(delta), false);
  assert.deepEqual(events.values(), []);
});
