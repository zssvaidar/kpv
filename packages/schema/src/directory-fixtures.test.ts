// The TypeScript half of the shared directory-browsing fixtures. The Go half
// is agent/internal/httpapi/directory_fixtures_test.go and reads the same
// file, so the answer the agent sends and the answer the browser parses are
// held to the same cases.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';

import type { DirectoryError, DirectoryListing } from './index.js';
import {
  directoryErrorSchema,
  directoryJsonSchema,
  directoryListingSchema,
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
  listings: CaseSet;
  errors: CaseSet;
}

const fixtures = JSON.parse(
  readFileSync(
    new URL(
      '../../../schema/fixtures/directory-responses.json',
      import.meta.url,
    ),
    'utf8',
  ),
) as Fixtures;

const listingValidator = new Validator(directoryJsonSchema, '2020-12', false);
// The error is a definition of the same schema, so it is validated as a
// reference into that schema's definitions rather than as a copy written out
// here. The definitions are cloned because the validator annotates the schema
// objects it is given, and two validators sharing one set of them resolve
// each other's references.
const errorValidator = new Validator(
  {
    $schema: directoryJsonSchema['$schema'] as string,
    $defs: structuredClone(directoryJsonSchema['$defs']) as Record<
      string,
      unknown
    >,
    $ref: '#/$defs/directoryError',
  },
  '2020-12',
  false,
);

describe('the directory browsing answer in @kpv/schema', () => {
  it('has fixtures of every kind, or it is asserting nothing', () => {
    for (const set of [fixtures.listings, fixtures.errors]) {
      expect(set.valid.length).toBeGreaterThan(0);
      expect(set.invalid.length).toBeGreaterThan(0);
    }
  });

  describe('a valid listing', () => {
    it.each(fixtures.listings.valid)('is accepted: $name', ({ body, why }) => {
      expect(listingValidator.validate(body).valid, why).toBe(true);
      const parsed = directoryListingSchema.safeParse(body);
      expect(parsed.error?.issues ?? [], why).toEqual([]);
    });
  });

  describe('an invalid listing', () => {
    it.each(fixtures.listings.invalid)('is refused: $name', ({ body, why }) => {
      expect(listingValidator.validate(body).valid, why).toBe(false);
      expect(directoryListingSchema.safeParse(body).success, why).toBe(false);
    });
  });

  describe('a valid error', () => {
    it.each(fixtures.errors.valid)('is accepted: $name', ({ body, why }) => {
      expect(errorValidator.validate(body).valid, why).toBe(true);
      const parsed = directoryErrorSchema.safeParse(body);
      expect(parsed.error?.issues ?? [], why).toEqual([]);
    });
  });

  describe('an invalid error', () => {
    it.each(fixtures.errors.invalid)('is refused: $name', ({ body, why }) => {
      expect(errorValidator.validate(body).valid, why).toBe(false);
      expect(directoryErrorSchema.safeParse(body).success, why).toBe(false);
    });
  });

  it('types the parsed answers, so a picker can branch on them', () => {
    // The parsed listing is not assigned to DirectoryListing whole: under
    // exactOptionalPropertyTypes, Zod's optional parent is string | undefined
    // and the interface's is string. The fields a picker branches on are the
    // generated types exactly, which is what these assignments check.
    const listing = directoryListingSchema.parse(
      fixtures.listings.valid[0]?.body,
    );
    const kind: DirectoryListing['entries'][number]['kind'] =
      listing.entries[0]?.kind ?? 'unknown';
    const looksLikeProject: DirectoryListing['looksLikeProject'] =
      listing.looksLikeProject;
    expect(kind).toBe('directory');
    expect(looksLikeProject).toBe(true);
    expect(
      listing.entries.find((entry) => entry.kind === 'symlink')?.linkTarget,
    ).toBe('directory');
    const failure: DirectoryError = directoryErrorSchema.parse(
      fixtures.errors.valid[0]?.body,
    );
    expect(failure.error).toBe('forbidden');
  });
});
