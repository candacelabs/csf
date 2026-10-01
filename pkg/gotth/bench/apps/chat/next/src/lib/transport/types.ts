import type { RoomView } from '../core';

/**
 * The connection states the page can be in.
 *
 * Same four names, same meanings, as the gotth-live client runtime writes into
 * data-gotth-status: the status indicator is part of the product surface (E1)
 * and a reader comparing screenshots should see the same words.
 */
export type ConnStatus = 'connecting' | 'live' | 'reconnecting' | 'closed';

export interface LiveState {
  view: RoomView;
  status: ConnStatus;
  /**
   * How many server messages this tab has applied. §3.3's Next.js ready
   * condition is "channel open AND its first message applied", and "applied"
   * has to be observable to be signalled.
   */
  received: number;
}

/** The one interface all three live-data variants implement. */
export type UseChatLive = (sessionKey: string, initial: RoomView) => LiveState;
