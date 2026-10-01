import Sortable from "sortablejs";
import type { SortableEvent } from "sortablejs";
import { afterEach, describe, expect, it, vi } from "vitest";
import { attachKanbanDragging } from "./kanbanIsland";

const html = `<div data-kanban-column="WORK_STATUS_QUEUED"><article data-kanban-card><button data-kanban-drag>Drag</button><details data-kanban-move-details><form data-kanban-move><input name="expected_checkpoint" value="checkpoint-1"><select name="status"><option value="WORK_STATUS_QUEUED">Queued</option><option value="WORK_STATUS_ACTIVE">Active</option><option value="WORK_STATUS_BLOCKED">Blocked</option></select><input name="next_action" value="Run tests" required><input name="reason"></form></details></article></div><div data-kanban-column="WORK_STATUS_ACTIVE"></div><div data-kanban-column="WORK_STATUS_BLOCKED"></div>`;
function fixture() {
  const host = document.createElement("div");
  host.innerHTML = html;
  document.body.append(host);
  const announce = vi.fn();
  const dispose = attachKanbanDragging(host, announce, () => true);
  const columns = host.querySelectorAll<HTMLElement>("[data-kanban-column]");
  const card = host.querySelector<HTMLElement>("article")!;
  const form = card.querySelector<HTMLFormElement>("form")!;
  const requestSubmit = vi.spyOn(form, "requestSubmit").mockImplementation(() => undefined);
  const sortable = Sortable.get(columns[0])!;
  const move = (index: number, during?: () => void) => {
    const event = { item: card, from: columns[0], to: columns[index] } as SortableEvent;
    sortable.option("onStart")!(event);
    columns[index].append(card);
    during?.();
    sortable.option("onEnd")!(event);
  };
  return { host, announce, dispose, columns, card, form, requestSubmit, move };
}
afterEach(() => { document.body.replaceChildren(); vi.restoreAllMocks(); });

describe("authoritative Kanban dragging", () => {
  it("restores placement then submits the existing checkpoint form for the destination status", () => {
    const f = fixture();
    f.move(1);
    expect(f.card.parentElement).toBe(f.columns[0]);
    expect((f.form.elements.namedItem("status") as HTMLSelectElement).value).toBe("WORK_STATUS_ACTIVE");
    expect((f.form.elements.namedItem("expected_checkpoint") as HTMLInputElement).value).toBe("checkpoint-1");
    expect(f.requestSubmit).toHaveBeenCalledTimes(1);
    expect(f.announce).toHaveBeenCalledWith(expect.stringContaining("until its checkpoint is saved"));
    f.dispose();
    expect(Sortable.get(f.columns[0])).toBeNull();
  });
  it("opens the keyboard form and asks for a blocker instead of silently submitting an incomplete move", () => {
    const f = fixture();
    f.move(2);
    expect(f.requestSubmit).not.toHaveBeenCalled();
    expect(f.card.querySelector("details")?.open).toBe(true);
    expect(document.activeElement).toBe(f.form.elements.namedItem("reason"));
    expect(f.announce).toHaveBeenCalledWith(expect.stringContaining("Enter a reason"));
    f.dispose();
  });
  it("rejects a drag whose checkpoint changed during the gesture", () => {
    const f = fixture();
    f.move(1, () => { (f.form.elements.namedItem("expected_checkpoint") as HTMLInputElement).value = "checkpoint-2"; });
    expect(f.requestSubmit).not.toHaveBeenCalled();
    expect(f.announce).toHaveBeenCalledWith(expect.stringContaining("changed during the drag"));
    f.dispose();
  });
  it("attaches newly rendered columns and destroys removed columns without polling", async () => {
    const f = fixture();
    const added = document.createElement("div");
    added.dataset.kanbanColumn = "WORK_STATUS_DONE";
    f.host.append(added);
    f.columns[1].remove();
    await new Promise<void>((resolve) => queueMicrotask(resolve));
    expect(Sortable.get(added)).toBeTruthy();
    expect(Sortable.get(f.columns[1])).toBeNull();
    f.dispose();
    expect(Sortable.get(added)).toBeNull();
  });
});
