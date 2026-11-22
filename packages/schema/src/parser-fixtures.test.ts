// The TypeScript half of the shared parser contract fixtures (#37). The Go
// half is agent/internal/parser/fixtures_test.go and reads the same file, so
// what a parser may answer is held to the same cases on both sides.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';

import type { ParserResult } from './index.js';
import {
  parserJsonSchema,
  parserResultSchema,
  parserStageTypeSchema,
  pipelineJsonSchema,
} from './index.js';

interface Case {
  name: string;
  why: string;
  result: unknown;
}

interface Fixtures {
  valid: Case[];
  invalid: Case[];
}

const fixtures = JSON.parse(
  readFileSync(
    new URL('../../../schema/fixtures/parser-results.json', import.meta.url),
    'utf8',
  ),
) as Fixtures;

const validator = new Validator(
  structuredClone(parserJsonSchema),
  '2020-12',
  false,
);

describe('the parser contract in @kpv/schema', () => {
  it('has fixtures of both kinds, or it is asserting nothing', () => {
    expect(fixtures.valid.length).toBeGreaterThan(0);
    expect(fixtures.invalid.length).toBeGreaterThan(0);
  });

  describe('a valid result', () => {
    it.each(fixtures.valid)('is accepted: $name', ({ result, why }) => {
      expect(validator.validate(result).valid, why).toBe(true);
      const parsed = parserResultSchema.safeParse(result);
      expect(parsed.error?.issues ?? [], why).toEqual([]);
      expect(parsed.success).toBe(true);
    });
  });

  describe('an invalid result', () => {
    it.each(fixtures.invalid)('is refused: $name', ({ result, why }) => {
      expect(validator.validate(result).valid, why).toBe(false);
      expect(parserResultSchema.safeParse(result).success, why).toBe(false);
    });
  });

  it('names exactly the step types of the pipeline schema', () => {
    const defs = pipelineJsonSchema['$defs'] as Record<
      string,
      { properties?: { type?: { enum?: unknown[] } } }
    >;
    const discriminators = Object.values(defs)
      .map((node) => node.properties?.type?.enum)
      .filter((values) => values?.length === 1)
      .map((values) => values?.[0]);
    expect([...parserStageTypeSchema.options].sort()).toEqual(
      [...discriminators].sort(),
    );
  });

  it('parses a result written as the generated type unchanged', () => {
    // A literal of the generated type is what fails the typecheck if the
    // types and the schema drift apart, and parsing it changes nothing.
    const result: ParserResult = {
      contract: 1,
      status: 'parsed',
      stages: [
        {
          type: 'kafkaSource',
          range: { start: 0, end: 9 },
          configuration: { topic: 'é' },
        },
      ],
      unrecognized: [{ range: { start: 9, end: 10 }, reason: 'a newline' }],
      warnings: [{ message: 'not ASCII', range: { start: 5, end: 7 } }],
    };
    expect(parserResultSchema.parse(result)).toEqual(result);
    expect(validator.validate(result).valid).toBe(true);
  });
});
