'use client';

import { useCallback, useEffect, useRef } from 'react';

import { useCounterLive } from '@transport';
import { decrement, increment, increment10, reset } from '@/app/counter/actions';
import { ageLabel, author, band, parity, tabLabel, type Snapshot } from '@/lib/core';

/*
 * The whole interactive surface of the counter, and the only 'use client' on
 * the measured route.
 *
 * Why one boundary and not four: regions A and B are two views of one
 * subscription. Splitting them into separate client components would give each
 * its own EventSource/WebSocket — two connections per tab where gotth-live
 * opens one — and D3 would then charge Next.js for an architecture no
 * competent team would ship. The static shell (h1, lede, status line, hint)
 * stays in the Server Component that renders this one (§5.4: client boundaries
 * as deep as the interactivity requires, and no deeper than it permits).
 *
 * The markup below is examples/counter/view.templ, element for element and
 * class for class. The differences are exactly two, both required by E1 rather
 * than chosen:
 *
 *   - data-gotth-region / data-gotth-on are absent. They are gotth-live's own
 *     protocol attributes; the benchmark's hooks (data-bench-*) are the ones
 *     §2.0 mandates on both sides and they are all present.
 *   - region A carries tabIndex and a keydown handler, because F-CTR-6 is a
 *     feature this stack has and the other does not. See bench/README.md's
 *     parity note.
 */

export interface CounterLiveProps {
  initial: Snapshot;
  tabId: string;
}

export default function CounterLive({ initial, tabId }: CounterLiveProps) {
  const { snapshot, status, received } = useCounterLive(tabId, initial);
  const readySignalled = useRef(false);

  /*
   * The connection indicator, written onto <html> so the one stylesheet can
   * select on it — the same mechanism the gotth-live runtime uses with
   * data-gotth-status, so the indicator costs neither stack an element.
   *
   * Imperative rather than rendered, because the server does not know the
   * client's connection state and rendering a guess would be a hydration
   * mismatch (a bug class G-8 says gotth-live does not have; introducing one
   * here would be scoring an own goal on behalf of the competitor).
   */
  useEffect(() => {
    document.documentElement.setAttribute('data-bench-status', status);
  }, [status]);

  /*
   * §3.3 — the TTI signal, on this stack's stated condition: React hydration
   * complete for the interactive region (this effect running is that), AND the
   * live-data channel open, AND its first message applied.
   *
   * Set exactly once. The shim stamps t_ready at the assignment, so the number
   * D5 publishes is taken by the same code on both stacks.
   */
  useEffect(() => {
    if (readySignalled.current) return;
    if (status !== 'live' || received === 0) return;
    readySignalled.current = true;
    const bench = (window as unknown as { __bench?: { ready: boolean } }).__bench;
    if (bench) bench.ready = true;
  }, [status, received]);

  /*
   * F-CTR-6 — `+` and `-` on the focused counter.
   *
   * Attached to both regions so the key works whether focus is on the counter
   * itself or on one of the buttons, which is what a user would expect and
   * what CTR-5 dispatches against. gotth-live cannot express this today: its
   * data-gotth-on vocabulary binds a DOM event type to a server event with no
   * way to say "only when the key was +", so a keydown binding would fire on
   * every key. That is a real parity finding, recorded verbatim in
   * bench/README.md, not a difference to paper over here.
   */
  const onKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === '+') {
        void increment(tabId);
      } else if (event.key === '-') {
        void decrement(tabId);
      }
    },
    [tabId],
  );

  return (
    <>
      <section
        className="card value"
        data-bench-region="A"
        data-bench-id="counter"
        tabIndex={0}
        onKeyDown={onKeyDown}
      >
        <p className="number" data-bench-id="value" data-bench-value>
          {snapshot.value}
        </p>
        <p className="derived">
          <span className="parity">{parity(snapshot.value)}</span>
          <span className={`badge badge-${band(snapshot.value)}`}>{band(snapshot.value)}</span>
        </p>
        <p className="age">
          changed {ageLabel(snapshot)} by {author(snapshot, tabId)}
        </p>
      </section>

      <section className="card controls" data-bench-region="B" onKeyDown={onKeyDown}>
        <div className="buttons">
          <button type="button" data-bench-id="dec" onClick={() => void decrement(tabId)}>
            &minus;1
          </button>
          <button type="button" data-bench-id="inc" onClick={() => void increment(tabId)}>
            +1
          </button>
          <button type="button" data-bench-id="inc10" onClick={() => void increment10(tabId)}>
            +10
          </button>
          <button type="button" data-bench-id="reset" onClick={() => void reset(tabId)}>
            Reset
          </button>
        </div>
        <p className="tabs" data-bench-id="tabs">
          {tabLabel(snapshot.tabs)} sharing this counter
        </p>
      </section>
    </>
  );
}
