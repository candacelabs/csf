'use server';

import { isRoom, type RoomId } from '@/lib/core';
import { identity } from '@/lib/session';
import { send, switchRoom } from '@/lib/store';

/*
 * The mutations, as Server Actions (§5.4: "Mutations (counter, chat send,
 * filters) | Server Actions").
 *
 * Neither action revalidates a path or a tag. Next re-renders the route and
 * ships a Flight payload back only when an action revalidates, sets a cookie or
 * redirects; none of that happens here, so the repaint arrives over the push
 * channel. That is what makes CHT-2 a like-for-like measurement of the same
 * round trip gotth-live makes, rather than a measurement of two mechanisms
 * racing each other to paint the same message.
 *
 * `send` RETURNS the verdict rather than pushing it, because F-CHT-4's error is
 * this session's own and nobody else's: pushing it down the shared channel
 * would repaint every viewer with a message about somebody else's typo.
 */

export interface SendVerdict {
  error: string;
  /** Server sequence of the accepted message, or 0. For assertions and keys. */
  seq: number;
}

export async function sendMessage(
  key: string,
  room: string,
  body: string,
  clientId: string,
): Promise<SendVerdict> {
  if (!isRoom(room)) return { error: 'unknown room', seq: 0 };
  const who = await identity();
  const result = send(key, who.me, room as RoomId, body, clientId.slice(0, 64));
  return { error: result.error, seq: result.message?.seq ?? 0 };
}

/*
 * F-CHT-7's room switch.
 *
 * It is a Server Action and NOT a navigation, which is a decision §2 forces:
 * "No client-side routing on either side", and §3.2 requires t_input and
 * t_paint to come from the same page's performance.now() timeline. A document
 * navigation would put them in two timelines and CHT-4 would be unmeasurable
 * under this spec's own definition. So the active room is session state on the
 * server, the switch mutates it, and the new room's view arrives on the push
 * channel exactly like every other change. The URL's [room] segment is the
 * entry point, not a router.
 */
export async function selectRoom(key: string, room: string): Promise<void> {
  if (!isRoom(room)) return;
  const who = await identity();
  switchRoom(key, who.me, room as RoomId);
}
