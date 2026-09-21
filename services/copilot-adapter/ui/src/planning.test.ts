import { describe, expect, it } from "vitest";
import { movePlan, reconcilePlan } from "./planning";

describe("browser-local planning", () => {
  it("recovers invalid storage and reconciles duplicates, missing sessions and new sessions", () => {
    expect(reconcilePlan({ backlog: ["a", "a", 1, "gone"], review: ["b"], done: "wrong" }, ["a", "b", "c"])).toEqual({ backlog: ["a", "c"], active: [], review: ["b"], done: [] });
    expect(reconcilePlan(null, ["a"]).backlog).toEqual(["a"]);
  });
  it("moves into empty lanes and reorders without duplicating or dropping hidden cards", () => {
    const initial = reconcilePlan(null, ["a", "b", "c"]);
    const ordered = movePlan(initial, "a", "c");
    expect(ordered.backlog).toEqual(["b", "c", "a"]);
    const moved = movePlan(ordered, "c", "review");
    expect(moved).toEqual({ backlog: ["b", "a"], active: [], review: ["c"], done: [] });
    expect(movePlan(moved, "gone", "done")).toBe(moved);
    expect(movePlan(moved, "a", "unknown")).toBe(moved);
  });
});
