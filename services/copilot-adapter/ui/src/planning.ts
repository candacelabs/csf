export const planningLanes = { backlog: "To do", active: "In progress", review: "Review", done: "Done" } as const;
export type PlanningLane = keyof typeof planningLanes;
export type PlanningBoard = Record<PlanningLane, string[]>;
export const planningLaneIDs = Object.keys(planningLanes) as PlanningLane[];
export const emptyPlan: PlanningBoard = { backlog: [], active: [], review: [], done: [] };

// Browser storage is untrusted and may name deleted sessions. New sessions join
// To do; an observed runtime state never decides a planning column.
export function reconcilePlan(value: unknown, ids: string[]): PlanningBoard {
  const result: PlanningBoard = { backlog: [], active: [], review: [], done: [] };
  const remaining = new Set(ids);
  for (const lane of planningLaneIDs) {
    const entries = value && typeof value === "object" && lane in value ? (value as Record<string, unknown>)[lane] : [];
    if (!Array.isArray(entries)) continue;
    for (const id of entries) {
      if (typeof id === "string" && remaining.delete(id)) result[lane].push(id);
    }
  }
  result.backlog.push(...remaining);
  return result;
}

export function movePlan(board: PlanningBoard, id: string, target: string): PlanningBoard {
  const source = planningLaneIDs.find((lane) => board[lane].includes(id));
  const destination = planningLaneIDs.find((lane) => lane === target || board[lane].includes(target));
  if (!source || !destination || id === target) return board;
  const next = { ...board, [source]: board[source].filter((entry) => entry !== id) };
  const entries = [...next[destination]];
  const position = board[destination].indexOf(target);
  entries.splice(position < 0 ? entries.length : position, 0, id);
  return { ...next, [destination]: entries };
}
