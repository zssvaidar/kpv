// The shared response fixtures (#7, #63), for the picker's tests. They are
// read with Vite's import.meta.glob, which Vitest runs, so the page's own
// TypeScript program needs no Node types to load them. The files are the ones
// the agent and @kpv/schema read; nothing is copied.
/// <reference types="vite/client" />

import type {
  DirectoryError,
  DirectoryListing,
  RootList,
  RootsError,
} from '@kpv/schema';

export interface Case<T> {
  name: string;
  why: string;
  body: T;
}

interface DirectoryFixtures {
  listings: { valid: Case<DirectoryListing>[]; invalid: Case<unknown>[] };
  errors: { valid: Case<DirectoryError>[]; invalid: Case<unknown>[] };
}

interface RootsFixtures {
  lists: { valid: Case<RootList>[]; invalid: Case<unknown>[] };
  authorized: { valid: Case<{ path: string }>[]; invalid: Case<unknown>[] };
  errors: { valid: Case<RootsError>[]; invalid: Case<unknown>[] };
}

const files = import.meta.glob<unknown>(
  '../../../../schema/fixtures/{directory,roots}-responses.json',
  { eager: true, import: 'default' },
);

function file(name: string): unknown {
  const found = files[`../../../../schema/fixtures/${name}`];
  if (found === undefined) {
    throw new Error(`schema/fixtures/${name} was not found`);
  }
  return found;
}

export const directoryFixtures = file(
  'directory-responses.json',
) as DirectoryFixtures;
export const rootsFixtures = file('roots-responses.json') as RootsFixtures;
