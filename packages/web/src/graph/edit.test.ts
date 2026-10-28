// The editing operations as pure model transforms (#11): no React, no React
// Flow, nothing but a pipeline in and a result out.

import { describe, expect, it } from 'vitest';

import type { Pipeline } from '@kpv/schema';
import { pipelineZodSchema, validatePipeline } from '@kpv/schema';

import shared from '../../../../schema/fixtures/pipelines.json';
import type { BlockRef, EditResult } from './edit.ts';
import {
  commit,
  deleteStep,
  insertStep,
  moveStep,
  newStepId,
  stepTypes,
} from './edit.ts';
import dragSource from './drag.ts?raw';
import editSource from './edit.ts?raw';
import { graphFixtures } from './fixtures.ts';
import geometrySource from './geometry.ts?raw';

const source: BlockRef = { role: 'source' };
const destination: BlockRef = { role: 'destination' };
const step = (id: string): BlockRef => ({ role: 'step', id });

/** A deep copy of a fixture, frozen all the way down. */
const frozen = (pipeline: Pipeline): Pipeline => {
  const freeze = <T>(value: T): T => {
    if (typeof value === 'object' && value !== null) {
      for (const inner of Object.values(value)) freeze(inner);
      Object.freeze(value);
    }
    return value;
  };
  return freeze(structuredClone(pipeline));
};

const everyType = () => frozen(graphFixtures['every-block-type']);
const zeroSteps = () => frozen(graphFixtures['zero-processing-steps']);

const stepIds = (pipeline: Pipeline) =>
  (pipeline.processingSteps ?? []).map((s) => s.id);

/** The pipeline of a result that must have succeeded. */
const accepted = (result: EditResult): Pipeline => {
  if (!result.ok) throw new Error(`rejected: ${result.reason}`);
  return result.pipeline;
};

/**
 * A rejection must hand back the very pipeline it was given, unchanged.
 * The input is frozen, so any write to it would already have thrown.
 */
const expectRejected = (
  result: EditResult,
  previous: Pipeline,
  reason: RegExp,
) => {
  expect(result.ok).toBe(false);
  expect(result.pipeline).toBe(previous);
  if (!result.ok) expect(result.reason).toMatch(reason);
};

/** What every accepted result has to be: new, and valid by the schema. */
const expectNewAndValid = (result: EditResult, previous: Pipeline) => {
  const next = accepted(result);
  expect(next).not.toBe(previous);
  expect(pipelineZodSchema.safeParse(next).success).toBe(true);
  expect(validatePipeline(next)).toEqual([]);
};

// every-block-type: only-paid, to-billing-shape, require-amount,
// apply-discounts.
const original = stepIds(graphFixtures['every-block-type']);

describe('insertStep', () => {
  it('inserts a block between two existing steps', () => {
    const before = everyType();
    const result = insertStep(
      before,
      { before: step('only-paid'), after: step('to-billing-shape') },
      'validate',
    );
    expectNewAndValid(result, before);
    expect(stepIds(accepted(result))).toEqual([
      'only-paid',
      'validate-1',
      'to-billing-shape',
      'require-amount',
      'apply-discounts',
    ]);
    expect(result.ok && result.stepId).toBe('validate-1');
    // The other blocks are the same blocks, unchanged.
    const next = accepted(result);
    expect(next.source).toEqual(before.source);
    expect(next.destination).toEqual(before.destination);
  });

  it('inserts right after the source and right before the destination', () => {
    const before = everyType();
    const first = accepted(
      insertStep(
        before,
        { before: source, after: step('only-paid') },
        'filter',
      ),
    );
    expect(stepIds(first)[0]).toBe('filter-1');
    const last = accepted(
      insertStep(
        before,
        { before: step('apply-discounts'), after: destination },
        'customCode',
      ),
    );
    expect(stepIds(last).at(-1)).toBe('customCode-1');
  });

  it('inserts between source and destination when there are no steps, absent or empty', () => {
    const empty = zeroSteps();
    expect(empty.processingSteps).toEqual([]);
    const absent = frozen(
      shared.valid.find(
        (fixture) => fixture.name === 'source-and-destination-only',
      )?.pipeline as Pipeline,
    );
    expect(absent.processingSteps).toBeUndefined();
    for (const before of [empty, absent]) {
      const result = insertStep(
        before,
        { before: source, after: destination },
        'transform',
      );
      expectNewAndValid(result, before);
      expect(stepIds(accepted(result))).toEqual(['transform-1']);
    }
  });

  it('inserts every step type as a valid step', () => {
    for (const type of stepTypes) {
      const before = zeroSteps();
      const result = insertStep(
        before,
        { before: source, after: destination },
        type,
      );
      expectNewAndValid(result, before);
      expect(accepted(result).processingSteps?.[0]?.type).toBe(type);
    }
  });

  it('rejects two blocks that are not next to each other', () => {
    const before = everyType();
    expectRejected(
      insertStep(before, { before: source, after: destination }, 'filter'),
      before,
      /not next to each other/,
    );
    expectRejected(
      insertStep(
        before,
        { before: step('to-billing-shape'), after: step('only-paid') },
        'filter',
      ),
      before,
      /not next to each other/,
    );
    expectRejected(
      insertStep(
        before,
        { before: destination, after: step('only-paid') },
        'filter',
      ),
      before,
      /not next to each other/,
    );
    expectRejected(
      insertStep(
        before,
        { before: step('missing'), after: destination },
        'filter',
      ),
      before,
      /no step "missing"/,
    );
  });
});

