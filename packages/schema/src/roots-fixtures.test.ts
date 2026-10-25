// The TypeScript half of the shared project root fixtures (#63). The Go half
// is agent/internal/httpapi/roots_fixtures_test.go and reads the same file, so
// what the agent sends and takes and what the browser parses and sends are
// held to the same cases.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';
import type { ZodType } from 'zod';

import type { ProjectRoot, RootList, RootsError } from './index.js';
import {
  authorizedRootSchema,
  rootListSchema,
  rootRequestSchema,
  rootsErrorSchema,
  rootsJsonSchema,
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
  requests: CaseSet;
  authorized: CaseSet;
  errors: CaseSet;
}

const fixtures = JSON.parse(
  readFileSync(
    new URL('../../../schema/fixtures/roots-responses.json', import.meta.url),
    'utf8',
  ),
) as Fixtures;

// A definition of the schema is validated as a reference into a clone of the
// schema's definitions, for the reason directory-fixtures.test.ts gives.
const validatorFor = (definition: string | null): Validator =>
  definition === null
    ? new Validator(structuredClone(rootsJsonSchema), '2020-12', false)
    : new Validator(
        {
          $schema: rootsJsonSchema['$schema'] as string,
          $defs: structuredClone(rootsJsonSchema['$defs']) as Record<
            string,
            unknown
          >,
          $ref: `#/$defs/${definition}`,
        },
        '2020-12',
        false,
      );

const groups: [string, CaseSet, Validator, ZodType][] = [
  ['list', fixtures.lists, validatorFor(null), rootListSchema],
  [
    'request',
    fixtures.requests,
    validatorFor('rootRequest'),
    rootRequestSchema,
  ],
  [
    'authorized',
    fixtures.authorized,
    validatorFor('authorizedRoot'),
    authorizedRootSchema,
  ],
  ['error', fixtures.errors, validatorFor('rootsError'), rootsErrorSchema],
];

describe('the project root shapes in @kpv/schema', () => {
  it('has fixtures of every kind, or it is asserting nothing', () => {
    for (const [, set] of groups) {
      expect(set.valid.length).toBeGreaterThan(0);
      expect(set.invalid.length).toBeGreaterThan(0);
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
    // A reason exactly when a root is unavailable, and a root named exactly
    // when the error is overlaps.
    for (const { body } of fixtures.lists.valid) {
      for (const root of rootListSchema.parse(body).roots) {
        expect(root.available, root.path).toBe(root.reason === undefined);
      }
    }
    for (const { body } of fixtures.errors.valid) {
      const failure = rootsErrorSchema.parse(body);
      expect(failure.error === 'overlaps').toBe(failure.root !== undefined);
    }
  });

  it('types the parsed answers, so a picker can branch on them', () => {
    const list = rootListSchema.parse(fixtures.lists.valid[0]?.body);
    const available: ProjectRoot['available'] =
      list.roots[0]?.available ?? false;
    const roots: RootList['roots'][number]['path'][] = list.roots.map(
      (root) => root.path,
    );
    expect(available).toBe(true);
    expect(roots).toContain('/home/user/code/app');
    const failure = rootsErrorSchema.parse(fixtures.errors.valid[2]?.body);
    const kind: RootsError['error'] = failure.error;
    expect(kind).toBe('overlaps');
    expect(failure.root).toBe('/home/user/code/app');
  });
});
