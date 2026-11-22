// Code generated from schema/parser.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/parser-zod.ts is stale.

import { z } from 'zod';

export const parserStatusSchema = z
  .enum(['parsed', 'unparseable'])
  .describe(
    'parsed when the parser read the source and reports its stages; unparseable when it could not read it at all (a syntax error, say), in which case there are no stages and at least one warning says why.',
  );

export const parserStageTypeSchema = z
  .enum([
    'kafkaSource',
    'filter',
    'transform',
    'validate',
    'customCode',
    'kafkaDestination',
  ])
  .describe(
    'The kind of pipeline step a stage is. A copy of the step type discriminators in pipeline.schema.json (kafkaSource, filter, transform, validate, customCode, kafkaDestination); generation fails when this list differs from them.',
  );

export const parserRangeSchema = z
  .object({
    start: z
      .number()
      .int()
      .gte(0)
      .describe("The byte offset of the region's first byte."),
    end: z
      .number()
      .int()
      .gte(0)
      .describe("The byte offset just past the region's last byte."),
  })
  .strict()
  .describe(
    'A region of the source as integer byte offsets into the exact UTF-8 bytes the parser was given: start inclusive, end exclusive, 0 <= start < end <= the source\'s length, and both on a character boundary. In x="é" the range of é is {"start": 3, "end": 5}, because é is two bytes.',
  );

export const parserStageSchema = z
  .object({
    type: parserStageTypeSchema,
    range: parserRangeSchema,
    configuration: z
      .record(z.string(), z.any())
      .describe(
        "What the parser read of the step's settings, free-form. Mapping it onto a pipeline block is #42's.",
      ),
  })
  .strict()
  .describe('One pipeline step the parser recognized in the source.');

export const parserUnrecognizedRegionSchema = z
  .object({
    range: parserRangeSchema,
    reason: z
      .string()
      .describe('Why the region was not recognized, for a person to read.')
      .optional(),
  })
  .strict()
  .describe('A region of the source the parser could not map onto a stage.');

export const parserWarningSchema = z
  .object({
    message: z.string().min(1).describe('The warning, for a person to read.'),
    range: parserRangeSchema.optional(),
  })
  .strict()
  .describe(
    'Something the parser has to say about the source, optionally tied to a region of it.',
  );

export const parserResultSchema = z
  .object({
    contract: z
      .literal(1)
      .describe(
        'The contract version, exactly the integer 1. Any other value fails the schema, so a parser written for another version of this contract is refused rather than misread.',
      ),
    status: parserStatusSchema,
    stages: z
      .array(parserStageSchema)
      .describe(
        'Every stage the parser recognized, in any order; the harness answers them sorted by start. No two overlap. Empty when the status is unparseable.',
      ),
    unrecognized: z
      .array(parserUnrecognizedRegionSchema)
      .describe(
        'The regions of the source the parser could not map onto a stage, in any order; the harness answers them sorted by start. None overlaps a stage or another region.',
      ),
    warnings: z
      .array(parserWarningSchema)
      .describe(
        'What the parser has to say about the source. At least one when the status is unparseable.',
      ),
  })
  .strict()
  .describe(
    'What a parser reports about one source file (#37): the stages it recognized, the regions it could not, and its warnings. A parser writes exactly one of these as JSON, whether it is a sidecar process writing it to stdout or an in-process function returning it, and the agent\'s harness (agent/internal/parser) validates it the same way for both. Every range counts integer byte offsets into the exact bytes the parser was given, which are UTF-8 as catalog.ReadSource answers them, start inclusive and end exclusive. In the source x="é", é is the two bytes 0xC3 0xA9 at offsets 3 and 4, so the range covering it is {"start": 3, "end": 5}; {"start": 3, "end": 4} ends inside it and is refused. The harness also refuses what this schema cannot say: a range outside the source or on no character boundary, two stages or regions that overlap (ranges that only touch, one\'s end equal to the other\'s start, do not), an unparseable result with a stage or without a warning, a key written twice in one object, and anything after the document but whitespace. This file is the single source of truth for the contract; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.',
  );
