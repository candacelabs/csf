'use server';

import { isCounterOp, type CounterOp } from '@/lib/core';
import { currentRoom } from '@/lib/session';
import { apply } from '@/lib/store';

/*
 * The mutations, as Server Actions (§5.4: "Mutations (counter, chat send,
 * filters) | Server Actions").
 *
 * ---------------------------------------------------------------------------
 * Two properties of this file are load-bearing for the comparison, and both
 * are disclosed rather than worked around.
 *
 * 1. The action returns nothing and revalidates nothing. Next re-renders the
 *    route and ships a Flight payload back only when an action calls
 *    revalidatePath/revalidateTag, sets a cookie, or redirects. None of those
 *    happens here, so the repaint arrives over the push channel — which is
 *    what makes CTR-1 a like-for-like measurement of the same round trip
 *    gotth-live makes, rather than a measurement of two different mechanisms
 *    racing.
 *
 * 2. React serialises Server Actions: a second action dispatched while the
 *    first is in flight waits for it. CTR-6 (20 clicks inside 1000 ms) is
 *    therefore 20 sequential round trips on this stack. That is real Next.js
 *    behaviour under the idiomatic pattern §5.4 mandates, not a handicap
 *    introduced here, and the alternative a latency-chasing team would reach
 *    for (fire-and-forget fetch to a Route Handler, or sending the event over
 *    the WebSocket in the ws variant) is written down in bench/README.md under
 *    "Pessimization risks" so the §5.4 audit can rule on it instead of
 *    discovering it.
 *
 * The `by` argument is the tab id. It is used only to render "this tab" vs
 * "another tab"; it grants nothing, so a client lying about it can only
 * mislabel its own view.
 */

async function run(op: CounterOp, by: string): Promise<void> {
  if (!isCounterOp(op)) {
    // Default-deny, mirroring Config.Events in examples/counter/counter.go:
    // the operation name is an allowlist, not a number the browser chose.
    throw new Error('unknown counter operation');
  }
  apply(await currentRoom(), op, typeof by === 'string' ? by.slice(0, 64) : '');
}

export async function increment(by: string): Promise<void> {
  await run('increment', by);
}

export async function decrement(by: string): Promise<void> {
  await run('decrement', by);
}

export async function increment10(by: string): Promise<void> {
  await run('increment10', by);
}

export async function reset(by: string): Promise<void> {
  await run('reset', by);
}
