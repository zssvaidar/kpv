// The shared pipeline endpoint fixtures (#48), for the list's tests, read the
// way picker/fixtures.ts reads its own.
/// <reference types="vite/client" />

import type {
  PipelineDocument,
  PipelineList,
  PipelinesError,
} from '@kpv/schema';

import type { Case } from '../picker/fixtures.ts';

interface PipelinesFixtures {
  lists: { valid: Case<PipelineList>[]; invalid: Case<unknown>[] };
  documents: { valid: Case<PipelineDocument>[]; invalid: Case<unknown>[] };
  errors: { valid: Case<PipelinesError>[]; invalid: Case<unknown>[] };
}

const files = import.meta.glob<unknown>(
  '../../../../schema/fixtures/pipelines-responses.json',
  { eager: true, import: 'default' },
);

const found = files['../../../../schema/fixtures/pipelines-responses.json'];
if (found === undefined) {
  throw new Error('schema/fixtures/pipelines-responses.json was not found');
}

export const pipelinesFixtures = found as PipelinesFixtures;

export function fixtureList(name: string): PipelineList {
  const list = pipelinesFixtures.lists.valid.find((c) => c.name === name);
  if (list === undefined) throw new Error(name);
  return list.body;
}
