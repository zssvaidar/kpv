// The TypeScript half of the shared source file endpoint fixtures (#87). The
// Go half is agent/internal/httpapi/sources_fixtures_test.go and reads the
// same file, so what the agent sends and takes and what the browser parses
// and sends are held to the same cases.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';
import type { ZodType } from 'zod';

import type { SourceError, SourceWriteRequest } from './index.js';
import {
  pipelineJsonSchema,
  pipelinesJsonSchema,
  sourceErrorSchema,
  sourceFileSchema,
  sourceJsonSchema,
  sourceWriteRequestSchema,
  sourceWrittenSchema,
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
  files: CaseSet;
  requests: CaseSet;
  written: CaseSet;
  errors: CaseSet;
  pairingViolations: { requests: Case[]; errors: Case[] };
}

const fixtures = JSON.parse(
  readFileSync(
    new URL('../../../schema/fixtures/source-responses.json', import.meta.url),
    'utf8',
  ),
) as Fixtures;

// A definition is validated as a reference into a clone of the schema's
// definitions, for the reason directory-fixtures.test.ts gives.
const validatorFor = (definition: string | null): Validator =>
  definition === null
    ? new Validator(structuredClone(sourceJsonSchema), '2020-12', false)
    : new Validator(
        {
          $schema: sourceJsonSchema['$schema'] as string,
          $defs: structuredClone(sourceJsonSchema['$defs']) as Record<
            string,
            unknown
          >,
          $ref: `#/$defs/${definition}`,
        },
        '2020-12',
        false,
      );

const groups: [string, CaseSet, Validator, ZodType][] = [
  ['file', fixtures.files, validatorFor(null), sourceFileSchema],
  [
    'request',
    fixtures.requests,
    validatorFor('sourceWriteRequest'),
    sourceWriteRequestSchema,
  ],
  [
    'written',
    fixtures.written,
    validatorFor('sourceWritten'),
    sourceWrittenSchema,
  ],
  ['error', fixtures.errors, validatorFor('sourceError'), sourceErrorSchema],
];

/** Whether a request keeps exactly one of its two expectations. */
const requestPaired = (request: SourceWriteRequest): boolean =>
  (request.expectedSha256 !== undefined) !==
  (request.expectedAbsent !== undefined);

/** Whether an error keeps the pairings its description states. */
const errorPaired = (failure: SourceError): boolean =>
  (failure.error === 'rootUnavailable') === (failure.reason !== undefined) &&
  (failure.sha256 === undefined || failure.error === 'changed');

describe('the source file endpoint shapes in @kpv/schema', () => {
  it('has fixtures of every kind, or it is asserting nothing', () => {
    for (const [, set] of groups) {
      expect(set.valid.length).toBeGreaterThan(0);
      expect(set.invalid.length).toBeGreaterThan(0);
    }
    expect(fixtures.pairingViolations.requests.length).toBeGreaterThan(0);
    expect(fixtures.pairingViolations.errors.length).toBeGreaterThan(0);
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
    for (const { body, why } of fixtures.requests.valid) {
      const request = sourceWriteRequestSchema.parse(
        body,
      ) as SourceWriteRequest;
      expect(requestPaired(request), why).toBe(true);
    }
    for (const { body, why } of fixtures.errors.valid) {
      const failure = sourceErrorSchema.parse(body) as SourceError;
      expect(errorPaired(failure), why).toBe(true);
    }
    for (const { body, why } of fixtures.pairingViolations.requests) {
      const parsed = sourceWriteRequestSchema.safeParse(body);
      expect(parsed.success, why).toBe(true);
      expect(requestPaired(parsed.data as SourceWriteRequest), why).toBe(false);
    }
    for (const { body, why } of fixtures.pairingViolations.errors) {
      const parsed = sourceErrorSchema.safeParse(body);
      expect(parsed.success, why).toBe(true);
      expect(errorPaired(parsed.data as SourceError), why).toBe(false);
    }
  });

  it('copies the custom-code path rule and the open route root reasons', () => {
    const defs = (schema: Readonly<Record<string, unknown>>) =>
      schema['$defs'] as Record<string, Record<string, unknown>>;
    const customCode = defs(pipelineJsonSchema)['customCodeReference'] as {
      properties: { path: Record<string, unknown> };
    };
    const own = defs(sourceJsonSchema)['sourcePath'] ?? {};
    for (const keyword of ['type', 'minLength', 'maxLength', 'pattern']) {
      expect(own[keyword]).toEqual(customCode.properties.path[keyword]);
    }
    expect(defs(sourceJsonSchema)['sourceRootReason']?.['enum']).toEqual(
      defs(pipelinesJsonSchema)['pipelineRootReason']?.['enum'],
    );
  });
});
