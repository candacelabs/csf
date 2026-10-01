/**
 * A grow-only set for durable Copilot session events.
 *
 * Copilot events already carry stable UUIDs and causal parent IDs. Replicas
 * converge by taking the union of events keyed by UUID; ordering is only a
 * projection for display and is not part of the merge rule.
 */
export class GrowOnlyEventSet {
  #events = new Map();

  get size() {
    return this.#events.size;
  }

  add(event) {
    if (!event || typeof event !== "object" || typeof event.id !== "string") {
      return false;
    }
    if (event.ephemeral === true || this.#events.has(event.id)) {
      return false;
    }
    this.#events.set(event.id, event);
    return true;
  }

  merge(events) {
    const added = [];
    for (const event of events ?? []) {
      if (this.add(event)) {
        added.push(event);
      }
    }
    return added;
  }

  values() {
    return [...this.#events.values()].sort(compareEvents);
  }
}

function compareEvents(left, right) {
  const timestampOrder = String(left.timestamp ?? "").localeCompare(
    String(right.timestamp ?? ""),
  );
  if (timestampOrder !== 0) {
    return timestampOrder;
  }
  return left.id.localeCompare(right.id);
}
