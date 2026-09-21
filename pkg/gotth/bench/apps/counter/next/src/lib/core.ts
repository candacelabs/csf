/*
 * The counter's derived display, as pure functions.
 *
 * Every string in this file is a transcription of examples/counter/counter.go,
 * not a re-invention of it. E1 ("same product surface") and E3 ("same data")
 * are checked by comparing rendered DOM, so "even"/"odd", "negative"/"zero"/
 * "low"/"high", "just now", "1 tab" and "nobody yet" are the wire format of
 * the equivalence claim. If the Go side changes one of them, this file is
 * wrong and the conformance test (§2.5) is what should say so.
 *
 * Go source of truth, for the reader diffing the two:
 *   Parity()    counter.go:180
 *   Band()      counter.go:189
 *   AgeLabel()  counter.go:205
 *   Author()    counter.go:223
 *   TabLabel()  counter.go:235
 */

/**
 * One session's view of the shared counter, exactly the fields the Go State
 * carries that are not per-viewer derivations.
 *
 * `value` is a JS number where Go has an int64. The counter moves by ±1, ±10
 * or to 0, so it cannot leave the ±2^53 range a double represents exactly
 * inside any run this benchmark performs; using bigint here would change the
 * JSON wire shape for no measurable gain and is deliberately not done.
 *
 * `nowMs` is the server's clock at the moment the snapshot was produced. The
 * relative timestamp F-CTR-7 asks for is therefore computed from two server
 * numbers, exactly as Go's ageAt() computes it from the event's own At stamp —
 * the browser's clock never enters the arithmetic, so client clock skew cannot
 * make the two stacks render different text from the same state.
 */
export interface Snapshot {
  value: number;
  version: number;
  tabs: number;
  changedAtMs: number;
  changedBy: string;
  nowMs: number;
}

export const EMPTY_SNAPSHOT: Snapshot = {
  value: 0,
  version: 0,
  tabs: 0,
  changedAtMs: 0,
  changedBy: '',
  nowMs: 0,
};

/** F-CTR-3, first derived display. */
export function parity(value: number): 'even' | 'odd' {
  return value % 2 === 0 ? 'even' : 'odd';
}

/**
 * F-CTR-3, the badge whose colour class changes at thresholds (<0, 0, 1–9,
 * >=10) rather than on every value — so a repaint is not a single text node.
 */
export function band(value: number): 'negative' | 'zero' | 'low' | 'high' {
  if (value < 0) return 'negative';
  if (value === 0) return 'zero';
  if (value < 10) return 'low';
  return 'high';
}

/** F-CTR-7, the "last updated" relative timestamp. */
export function ageLabel(snapshot: Snapshot): string {
  if (snapshot.changedAtMs === 0) return 'never';
  const ms = Math.max(0, snapshot.nowMs - snapshot.changedAtMs);
  if (ms < 2000) return 'just now';
  if (ms < 60_000) return `${Math.trunc(ms / 1000)}s ago`;
  if (ms < 3_600_000) return `${Math.trunc(ms / 60_000)}m ago`;
  return `${Math.trunc(ms / 3_600_000)}h ago`;
}

/**
 * Who made the last change, from this tab's point of view.
 *
 * This is the one derivation that is per-viewer, so the server cannot bake it
 * into the pushed snapshot the way gotth-live bakes it into the rendered
 * fragment. The visible behaviour is identical; the place the comparison
 * happens is not, and that is AS-declared in bench/README.md rather than left
 * for a reviewer to notice.
 */
export function author(snapshot: Snapshot, self: string): string {
  if (!snapshot.changedBy) return 'nobody yet';
  if (snapshot.changedBy === self) return 'this tab';
  return 'another tab';
}

/** Pluralises the shared-session count. */
export function tabLabel(tabs: number): string {
  return tabs === 1 ? '1 tab' : `${tabs} tabs`;
}

/** The four operations F-CTR-2 exposes. One name per operation, as in Go. */
export type CounterOp = 'increment' | 'decrement' | 'increment10' | 'reset';

export const COUNTER_OPS: readonly CounterOp[] = [
  'increment',
  'decrement',
  'increment10',
  'reset',
];

/**
 * The pure transition. Mirrors store.go's Apply: a click never sets the value
 * from the client, it names an operation and the server decides the result.
 */
export function applyOp(value: number, op: CounterOp): number {
  switch (op) {
    case 'increment':
      return value + 1;
    case 'decrement':
      return value - 1;
    case 'increment10':
      return value + 10;
    case 'reset':
      return 0;
  }
}

export function isCounterOp(v: unknown): v is CounterOp {
  return typeof v === 'string' && (COUNTER_OPS as readonly string[]).includes(v);
}