describe('the id an inserted step gets', () => {
  it('is unique within the pipeline, the source and destination ids included', () => {
    // Twelve filters, each inserted right after the source.
    let pipeline = zeroSteps();
    for (let i = 0; i < 12; i++) {
      const first = stepIds(pipeline)[0];
      pipeline = accepted(
        insertStep(
          pipeline,
          {
            before: source,
            after: first === undefined ? destination : step(first),
          },
          'filter',
        ),
      );
    }
    const ids = stepIds(pipeline);
    expect(ids.length).toBe(12);
    expect(new Set(ids).size).toBe(12);
    expect(validatePipeline(pipeline)).toEqual([]);

    const clashing: Pipeline = frozen({
      ...graphFixtures['zero-processing-steps'],
      source: { id: 'filter-1', type: 'kafkaSource', topic: 'in.v1' },
      destination: {
        id: 'filter-2',
        type: 'kafkaDestination',
        topic: 'out.v1',
        messageFormat: 'json',
      },
    });
    expect(newStepId(clashing, 'filter')).toBe('filter-3');
  });

  it('is stable: later moves, inserts and deletes leave it as it was', () => {
    const inserted = accepted(
      insertStep(
        everyType(),
        { before: step('only-paid'), after: step('to-billing-shape') },
        'filter',
      ),
    );
    expect(stepIds(inserted)).toContain('filter-1');
    const moved = accepted(moveStep(inserted, step('filter-1'), 4));
    const deleted = accepted(deleteStep(moved, step('only-paid')));
    const again = accepted(
      insertStep(
        deleted,
        { before: source, after: step('to-billing-shape') },
        'filter',
      ),
    );
    expect(stepIds(again)).toEqual([
      'filter-2',
      'to-billing-shape',
      'require-amount',
      'filter-1',
      'apply-discounts',
    ]);
  });

  it('fills the lowest free number, so a deleted id can be given again only once it is gone', () => {
    const one = accepted(
      insertStep(zeroSteps(), { before: source, after: destination }, 'filter'),
    );
    const gone = accepted(deleteStep(one, step('filter-1')));
    expect(newStepId(one, 'filter')).toBe('filter-2');
    expect(newStepId(gone, 'filter')).toBe('filter-1');
  });
});

describe('moveStep', () => {
  it('moves a step later and earlier, counting places in the chain', () => {
    const before = everyType();
    const later = moveStep(before, step('only-paid'), 3);
    expectNewAndValid(later, before);
    expect(stepIds(accepted(later))).toEqual([
      'to-billing-shape',
      'require-amount',
      'only-paid',
      'apply-discounts',
    ]);
    const earlier = moveStep(before, step('apply-discounts'), 1);
    expectNewAndValid(earlier, before);
    expect(stepIds(accepted(earlier))).toEqual([
      'apply-discounts',
      'only-paid',
      'to-billing-shape',
      'require-amount',
    ]);
    // To the place it is already in: the same order, as a new pipeline.
    const same = moveStep(before, step('require-amount'), 3);
    expectNewAndValid(same, before);
    expect(stepIds(accepted(same))).toEqual(original);
  });

  it('keeps every block as it was, only in another order', () => {
    const before = everyType();
    const next = accepted(moveStep(before, step('only-paid'), 4));
    const byId = (p: Pipeline) =>
      Object.fromEntries((p.processingSteps ?? []).map((s) => [s.id, s]));
    expect(byId(next)).toEqual(byId(before));
    expect(next.source).toBe(before.source);
    expect(next.destination).toBe(before.destination);
  });

  it('does not move the source or the destination', () => {
    const before = everyType();
    expectRejected(
      moveStep(before, source, 2),
      before,
      /source cannot be moved/,
    );
    expectRejected(
      moveStep(before, destination, 2),
      before,
      /destination cannot be moved/,
    );
  });

  it('does not put a step before the source or after the destination', () => {
    const before = everyType();
    for (const to of [0, -1, -10]) {
      expectRejected(
        moveStep(before, step('to-billing-shape'), to),
        before,
        /before the source/,
      );
    }
    // Four steps: places 1 to 4. 5 is the destination's, 6 is past it.
    for (const to of [5, 6, 100]) {
      expectRejected(
        moveStep(before, step('to-billing-shape'), to),
        before,
        /after the destination/,
      );
    }
    expectRejected(
      moveStep(before, step('to-billing-shape'), 1.5),
      before,
      /not a place/,
    );
    expectRejected(moveStep(before, step('missing'), 1), before, /no step/);
  });
});

