// The checked-in pipelines the graph is tested and previewed with, each one
// validated by @kpv/schema before anything draws it, the way a pipeline from the
// agent will be.
//
// Two come from the shared fixtures in schema/fixtures/pipelines.json, which
// both toolchains already hold valid: one of every block type, and a pipeline
// with zero processing steps. The long one is this package's own, because no
// shared fixture is long enough to show the graph fitting a long chain. None
// of them says where a node goes.

import type { Pipeline } from '@kpv/schema';
import { validatePipeline } from '@kpv/schema';

import shared from '../../../../schema/fixtures/pipelines.json';
import long from './long-pipeline.json';

/**
 * The value as a Pipeline once @kpv/schema has found nothing wrong with it.
 * validatePipeline is the JSON Schema itself plus its annotation rules, the
 * same outcome the agent's validation reaches; the value is passed on as it
 * is, with no defaults filled in.
 */
const validated = (what: string, value: unknown): Pipeline => {
  const errors = validatePipeline(value);
  if (errors.length > 0) {
    throw new Error(
      `${what} is not a valid pipeline: ${errors
        .map((error) => `${error.instanceLocation} ${error.message}`)
        .join('; ')}`,
    );
  }
  return value as Pipeline;
};

const sharedValid = (name: string): Pipeline => {
  const found = shared.valid.find((fixture) => fixture.name === name);
  if (found === undefined) {
    throw new Error(`schema/fixtures/pipelines.json has no valid "${name}"`);
  }
  return validated(name, found.pipeline);
};

export const graphFixtures = {
  'every-block-type': sharedValid('every-block-type'),
  'zero-processing-steps': sharedValid('zero-processing-steps'),
  long: validated('long-pipeline.json', long),
} satisfies Record<string, Pipeline>;

export type GraphFixtureName = keyof typeof graphFixtures;
