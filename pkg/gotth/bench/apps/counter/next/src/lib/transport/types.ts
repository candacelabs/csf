import type { Snapshot } from '../core';

/**
 * The connection states the page can be in.
 *
 * Same four names, same meanings, as the gotth-live client runtime writes into
 * data-gotth-status: the status indicator is part of the product surface (E1)
 * and a reader comparing screenshots should see the same words.
 */
export type ConnStatus = 'connecting' | 'live' | 'reconnecting' | 'closed';

export interface LiveState {
  snapshot: Snapshot;
  status: ConnStatus;
  /**
   * How many server messages this tab has applied.
   *
   * It exists for §3.3: the Next.js `ready` condition is "channel open AND its
   * first message applied", and "applied" has to be observable to be
   * signalled. A boolean would do; a count also makes a dropped-first-message
   * bug visible in the data instead of as a missing t_ready.
   */
  received: number;
}

/**
 * The one interface all three live-data variants implement.
 *
 * Exactly one implementation is bundled per build (see lib/variant.ts and the
 * `@transport` alias in next.config.ts), so this type is the seam and not a
 * runtime switch.
 */
export type UseCounterLive = (tabId: string, initial: Snapshot) => LiveState;
