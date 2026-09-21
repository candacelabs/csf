'use server';

import {
  PER_PAGE_CHOICES,
  STATUS_FILTERS,
  type PerPage,
  type SortMode,
  type StatusFilter,
} from '@/lib/core';
import { controlsOf, refreshPanel, setControls } from '@/lib/store';

/*
 * The dashboard's mutations, as Server Actions (§5.4: "Mutations (counter, chat
 * send, filters) | Server Actions"). Every one of §2.4's six controls goes
 * through here, because §2.4 says the filter and the search "filter Region B
 * server-side on both stacks" and E4 says a feature that is server-authoritative
 * in one stack is server-authoritative in the other.
 *
 * None of them revalidates a path or a tag: the repaint arrives over the push
 * channel, which is what makes DSH-1..5 like-for-like measurements of the same
 * round trip gotth-live makes rather than measurements of two mechanisms racing.
 * The one exception is region E, whose whole point is that it is NOT pushed —
 * see refreshPanelAction.
 *
 * Every argument is validated against a closed list. A control is a name the
 * server recognises, never a value the browser chose, which is the same
 * default-deny shape the counter's actions use.
 */

export async function setFilter(key: string, filter: string): Promise<void> {
  if (!(STATUS_FILTERS as readonly string[]).includes(filter)) return;
  setControls(key, { filter: filter as StatusFilter });
}

export async function setSearch(key: string, search: string): Promise<void> {
  /* Bounded, because it is a substring match over 200 names and an unbounded
     needle is an unbounded scan somebody else pays for. */
  setControls(key, { search: search.slice(0, 64) });
}

export async function setSort(key: string, sort: string): Promise<void> {
  if (sort !== 'off' && sort !== 'asc' && sort !== 'desc') return;
  setControls(key, { sort: sort as SortMode });
}

export async function setPerPage(key: string, perPage: number): Promise<void> {
  if (!(PER_PAGE_CHOICES as readonly number[]).includes(perPage)) return;
  setControls(key, { perPage: perPage as PerPage });
}

/** DSH-5. Server-authoritative; see store.ts's applyTick for why. */
export async function setPaused(key: string, paused: boolean): Promise<void> {
  setControls(key, { paused: !!paused });
}

/*
 * Region E (AS-3): "gotth-live: plain HTMX per FR-62; Next.js: Server Action
 * form — same visible behaviour".
 *
 * The panel is the one thing on this page that is NOT pushed. It refreshes only
 * when a person presses the button, which is what "on demand" means in §2.4's
 * table, and it returns its content to the caller rather than broadcasting it —
 * the same shape as HTMX's GET-and-swap on the other stack. Both mechanisms are
 * in both apps and both are counted in both bundle measurements (§3.5).
 */
export interface PanelState {
  text: string;
  seq: number;
  ts: number;
}

export async function refreshPanelAction(
  _previous: PanelState,
  form: FormData,
): Promise<PanelState> {
  const key = String(form.get('k') ?? '');
  if (key === '') return _previous;
  return refreshPanel(key);
}

export async function currentControls(key: string) {
  return controlsOf(key);
}
