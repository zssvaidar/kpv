// The TypeScript half of the shared fixtures. The Go half is
// agent/internal/pipeline/fixtures_test.go and reads the same file, so a
// fixture only has to be written once and both sides are held to it.

import { readFileSync } from 'node:fs';

import { describe, expect, it } from 'vitest';

import type { z } from 'zod';

import type { Pipeline } from './index.js';
import {
  pipelineJsonSchema,
  pipelineSchema,
  pipelineZodSchema,
  validatePipeline,
} from './index.js';

interface ExpectedError {
  instanceLocation: string;
  keywordLocation: string;
}

interface Fixtures {
  valid: { name: string; why: string; pipeline: unknown }[];
  invalid: {
    name: string;
    why: string;
    errors: ExpectedError[];
    pipeline: unknown;
  }[];
}

const read = (path: string): string =>
  readFileSync(new URL(path, import.meta.url), 'utf8');

const fixtures = JSON.parse(
  read('../../../schema/fixtures/pipelines.json'),
) as Fixtures;

/**
 * Whether two JSON Pointers point at the same place or at one inside the
 * other. Zod reports a union failure at the step and the JSON Schema validator
 * at the exact field, so this is the honest comparison between them; the
 * pointer-for-pointer assertions are the ones on validatePipeline.
 */
const related = (a: string, b: string): boolean =>
  a === b ||
  a.startsWith(b === '' ? '/' : `${b}/`) ||
  b.startsWith(a === '' ? '/' : `${a}/`);

const zodIssuePointers = (pipeline: unknown): string[] => {
  const result = pipelineZodSchema.safeParse(pipeline);
  if (result.success) return [];
  return result.error.issues.map((issue) =>
    issue.path.length === 0 ? '' : `/${issue.path.join('/')}`,
  );
};

describe('the schema shipped in @kpv/schema', () => {
  it('is the authoritative file, verbatim', () => {
    expect(pipelineJsonSchema).toEqual(
      JSON.parse(read('../../../schema/pipeline.schema.json')),
    );
  });
});

describe('valid fixtures', () => {
  it.each(fixtures.valid.map((fixture) => [fixture.name, fixture] as const))(
    '%s passes the JSON Schema and the generated Zod schema',
    (_name, fixture) => {
      expect(validatePipeline(fixture.pipeline)).toEqual([]);

      // The annotations are the assertion: a value shaped like the generated
      // interfaces is a value the generated Zod schema accepts, so the two
      // generated descriptions of the model agree.
      const declared = fixture.pipeline as Pipeline;
      const accepted: z.input<typeof pipelineSchema> = declared;
      const parsed = pipelineZodSchema.parse(accepted);
      expect(parsed.id).toBe(declared.id);
    },
  );
});

describe('invalid fixtures', () => {
  it.each(fixtures.invalid.map((fixture) => [fixture.name, fixture] as const))(
    '%s fails at the schema path the fixture names',
    (_name, fixture) => {
      const errors = validatePipeline(fixture.pipeline);
      expect(errors.length).toBeGreaterThan(0);
      expect(fixture.errors.length).toBeGreaterThan(0);

      for (const expected of fixture.errors) {
        expect(
          errors.map(({ instanceLocation, keywordLocation }) => ({
            instanceLocation,
            keywordLocation,
          })),
        ).toContainEqual(expected);
      }
    },
  );

  it.each(fixtures.invalid.map((fixture) => [fixture.name, fixture] as const))(
    '%s is also rejected by the generated Zod schema, in the same part of the document',
    (_name, fixture) => {
      const pointers = zodIssuePointers(fixture.pipeline);
      expect(pointers.length).toBeGreaterThan(0);

      for (const expected of fixture.errors) {
        expect(
          pointers.some((pointer) =>
            related(pointer, expected.instanceLocation),
          ),
        ).toBe(true);
      }
    },
  );
});
