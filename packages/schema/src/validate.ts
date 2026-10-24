// Validating a pipeline document on the TypeScript side.
//
// Two validators, one schema. `validatePipeline` runs the JSON Schema itself
// and reports failures the way the Go agent does, so both sides can be held to
// the same outcome. `pipelineZodSchema` is the generated Zod schema, for a
// browser that wants parsing and typed output rather than a list of locations.
// Both apply the schema's x-kpv annotation rules, so they agree.

import { Validator } from '@cfworker/json-schema';

import { pipelineJsonSchema } from './generated/pipeline-json-schema.js';
import { pipelineSchema } from './generated/pipeline-zod.js';
import type { AnnotationViolation } from './unique-by.js';
import { uniqueByViolations } from './unique-by.js';

/**
 * One way a document fails the schema, located in both the document and the
 * schema so a caller can point at the offending field and say which rule
 * rejected it. The same shape an annotation rule reports, because to a caller
 * they are the same kind of failure.
 */
export type PipelineValidationError = AnnotationViolation;

// Not in strict mode: the schema carries x-kpv annotations, which a JSON
// Schema validator is meant to ignore and this one would otherwise reject.
const validator = new Validator(pipelineJsonSchema, '2020-12', false);

const isIndexable = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null;

/** Unescapes one JSON Pointer reference token, per RFC 6901. */
const unescapePointerToken = (token: string): string =>
  token.replace(/~1/g, '/').replace(/~0/g, '~');

const pointerTokens = (pointer: string): string[] =>
  pointer
    .replace(/^#/, '')
    .split('/')
    .filter((token) => token !== '');

const resolvePointer = (tokens: string[]): unknown => {
  let node: unknown = pipelineJsonSchema;
  for (const token of tokens) {
    if (!isIndexable(node)) return undefined;
    node = node[unescapePointerToken(token)];
  }
  return node;
};

/**
 * Turns the keyword location of the standard output format, which records the
 * path the validator walked including every `$ref` it followed, into the
 * absolute location of the keyword inside the schema document: not
 * `/properties/language/$ref/enum` but `/$defs/language/enum`.
 *
 * That is the location the Go side reports, so the shared fixtures can state
 * one pointer for both. A location that does not resolve is handed back as it
 * came, which is what happens to the extra diagnostics a validator adds beyond
 * the failure itself.
 */
const absoluteKeywordLocation = (keywordLocation: string): string => {
  const fallback = keywordLocation.replace(/^#/, '');
  let absolute: string[] = [];
  let node: unknown = pipelineJsonSchema;
  for (const token of pointerTokens(keywordLocation)) {
    if (
      token === '$ref' &&
      isIndexable(node) &&
      typeof node.$ref === 'string'
    ) {
      const target = pointerTokens(node.$ref);
      const resolved = resolvePointer(target);
      if (resolved === undefined) return fallback;
      absolute = target;
      node = resolved;
      continue;
    }
    if (!isIndexable(node)) return fallback;
    const next = node[unescapePointerToken(token)];
    if (next === undefined) return fallback;
    absolute.push(token);
    node = next;
  }
  return absolute.length === 0 ? '' : `/${absolute.join('/')}`;
};

/**
 * Reports every way value fails the pipeline schema. An empty array means the
 * document is valid.
 *
 * Validation is the schema plus the x-kpv-uniqueBy annotations it carries,
 * which express a rule JSON Schema itself cannot: duplicate processing-step
 * identifiers within one pipeline.
 */
export function validatePipeline(value: unknown): PipelineValidationError[] {
  const found: PipelineValidationError[] = [];
  const seen = new Set<string>();
  const record = (error: PipelineValidationError): void => {
    const key = `${error.instanceLocation}\u0000${error.keywordLocation}`;
    if (seen.has(key)) return;
    seen.add(key);
    found.push(error);
  };

  const result = validator.validate(value);
  for (const error of result.errors) {
    record({
      instanceLocation: error.instanceLocation.replace(/^#/, ''),
      keywordLocation: absoluteKeywordLocation(error.keywordLocation),
      message: error.error,
    });
  }
  for (const violation of uniqueByViolations(pipelineJsonSchema, value)) {
    record(violation);
  }
  return found;
}

/** Turns a JSON Pointer into the path shape Zod reports issues with. */
const issuePath = (pointer: string): (string | number)[] =>
  pointerTokens(pointer).map((token) => {
    const unescaped = unescapePointerToken(token);
    return /^(0|[1-9][0-9]*)$/.test(unescaped) ? Number(unescaped) : unescaped;
  });

/**
 * The generated Zod schema with the schema's annotation rules applied, which
 * is the pipeline parser the web app should use. Parsing it yields a typed
 * Pipeline; the generated `pipelineSchema` on its own is the structure only.
 */
export const pipelineZodSchema = pipelineSchema.superRefine((value, ctx) => {
  for (const violation of uniqueByViolations(pipelineJsonSchema, value)) {
    ctx.addIssue({
      code: 'custom',
      message: violation.message,
      path: issuePath(violation.instanceLocation),
    });
  }
});
