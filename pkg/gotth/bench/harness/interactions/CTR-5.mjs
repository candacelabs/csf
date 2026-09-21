import { focus, pressKey } from '../input.mjs';

/*
 * CTR-5 — keydown `+` on the focused counter; same predicate as CTR-1 (§2.1).
 *
 * BOTH STACKS EXPRESS F-CTR-6, so this row is measured on both and neither
 * column reports "not measured, and why" (§7).
 *
 * That is a correction. This note used to say gotth-live could not express the
 * row, because `data-gotth-on` bound a DOM event type with no key filter — true
 * when the row was written, and it is why `live.Bind.Keys` and `live.OnAll`
 * landed at api-surface checkpoint 3 (F-3), citing F-CTR-6 by name. The counter
 * now spreads two filtered bindings onto one focusable element
 * (apps/counter/gotth/bindings.go). `Bind.Keys` compares exactly and
 * case-sensitively against the browser's own KeyboardEvent.key, and pressKey
 * below dispatches key "+" with its text, so the filter and the harness agree
 * on the spelling by construction rather than by luck.
 *
 * What gotth-live still cannot express is narrower and belongs to the chat app:
 * `Bind.Keys` compares the key and not the modifier state, and a key binding
 * never calls preventDefault — which is why F-CHT-3's "Enter sends,
 * Shift+Enter newlines" is not expressible. bench/README.md carries that one.
 * Neither property is reachable from a bare `+` or `-`.
 *
 * CORRECTED 2026-08-05: the paragraph above is no longer true and is kept for
 * the same reason the paragraph above IT is kept — this file has now been wrong
 * about the library's reach twice, in the same direction, and both times the
 * fix landed citing the row that recorded the gap. `Bind.NoModifiers` compares
 * the modifier state and `Bind.PreventDefault` takes the key, both per binding;
 * F-CHT-3 is expressible, it is implemented in apps/chat/gotth, and
 * bench/README.md still carries that one.
 *
 * WHAT DOES NOT CHANGE HERE, and it is the point of the last sentence above:
 * this row's two bindings set NEITHER option and must not. F-CTR-6's "+" IS
 * Shift and "=" pressed together, so a binding that demanded no modifier held
 * would match nothing at all; and nothing should take "+" away from the
 * browser. The two bindings below render byte for byte what they rendered
 * before either option existed.
 */
export default {
  id: 'CTR-5',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'yes',
  async setup(page) {
    await focus(page, 'counter');
  },
  async expect(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    return { value: value + 1 };
  },
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  drive: (page) => pressKey(page, '+'),
};
