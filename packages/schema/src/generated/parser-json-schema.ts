// Code generated from schema/parser.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/parser-json-schema.ts is stale.

/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */
export const parserJsonSchema: Readonly<Record<string, unknown>> = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://github.com/zssvaidar/kpv/schema/parser.schema.json',
  title: 'ParserResult',
  description:
    'What a parser reports about one source file (#37): the stages it recognized, the regions it could not, and its warnings. A parser writes exactly one of these as JSON, whether it is a sidecar process writing it to stdout or an in-process function returning it, and the agent\'s harness (agent/internal/parser) validates it the same way for both. Every range counts integer byte offsets into the exact bytes the parser was given, which are UTF-8 as catalog.ReadSource answers them, start inclusive and end exclusive. In the source x="é", é is the two bytes 0xC3 0xA9 at offsets 3 and 4, so the range covering it is {"start": 3, "end": 5}; {"start": 3, "end": 4} ends inside it and is refused. The harness also refuses what this schema cannot say: a range outside the source or on no character boundary, two stages or regions that overlap (ranges that only touch, one\'s end equal to the other\'s start, do not), an unparseable result with a stage or without a warning, a key written twice in one object, and anything after the document but whitespace. This file is the single source of truth for the contract; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.',
  type: 'object',
  additionalProperties: false,
  required: ['contract', 'status', 'stages', 'unrecognized', 'warnings'],
  properties: {
    contract: {
      description:
        'The contract version, exactly the integer 1. Any other value fails the schema, so a parser written for another version of this contract is refused rather than misread.',
      type: 'integer',
      enum: [1],
    },
    status: {
      $ref: '#/$defs/parserStatus',
    },
    stages: {
      description:
        'Every stage the parser recognized, in any order; the harness answers them sorted by start. No two overlap. Empty when the status is unparseable.',
      type: 'array',
      items: {
        $ref: '#/$defs/parserStage',
      },
    },
    unrecognized: {
      description:
        'The regions of the source the parser could not map onto a stage, in any order; the harness answers them sorted by start. None overlaps a stage or another region.',
      type: 'array',
      items: {
        $ref: '#/$defs/parserUnrecognizedRegion',
      },
    },
    warnings: {
      description:
        'What the parser has to say about the source. At least one when the status is unparseable.',
      type: 'array',
      items: {
        $ref: '#/$defs/parserWarning',
      },
    },
  },
  $defs: {
    parserStatus: {
      title: 'ParserStatus',
      description:
        'parsed when the parser read the source and reports its stages; unparseable when it could not read it at all (a syntax error, say), in which case there are no stages and at least one warning says why.',
      type: 'string',
      enum: ['parsed', 'unparseable'],
    },
    parserStageType: {
      title: 'ParserStageType',
      description:
        'The kind of pipeline step a stage is. A copy of the step type discriminators in pipeline.schema.json (kafkaSource, filter, transform, validate, customCode, kafkaDestination); generation fails when this list differs from them.',
      type: 'string',
      enum: [
        'kafkaSource',
        'filter',
        'transform',
        'validate',
        'customCode',
        'kafkaDestination',
      ],
    },
    parserRange: {
      title: 'ParserRange',
      description:
        'A region of the source as integer byte offsets into the exact UTF-8 bytes the parser was given: start inclusive, end exclusive, 0 <= start < end <= the source\'s length, and both on a character boundary. In x="é" the range of é is {"start": 3, "end": 5}, because é is two bytes.',
      type: 'object',
      additionalProperties: false,
      required: ['start', 'end'],
      properties: {
        start: {
          description: "The byte offset of the region's first byte.",
          type: 'integer',
          minimum: 0,
        },
        end: {
          description: "The byte offset just past the region's last byte.",
          type: 'integer',
          minimum: 0,
        },
      },
    },
    parserStage: {
      title: 'ParserStage',
      description: 'One pipeline step the parser recognized in the source.',
      type: 'object',
      additionalProperties: false,
      required: ['type', 'range', 'configuration'],
      properties: {
        type: {
          $ref: '#/$defs/parserStageType',
        },
        range: {
          $ref: '#/$defs/parserRange',
        },
        configuration: {
          description:
            "What the parser read of the step's settings, free-form. Mapping it onto a pipeline block is #42's.",
          type: 'object',
          additionalProperties: true,
        },
      },
    },
    parserUnrecognizedRegion: {
      title: 'ParserUnrecognizedRegion',
      description:
        'A region of the source the parser could not map onto a stage.',
      type: 'object',
      additionalProperties: false,
      required: ['range'],
      properties: {
        range: {
          $ref: '#/$defs/parserRange',
        },
        reason: {
          description:
            'Why the region was not recognized, for a person to read.',
          type: 'string',
        },
      },
    },
    parserWarning: {
      title: 'ParserWarning',
      description:
        'Something the parser has to say about the source, optionally tied to a region of it.',
      type: 'object',
      additionalProperties: false,
      required: ['message'],
      properties: {
        message: {
          description: 'The warning, for a person to read.',
          type: 'string',
          minLength: 1,
        },
        range: {
          $ref: '#/$defs/parserRange',
        },
      },
    },
  },
};
