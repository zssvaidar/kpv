// What a drag of a processing step on the graph does to the pipeline, decided
// without the graph library: the graph reports how the drag ended, and this
// turns it into a move (edit.ts) or into nothing.
//
// A drag that was cancelled (escape, or the pointer cancelled by the browser)
// or that let go outside the canvas changes nothing: the step goes back to
// where it was and the order is the order it was. A drop on the canvas moves
// the step to the place in the chain it was dropped over; dropping it over or
// beyond the source or the destination is a move edit.ts rejects, so that is
// nothing too, with the reason why.

import type { Pipeline } from '@kpv/schema';

import type { BlockRef, EditResult } from './edit.ts';
import { chainIndexOf, moveStep } from './edit.ts';
import { chainIndexAt } from './geometry.ts';

/** How a drag ended. */
export type DragEnd =
  { kind: 'dropped'; x: number } | { kind: 'cancelled' } | { kind: 'outside' };

/**
 * What a drag of this block that ended this way does: a move when it was
 * dropped on the canvas somewhere other than where it started, otherwise
 * `null`, which means the pipeline is left as it was.
 */
export function dragResult(
  pipeline: Pipeline,
  ref: BlockRef,
  end: DragEnd,
): EditResult | null {
  if (end.kind !== 'dropped') return null;
  const target = chainIndexAt(end.x);
  if (target === chainIndexOf(pipeline, ref)) return null;
  return moveStep(pipeline, ref, target);
}
