// The TypeScript half of the shared working-copy endpoint fixtures (#43). The
// Go half is agent/internal/httpapi/workcopies_fixtures_test.go and reads the
// same file, so what the agent sends and takes and what the browser parses
// and sends are held to the same cases.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';
import type { ZodType } from 'zod';

import type {
  WorkcopyError,
  WorkcopyList,
  WorkcopyReport,
  WorkcopyReview,
} from './index.js';
import {
  sourceJsonSchema,
  workcopyDiscardedSchema,
  workcopyErrorSchema,
  workcopyJsonSchema,
  workcopyListSchema,
  workcopyReportSchema,
  workcopyReviewSchema,
  workcopyRevisionRequestSchema,
  workcopySnapshotRemovedSchema,
  workcopySnapshotRequestSchema,
} from './index.js';

interface Case {
  name: string;
  why: string;
  body: unknown;
}

interface CaseSet {
  valid: Case[];
  invalid: Case[];
}

interface Fixtures {
  lists: CaseSet;
  reviews: CaseSet;
  revisionRequests: CaseSet;
  snapshotRequests: CaseSet;
  reports: CaseSet;
  discarded: CaseSet;
  removed: CaseSet;
  errors: CaseSet;
  pairingViolations: {
    lists: Case[];
    reviews: Case[];
    reports: Case[];
    errors: Case[];
  };
}

const fixtures = JSON.parse(
  readFileSync(
    new URL(
      '../../../schema/fixtures/workcopy-responses.json',
      import.meta.url,
    ),
    'utf8',
  ),
) as Fixtures;

const validatorFor = (definition: string | null): Validator =>
  definition === null
    ? new Validator(structuredClone(workcopyJsonSchema), '2020-12', false)
    : new Validator(
        {
          $schema: workcopyJsonSchema['$schema'] as string,
          $defs: structuredClone(workcopyJsonSchema['$defs']) as Record<
            string,
            unknown
          >,
          $ref: `#/$defs/${definition}`,
        },
        '2020-12',
        false,
      );

const groups: [string, CaseSet, Validator, ZodType][] = [
  ['list', fixtures.lists, validatorFor(null), workcopyListSchema],
  [
    'review',
    fixtures.reviews,
    validatorFor('workcopyReview'),
    workcopyReviewSchema,
  ],
  [
    'revision request',
    fixtures.revisionRequests,
    validatorFor('workcopyRevisionRequest'),
    workcopyRevisionRequestSchema,
  ],
  [
    'snapshot request',
    fixtures.snapshotRequests,
    validatorFor('workcopySnapshotRequest'),
    workcopySnapshotRequestSchema,
  ],
  [
    'report',
    fixtures.reports,
    validatorFor('workcopyReport'),
    workcopyReportSchema,
  ],
  [
    'discarded',
    fixtures.discarded,
    validatorFor('workcopyDiscarded'),
    workcopyDiscardedSchema,
  ],
  [
    'removed',
    fixtures.removed,
    validatorFor('workcopySnapshotRemoved'),
    workcopySnapshotRemovedSchema,
  ],
  [
    'error',
    fixtures.errors,
    validatorFor('workcopyError'),
    workcopyErrorSchema,
  ],
];

/** The pairings each shape's description states in words. */
const listPaired = (list: WorkcopyList): boolean =>
  list.copies.every(
    (copy) =>
      copy.available === (copy.reason === undefined) &&
      (copy.reason === 'rootUnavailable') === (copy.rootReason !== undefined),
  );

const reviewPaired = (review: WorkcopyReview): boolean =>
  (review.revision !== undefined) === review.files.length > 0 &&
  review.files.every((file) =>
    file.state === 'ready'
      ? (file.baseText !== undefined) === (file.change !== 'added') &&
        (file.stagedText !== undefined) === (file.change !== 'deleted')
      : file.baseText === undefined && file.stagedText === undefined,
  );

const reportPaired = (report: WorkcopyReport): boolean =>
  report.complete ===
    report.files.every(
      (file) => file.result === 'written' || file.result === 'unchanged',
    ) &&
  report.files.every(
    (file) => (file.result === 'failed') === (file.kind !== undefined),
  );

const errorPaired = (failure: WorkcopyError): boolean =>
  (failure.error === 'rootUnavailable') === (failure.reason !== undefined) &&
  (failure.error === 'drift') === (failure.drift !== undefined) &&
  (failure.drift ?? []).every((drift) => drift.now !== 'ready');

describe('the working-copy endpoint shapes in @kpv/schema', () => {
  it('has fixtures of every kind, or it is asserting nothing', () => {
    for (const [, set] of groups) {
      expect(set.valid.length).toBeGreaterThan(0);
      expect(set.invalid.length).toBeGreaterThan(0);
    }
    for (const cases of Object.values(fixtures.pairingViolations)) {
      expect(cases.length).toBeGreaterThan(0);
    }
  });

  for (const [name, set, validator, zod] of groups) {
    describe(`a valid ${name}`, () => {
      it.each(set.valid)('is accepted: $name', ({ body, why }) => {
        expect(validator.validate(body).valid, why).toBe(true);
        const parsed = zod.safeParse(body);
        expect(parsed.error?.issues ?? [], why).toEqual([]);
      });
    });

    describe(`an invalid ${name}`, () => {
      it.each(set.invalid)('is refused: $name', ({ body, why }) => {
        expect(validator.validate(body).valid, why).toBe(false);
        expect(zod.safeParse(body).success, why).toBe(false);
      });
    });
  }

  it('holds the pairings the schema states in words', () => {
    const checks: [Case[], Case[], ZodType, (value: never) => boolean][] = [
      [
        fixtures.lists.valid,
        fixtures.pairingViolations.lists,
        workcopyListSchema,
        listPaired,
      ],
      [
        fixtures.reviews.valid,
        fixtures.pairingViolations.reviews,
        workcopyReviewSchema,
        reviewPaired,
      ],
      [
        fixtures.reports.valid,
        fixtures.pairingViolations.reports,
        workcopyReportSchema,
        reportPaired,
      ],
      [
        fixtures.errors.valid,
        fixtures.pairingViolations.errors,
        workcopyErrorSchema,
        errorPaired,
      ],
    ];
    for (const [valid, violations, zod, paired] of checks) {
      for (const { body, why } of valid) {
        expect(paired(zod.parse(body) as never), why).toBe(true);
      }
      for (const { body, why } of violations) {
        const parsed = zod.safeParse(body);
        expect(parsed.success, why).toBe(true);
        expect(paired(parsed.data as never), why).toBe(false);
      }
    }
  });

  it('copies the source path rule and the source root reasons', () => {
    const defs = (schema: Readonly<Record<string, unknown>>) =>
      schema['$defs'] as Record<string, Record<string, unknown>>;
    const own = defs(workcopyJsonSchema)['workcopyPath'] ?? {};
    const source = defs(sourceJsonSchema)['sourcePath'] ?? {};
    for (const keyword of ['type', 'minLength', 'maxLength', 'pattern']) {
      expect(own[keyword]).toEqual(source[keyword]);
    }
    expect(defs(workcopyJsonSchema)['workcopyRootReason']?.['enum']).toEqual(
      defs(sourceJsonSchema)['sourceRootReason']?.['enum'],
    );
  });
});