describe('deleteStep', () => {
  it('deletes a processing step', () => {
    const before = everyType();
    const result = deleteStep(before, step('require-amount'));
    expectNewAndValid(result, before);
    expect(stepIds(accepted(result))).toEqual([
      'only-paid',
      'to-billing-shape',
      'apply-discounts',
    ]);
  });

  it('does not delete the source or the destination', () => {
    const before = everyType();
    expectRejected(
      deleteStep(before, source),
      before,
      /source cannot be deleted/,
    );
    expectRejected(
      deleteStep(before, destination),
      before,
      /destination cannot be deleted/,
    );
    expectRejected(deleteStep(before, step('missing')), before, /no step/);
  });

  it('deleting the only step leaves a valid source-to-destination pipeline', () => {
    const one = frozen(
      accepted(
        insertStep(
          zeroSteps(),
          { before: source, after: destination },
          'filter',
        ),
      ),
    );
    const result = deleteStep(one, step('filter-1'));
    expectNewAndValid(result, one);
    const next = accepted(result);
    expect(next.processingSteps).toEqual([]);
    expect(next.source).toEqual(one.source);
    expect(next.destination).toEqual(one.destination);
  });

  it('can delete every step, one after another', () => {
    let pipeline = everyType();
    for (const id of original)
      pipeline = accepted(deleteStep(pipeline, step(id)));
    expect(stepIds(pipeline)).toEqual([]);
    expect(validatePipeline(pipeline)).toEqual([]);
  });
});

describe('every operation', () => {
  it('returns a new pipeline and never changes the one it was given', () => {
    const before = everyType();
    const snapshot = structuredClone(before);
    const results = [
      insertStep(
        before,
        { before: source, after: step('only-paid') },
        'filter',
      ),
      moveStep(before, step('only-paid'), 2),
      deleteStep(before, step('only-paid')),
    ];
    for (const result of results) {
      const next = accepted(result);
      expect(next).not.toBe(before);
      expect(next.processingSteps).not.toBe(before.processingSteps);
    }
    // Frozen, so a write would have thrown; and deep-equal to a copy taken
    // before, so nothing was swapped either.
    expect(before).toEqual(snapshot);
  });

  it('is validated against the schema: an invalid outcome is rejected and the previous model handed back', () => {
    // A pipeline with a step the schema does not accept (an empty filter
    // expression): every operation's outcome would carry it, so every one
    // is refused and the pipeline it was given comes back as it was.
    const broken = frozen({
      ...graphFixtures['every-block-type'],
      processingSteps: [
        { id: 'bad', type: 'filter', configuration: { expression: '' } },
        ...(graphFixtures['every-block-type'].processingSteps ?? []),
      ],
    });
    const snapshot = structuredClone(broken);
    expectRejected(
      insertStep(broken, { before: source, after: step('bad') }, 'filter'),
      broken,
      /not be a valid pipeline \(processingSteps\.1\.configuration\.expression\)/,
    );
    expectRejected(
      moveStep(broken, step('only-paid'), 1),
      broken,
      /not be a valid/,
    );
    expectRejected(
      deleteStep(broken, step('only-paid')),
      broken,
      /not be a valid/,
    );
    expect(broken).toEqual(snapshot);
  });

  it('is checked with the annotation rules too: a duplicate step id is refused', () => {
    const before = everyType();
    const duplicate: Pipeline = {
      ...before,
      processingSteps: [
        ...(before.processingSteps ?? []),
        { id: 'only-paid', type: 'filter', configuration: { expression: 'x' } },
      ],
    };
    expectRejected(
      commit(before, duplicate),
      before,
      /duplicate id "only-paid"/,
    );
    // And the outcome is the model as built, without defaults filled in.
    const plain = accepted(commit(before, { ...before }));
    expect(plain.processingSteps?.[0]).toEqual(before.processingSteps?.[0]);
    expect(
      (plain.processingSteps?.[0]?.configuration as { keepOnMatch?: boolean })
        .keepOnMatch,
    ).toBeUndefined();
  });
});

describe('the editing modules', () => {
  // Every module the transforms load, followed through relative imports:
  // none of them may name React or React Flow, so the transforms import and
  // run without either.
  const sources: Record<string, string> = {
    'edit.ts': editSource,
    'drag.ts': dragSource,
    'geometry.ts': geometrySource,
  };
  const importsOf = (file: string): string[] => {
    const source = sources[file];
    if (source === undefined) throw new Error(`${file} was not read`);
    return [
      ...source.matchAll(/^(?:import|export)[^;]*?from '([^']+)';/gms),
    ].map((match) => match[1] as string);
  };

  it('import neither React nor React Flow', () => {
    const seen = new Set<string>();
    const outside = new Set<string>();
    const visit = (file: string) => {
      if (seen.has(file)) return;
      seen.add(file);
      for (const target of importsOf(file)) {
        if (target.startsWith('./')) visit(target.slice(2));
        else outside.add(target);
      }
    };
    for (const file of ['edit.ts', 'drag.ts', 'geometry.ts']) visit(file);
    expect([...seen].sort()).toEqual(['drag.ts', 'edit.ts', 'geometry.ts']);
    expect([...outside].sort()).toEqual(['@kpv/schema']);
  });
});
