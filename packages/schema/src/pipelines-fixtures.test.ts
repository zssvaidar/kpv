// The TypeScript half of the shared pipeline endpoint fixtures (#48). The Go
// half is agent/internal/httpapi/pipelines_fixtures_test.go and reads the
// same file, so what the agent sends and takes and what the browser parses
// and sends are held to the same cases.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';
import type { ZodType } from 'zod';

import {
  createPipelineRequestSchema,
  pipelineDocumentSchema,
  pipelineListSchema,
  pipelineSchema,
  pipelinesErrorSchema,
  pipelinesJsonSchema,
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
  documents: CaseSet;
  requests: CaseSet;
  errors: CaseSet;
}

const fixtures = JSON.parse(
  readFileSync(
    new URL(
      '../../../schema/fixtures/pipelines-responses.json',
      import.meta.url,
    ),
    'utf8',
  ),
) as Fixtures;

// A definition is validated as a reference into a clone of the schema's
// definitions, for the reason directory-fixtures.test.ts gives.
const validatorFor = (definition: string | null): Validator =>
  definition === null
    ? new Validator(structuredClone(pipelinesJsonSchema), '2020-12', false)
    : new Validator(
        {
          $schema: pipelinesJsonSchema['$schema'] as string,
          $defs: structuredClone(pipelinesJsonSchema['$defs']) as Record<
            string,
            unknown
          >,
          $ref: `#/$defs/${definition}`,
        },
        '2020-12',
        false,
      );

const groups: [string, CaseSet, Validator, ZodType][] = [
  ['list', fixtures.lists, validatorFor(null), pipelineListSchema],
  [
    'document',
    fixtures.documents,
    validatorFor('pipelineDocument'),
    pipelineDocumentSchema,
  ],
  [
    'request',
    fixtures.requests,
    validatorFor('createPipelineRequest'),
    createPipelineRequestSchema,
  ],
  [
    'error',
    fixtures.errors,
    validatorFor('pipelinesError'),
    pipelinesErrorSchema,
  ],
];

describe('the pipeline endpoint shapes in @kpv/schema', () => {
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
    for (const { body } of fixtures.lists.valid) {
      for (const root of pipelineListSchema.parse(body).roots) {
        expect(root.available, root.path).toBe(root.reason === undefined);
        if (!root.available) expect(root.pipelines).toEqual([]);
        for (const entry of root.pipelines) {
          expect(entry.root).toBe(root.path);
          expect(entry.readable).toBe(entry.unreadable === undefined);
          expect(entry.readable).toBe(entry.id !== undefined);
          expect(entry.readable).toBe(entry.language !== undefined);
          if (!entry.readable) expect(entry.name).toBeUndefined();
        }
      }
    }
    for (const { body } of fixtures.errors.valid) {
      const failure = pipelinesErrorSchema.parse(body);
      expect(failure.error === 'rootUnavailable').toBe(
        failure.reason !== undefined,
      );
      expect(failure.error === 'unreadable').toBe(
        failure.unreadable !== undefined,
      );
      if (failure.problems !== undefined) expect(failure.error).toBe('invalid');
    }
  });

  it('carries an opened pipeline the pipeline schema accepts', () => {
    for (const { body, why } of fixtures.documents.valid) {
      const opened = pipelineDocumentSchema.parse(body);
      expect(pipelineSchema.safeParse(opened.pipeline).success, why).toBe(true);
    }
  });

  it('reports unavailable roots with the reasons the roots endpoints use', () => {
    const defs = (schema: Readonly<Record<string, unknown>>) =>
      schema['$defs'] as Record<string, { enum: string[] }>;
    expect(defs(pipelinesJsonSchema)['pipelineRootReason']?.enum).toEqual(
      defs(rootsJsonSchema)['rootUnavailableReason']?.enum,
    );
  });
});
