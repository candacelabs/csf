import Sortable from "sortablejs";
import type { SortableEvent } from "sortablejs";

export const kanbanViewURL = "/v1/kanban/view";
export const kanbanLiveURL = "/v1/kanban/live";
const runtimeURL = `${kanbanLiveURL}/gotth-live.min.js`;
const columnSelector = "[data-kanban-column]";
const moveFormSelector = "form[data-kanban-move]";
const unassignedStatus = "unassigned";
const reasonStatuses = new Set(["WORK_STATUS_BLOCKED", "WORK_STATUS_OPERATOR"]);

type GotthRuntime = { start: (url: string, root?: HTMLElement) => void; stop: () => void; status: () => string };
declare global { interface Window { gotthLive?: GotthRuntime } }

let loadingRuntime: Promise<GotthRuntime> | undefined;
export function loadGotthRuntime(): Promise<GotthRuntime> {
  if (window.gotthLive) return Promise.resolve(window.gotthLive);
  if (loadingRuntime) return loadingRuntime;
  loadingRuntime = new Promise<GotthRuntime>((resolve, reject) => {
    const script = document.createElement("script");
    script.src = runtimeURL;
    script.onload = () => {
      script.remove();
      if (window.gotthLive) resolve(window.gotthLive);
      else reject(new Error("The live board runtime did not load."));
    };
    script.onerror = () => { script.remove(); reject(new Error("The live board runtime could not be downloaded.")); };
    document.head.append(script);
  }).catch((error: unknown) => { loadingRuntime = undefined; throw error; });
  return loadingRuntime;
}

// This imperative bridge is the sole temporary DOM owner during a drag. It
// restores the observed placement before submitting; only gotth confirms moves.
export function attachKanbanDragging(host: HTMLElement, announce: (message: string) => void, live: () => boolean): () => void {
  const sortables = new Map<HTMLElement, Sortable>();
  let observed: { card: HTMLElement; parent: HTMLElement; next: Element | null; checkpoint: string } | undefined;
  const checkpoint = (card: HTMLElement) => card.querySelector<HTMLInputElement>(`${moveFormSelector} [name="expected_checkpoint"]`)?.value ?? "";
  const restore = () => {
    if (!observed || !host.contains(observed.parent) || !host.contains(observed.card)) return;
    const { parent, card, next } = observed;
    parent.insertBefore(card, next?.parentElement === parent ? next : null);
  };
  const end = (event: SortableEvent) => {
    const target = event.to.dataset.kanbanColumn;
    const source = observed;
    restore();
    observed = undefined;
    if (!source || source.card !== event.item || event.from === event.to) return;
    if (!host.contains(event.item) || checkpoint(event.item) !== source.checkpoint) {
      announce("This task changed during the drag. Review its current checkpoint and try again.");
      return;
    }
    const form = event.item.querySelector<HTMLFormElement>(moveFormSelector);
    const select = form?.elements.namedItem("status");
    if (!form || !(select instanceof HTMLSelectElement) || !target || target === unassignedStatus) {
      announce("Link a task with a current checkpoint before moving it.");
      return;
    }
    if (!live()) { announce("The board is reconnecting. Wait for its live connection before moving a task."); return; }
    select.value = target;
    if (select.value !== target) { announce("That task status is unavailable. Refresh the task checkpoint and try again."); return; }
    const details = event.item.querySelector<HTMLDetailsElement>("[data-kanban-move-details]");
    if (details) details.open = true;
    const reason = form.elements.namedItem("reason");
    if (reason instanceof HTMLInputElement && reasonStatuses.has(target) && reason.value.trim() === "") {
      reason.focus();
      announce("Enter a reason or blocker, then choose Move task to confirm this move.");
      return;
    }
    if (!form.reportValidity()) { announce("Complete the move form to confirm this move."); return; }
    form.requestSubmit();
    announce("Move requested. The card stays in its current column until its checkpoint is saved.");
  };
  const reconcile = () => {
    for (const [element, sortable] of sortables) {
      if (!host.contains(element)) { sortable.destroy(); sortables.delete(element); }
    }
    for (const element of host.querySelectorAll<HTMLElement>(columnSelector)) {
      if (sortables.has(element)) continue;
      sortables.set(element, Sortable.create(element, {
        group: "kanban-tasks", draggable: "[data-kanban-card]", handle: "[data-kanban-drag]", sort: false,
        filter: (_event, target) => {
          const card = target.closest<HTMLElement>("[data-kanban-card]");
          const denied = !card?.querySelector(moveFormSelector) || !live();
          if (denied) announce(!live() ? "Wait for the live board connection before moving a task." : "Link a task with a current checkpoint before moving it.");
          return denied;
        },
        onStart: (event) => {
          observed = { card: event.item, parent: event.from, next: event.item.nextElementSibling, checkpoint: checkpoint(event.item) };
        },
        onMove: (event) => {
          if (event.to.dataset.kanbanColumn === unassignedStatus) {
            announce("Choose a task status column. Unassigned contains sessions without a ready task link.");
            return false;
          }
          return true;
        },
        onEnd: end,
      }));
    }
  };
  const observer = new MutationObserver(reconcile);
  observer.observe(host, { childList: true, subtree: true });
  reconcile();
  return () => {
    observer.disconnect();
    restore();
    observed = undefined;
    for (const sortable of sortables.values()) sortable.destroy();
    sortables.clear();
  };
}
