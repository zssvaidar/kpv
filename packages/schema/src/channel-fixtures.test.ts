// The TypeScript half of the shared envelope fixtures. The Go half is
// agent/internal/channel/fixtures_test.go and reads the same file, so a
// fixture only has to be written once and both sides are held to it.
//
// What this proves that a hand-written test cannot: the envelope the browser
// will parse and the envelope the agent will send are the same envelope. The
// JSON Schema checked here is the authoritative one, the Zod schema is
// generated from it, and both are held to the cases the Go side is held to.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';

import type { ChannelMessage } from './index.js';
import { channelJsonSchema, channelMessageSchema } from './index.js';

interface Fixtures {
  valid: { name: string; why: string; message: unknown }[];
  invalid: { name: string; why: string; message: unknown }[];
}

const fixtures = JSON.parse(
  readFileSync(
    new URL('../../../schema/fixtures/channel-messages.json', import.meta.url),
    'utf8',
  ),
) as Fixtures;

const validator = new Validator(channelJsonSchema, '2020-12', false);

describe('the channel envelope in @kpv/schema', () => {
  it('has fixtures of both kinds, or it is asserting nothing', () => {
    expect(fixtures.valid.length).toBeGreaterThan(0);
    expect(fixtures.invalid.length).toBeGreaterThan(0);
  });

  describe('a valid envelope', () => {
    it.each(fixtures.valid)('is accepted: $name', ({ message, why }) => {
      expect(validator.validate(message).valid, why).toBe(true);
      const parsed = channelMessageSchema.safeParse(message);
      expect(parsed.error?.issues ?? [], why).toEqual([]);
      expect(parsed.success).toBe(true);
    });
  });

  describe('an invalid envelope', () => {
    it.each(fixtures.invalid)('is refused: $name', ({ message, why }) => {
      expect(validator.validate(message).valid, why).toBe(false);
      expect(channelMessageSchema.safeParse(message).success, why).toBe(false);
    });
  });

  it('types the parsed envelope, so a consumer branches on the kind', () => {
    const parsed = channelMessageSchema.parse(fixtures.valid[0]?.message);
    // The generated type is the contract this line is about: assigning the
    // parse result to it is what fails the typecheck if the two drift apart.
    const message: ChannelMessage = parsed;
    expect(message.kind).toBe('log');
    expect(message.pipelineId).toBe('orders-to-billing');
    expect(message.payload).toMatchObject({ line: 'consumer started' });
  });
});
