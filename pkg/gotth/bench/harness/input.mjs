/*
 * Dispatching the input half of §3.2, and running one interaction end to end.
 *
 * Every input here goes through the CDP Input domain, never through
 * element.click() or a synthetic Event. The reason is §3.2's definition of
 * t_input: "event.timeStamp of the native pointerdown (or keydown), captured by
 * a {capture:true, passive:true} listener registered at window by the shim
 * before any application script. Using the browser's own hardware event
 * timestamp — not performance.now() inside a handler — removes any advantage
 * from whose listener runs first."
 *
 * A synthesised event has isTrusted:false and a timestamp taken at construction
 * in the page, which is a different quantity measured in a different place. CDP
 * Input events enter through the browser's real input pipeline and carry a real
 * hardware timestamp, so the number the shim reads is the number the spec
 * names.
 */

/**
 * Viewport-space centre of the element carrying a data-bench-id, after
 * scrolling it into view.
 *
 * The scroll is not a convenience. CDP Input events are dispatched at VIEWPORT
 * coordinates, and the dashboard's document is several screens tall — region E
 * sits below the fold behind 200 table rows. Without the scroll the event is
 * delivered to whatever happens to be at those coordinates, which is usually
 * nothing, and the interaction fails as a paint-predicate timeout that looks
 * like a slow stack rather than a harness bug. A real user scrolls too.
 *
 * `behavior: 'instant'` matters: a smooth scroll would still be animating when
 * the rect is read, and the click would land where the element was going to be.
 */
export async function centerOf(page, benchId) {
  const box = await page.eval(`(() => {
    const el = document.querySelector('[data-bench-id="${benchId}"]');
    if (!el) return null;
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
    const r = el.getBoundingClientRect();
    return { x: r.x + r.width / 2, y: r.y + r.height / 2, w: r.width, h: r.height };
  })()`);
  if (!box) throw new Error(`no element with data-bench-id=${benchId}`);
  if (box.w === 0 || box.h === 0) throw new Error(`[data-bench-id=${benchId}] has zero area`);
  return box;
}

export async function click(page, benchId) {
  const { x, y } = await centerOf(page, benchId);
  await page.send('Input.dispatchMouseEvent', {
    type: 'mousePressed',
    x,
    y,
    button: 'left',
    clickCount: 1,
    pointerType: 'mouse',
  });
  await page.send('Input.dispatchMouseEvent', {
    type: 'mouseReleased',
    x,
    y,
    button: 'left',
    clickCount: 1,
    pointerType: 'mouse',
  });
}

/** Focus an element without generating an input event that the shim would time. */
export async function focus(page, benchId) {
  await page.eval(`document.querySelector('[data-bench-id="${benchId}"]').focus()`, {
    awaitPromise: false,
  });
}

/**
 * A real key press: rawKeyDown, char, keyUp.
 *
 * `char` is what makes a printable key insert text into a focused field; a
 * keyDown alone moves no caret, which would make CHT-1 assert on a composer
 * that never changed.
 */
export async function pressKey(page, key, { text = key } = {}) {
  const printable = text.length === 1;
  await page.send('Input.dispatchKeyEvent', {
    type: printable ? 'keyDown' : 'rawKeyDown',
    key,
    text: printable ? text : undefined,
    unmodifiedText: printable ? text : undefined,
    windowsVirtualKeyCode: virtualKeyCode(key),
  });
  await page.send('Input.dispatchKeyEvent', {
    type: 'keyUp',
    key,
    windowsVirtualKeyCode: virtualKeyCode(key),
  });
}

function virtualKeyCode(key) {
  if (key.length === 1) return key.toUpperCase().charCodeAt(0);
  const table = { Enter: 13, Tab: 9, Escape: 27, Backspace: 8 };
  return table[key] ?? 0;
}

/**
 * Run one interaction and return its §3.1/§3.2 sample.
 *
 * The order is load-bearing and is the order the spec's recipe implies:
 *
 *   1. compute what the paint predicate should assert, from the page's CURRENT
 *      state, before anything is dispatched;
 *   2. attach the MutationObserver (shim.awaitPaint) — the shim deliberately
 *      does NOT evaluate the predicate on attach, because a push landing
 *      between "decide to observe" and "observer attached" would otherwise be
 *      reported as a paint that happened before we looked;
 *   3. dispatch the input;
 *   4. await the promise the shim already holds.
 *
 * Doing 3 before 2 is the classic race that silently drops the fastest samples,
 * which would bias every latency distribution in the same direction on both
 * stacks — common-mode, but a lie either way.
 */
export async function runInteraction(page, interaction, ctx = {}) {
  const expected = interaction.expect ? await interaction.expect(page, ctx) : {};
  const predicate = interaction.predicate(expected, ctx);

  await page.eval(
    `window.__benchPending = window.__bench.awaitPaint(${JSON.stringify(interaction.region)}, () => (${predicate}), { timeoutMs: ${interaction.timeoutMs ?? 10000} });`,
    { awaitPromise: false },
  );

  await interaction.drive(page, { ...ctx, expected });

  const sample = await page.eval('window.__benchPending');
  return { ...sample, id: interaction.id, expected };
}
