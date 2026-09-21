'use client';

import { useCallback, useEffect, useState } from 'react';

import { band, parity } from '@/lib/core';

/*
 * C-A. The same visible derivations as C-B — value, parity, badge — so the
 * two rows differ in where the state lives and in nothing else. Nothing here
 * touches the network.
 *
 * data-bench-region="C" keeps its ROI distinct from A and B, so the
 * paint_present cross-check (§3.1) cannot confuse a C-A frame for a C-B one.
 */
export default function LocalCounter() {
  const [value, setValue] = useState(0);

  // C-A is interactive as soon as it has hydrated: there is no channel to
  // open and no first message to apply, which is exactly the advantage the
  // row exists to quantify.
  useEffect(() => {
    const bench = (window as unknown as { __bench?: { ready: boolean } }).__bench;
    if (bench) bench.ready = true;
  }, []);

  const onKeyDown = useCallback((event: React.KeyboardEvent) => {
    if (event.key === '+') setValue((v) => v + 1);
    else if (event.key === '-') setValue((v) => v - 1);
  }, []);

  return (
    <section
      className="card value"
      data-bench-region="C"
      data-bench-id="local-counter"
      tabIndex={0}
      onKeyDown={onKeyDown}
    >
      <p className="number" data-bench-id="local-value" data-bench-value>
        {value}
      </p>
      <p className="derived">
        <span className="parity">{parity(value)}</span>
        <span className={`badge badge-${band(value)}`}>{band(value)}</span>
      </p>
      <div className="buttons">
        <button type="button" data-bench-id="local-dec" onClick={() => setValue((v) => v - 1)}>
          &minus;1
        </button>
        <button type="button" data-bench-id="local-inc" onClick={() => setValue((v) => v + 1)}>
          +1
        </button>
        <button type="button" data-bench-id="local-inc10" onClick={() => setValue((v) => v + 10)}>
          +10
        </button>
        <button type="button" data-bench-id="local-reset" onClick={() => setValue(0)}>
          Reset
        </button>
      </div>
    </section>
  );
}
