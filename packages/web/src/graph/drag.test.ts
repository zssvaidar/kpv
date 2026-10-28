// How a drag of a step on the graph turns into a move, or into nothing (#11).

import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';

import { dragResult } from './drag.ts';
import type { BlockRef } from './edit.ts';
import { graphFixtures } from './fixtures.ts';
import {
  chainIndexAt,
  nodePitch,
  nodeWidth,
  xAtChainIndex,
} from './geometry.ts';

const pipeline = graphFixtures['every-block-type'];
const step = (id: string): BlockRef => ({ role: 'step', id });
const stepIds = (p: Pipeline) => (p.processingSteps ?? []).map((s) => s.id);

describe('chainIndexAt', () => {
  it('is the slot whose node a dropped node overlaps most', () => {
    expect(chainIndexAt(xAtChainIndex(3))).toBe(3);
    expect(chainIndexAt(xAtChainIndex(3) + nodePitch / 2 - 1)).toBe(3);
    expect(chainIndexAt(xAtChainIndex(3) + nodePitch / 2 + 1)).toBe(4);
    expect(chainIndexAt(-nodeWidth)).toBe(-1);
  });
});

describe('dragResult', () => {
  it('moves a step to the place it is dropped over', () => {
    const result = dragResult(pipeline, step('only-paid'), {
      kind: 'dropped',
      x: xAtChainIndex(3) + 10,
    });
    expect(result?.ok).toBe(true);
    expect(stepIds(result?.pipeline as Pipeline)).toEqual([
      'to-billing-shape',
      'require-amount',
      'only-paid',
      'apply-discounts',
    ]);
  });

  it('changes nothing when the drag is cancelled or let go outside the canvas', () => {
    for (const kind of ['cancelled', 'outside'] as const) {
      expect(dragResult(pipeline, step('only-paid'), { kind })).toBeNull();
    }
  });

  it('changes nothing when the step is dropped back where it was', () => {
    expect(
      dragResult(pipeline, step('require-amount'), {
        kind: 'dropped',
        x: xAtChainIndex(3) - 20,
      }),
    ).toBeNull();
  });

  it('refuses a drop before the source or after the destination, and the pipeline stays as it was', () => {
    for (const x of [
      xAtChainIndex(0),
      xAtChainIndex(-1),
      xAtChainIndex(-20),
      xAtChainIndex(5),
      xAtChainIndex(6),
      xAtChainIndex(40),
    ]) {
      const result = dragResult(pipeline, step('to-billing-shape'), {
        kind: 'dropped',
        x,
      });
      expect(result?.ok).toBe(false);
      expect(result?.pipeline).toBe(pipeline);
    }
  });

  it('never moves the source or the destination', () => {
    for (const ref of [{ role: 'source' }, { role: 'destination' }] as const) {
      const result = dragResult(pipeline, ref, {
        kind: 'dropped',
        x: xAtChainIndex(2),
      });
      expect(result?.ok).toBe(false);
      expect(result?.pipeline).toBe(pipeline);
    }
  });
});
