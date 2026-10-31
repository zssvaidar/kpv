// Code generated from schema/pipelines.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/pipelines-zod.ts is stale.

import { z } from 'zod';

export const pipelineRootReasonSchema = z
  .enum([
    'notFound',
    'notDirectory',
    'unreadable',
    'forbidden',
    'tooBroad',
    'changed',
    'overlaps',
    'io',
  ])
  .describe(
    "Why a root is not available, present exactly when available is false. The same values, with the same meaning, as the roots endpoints' RootUnavailableReason; a root found gone or unreadable while it was being scanned is reported with the reason the scan met.",
  );

export const unreadableKindSchema = z
  .enum(['parse', 'schema', 'tooLarge', 'link', 'notRegularFile', 'unreadable'])
  .describe(
    'Why pipeline.yaml did not load. parse: it is not one well-formed YAML document the JSON data model can hold. schema: it is, but it is not a valid pipeline. tooLarge: it is over 1 MiB and was not read. link: it is a symbolic link, which is never read, whatever it points to. notRegularFile: it is a directory, FIFO, socket or device. unreadable: the directory or the file could not be read or searched by the agent.',
  );

export const pipelineProblemSchema = z
  .object({
    pointer: z
      .string()
      .describe(
        'A JSON Pointer to the offending value in the document; empty for the document as a whole.',
      ),
    line: z
      .number()
      .int()
      .gte(1)
      .describe('The 1-based line in pipeline.yaml, when it is known.')
      .optional(),
    column: z
      .number()
      .int()
      .gte(1)
      .describe('The 1-based column in pipeline.yaml, when it is known.')
      .optional(),
    message: z.string().min(1),
  })
  .strict()
  .describe(
    'One problem in a pipeline document. The message describes the document, never the disk.',
  );

export const unreadablePipelineSchema = z
  .object({
    kind: unreadableKindSchema,
    problems: z
      .array(pipelineProblemSchema)
      .describe(
        'The problems in the document, each with where it is. Empty for tooLarge, link, notRegularFile and unreadable, which are about the file rather than what is in it.',
      ),
  })
  .strict()
  .describe('Why a pipeline did not load, and every problem found in it.');

export const pipelineLocationSchema = z
  .object({ root: z.string().min(1), directory: z.string().min(1).optional() })
  .strict()
  .describe(
    'Where a pipeline is: its root, and its directory inside the root, absent when the root itself is the pipeline.',
  );

export const pipelineEntrySchema = z
  .object({
    root: z.string().min(1).describe('The root the pipeline is in, as listed.'),
    directory: z
      .string()
      .min(1)
      .describe(
        "The name of the pipeline's directory inside the root. Absent when the root itself is the pipeline.",
      )
      .optional(),
    readable: z
      .boolean()
      .describe(
        'Whether pipeline.yaml loaded: it is a regular file of at most 1 MiB that parses and passes the pipeline schema.',
      ),
    id: z
      .string()
      .min(1)
      .describe("The pipeline's id, present exactly when readable is true.")
      .optional(),
    name: z
      .string()
      .min(1)
      .describe("The pipeline's name, when it is readable and has one.")
      .optional(),
    language: z
      .string()
      .min(1)
      .describe(
        "The pipeline's language, present exactly when readable is true.",
      )
      .optional(),
    modified: z
      .string()
      .min(1)
      .describe(
        'When pipeline.yaml was last modified, as an RFC 3339 UTC time. Absent when the file could not be described.',
      )
      .optional(),
    unreadable: unreadablePipelineSchema.optional(),
    duplicateId: z
      .array(pipelineLocationSchema)
      .min(1)
      .describe(
        'The other listed pipelines with the same id, compared ignoring letter case. Present only when there are any. Every one of them can still be opened.',
      )
      .optional(),
  })
  .strict()
  .describe(
    'One pipeline directory. A readable entry has its id, language and the time pipeline.yaml was last modified, and its name if the file gives one. An unreadable one has unreadable instead, and no id, name or language, because a file that did not load is not trusted for them; it is still identified by its root and directory.',
  );

export const pipelineRootSchema = z
  .object({
    path: z
      .string()
      .min(1)
      .describe(
        'The real, absolute path of the root, as GET /api/roots lists it.',
      ),
    available: z
      .boolean()
      .describe(
        'Whether the root was authorized and could be scanned for the whole of this list.',
      ),
    reason: pipelineRootReasonSchema.optional(),
    truncated: z
      .boolean()
      .describe(
        'True when the root has more directory entries than the agent examines (1000), so not every directory in it was looked at. A pipeline may exist that is not listed.',
      ),
    skippedLinks: z
      .number()
      .int()
      .gte(0)
      .describe(
        'How many symbolic links directly inside the root were not followed. A link is never listed as a pipeline, whatever it points to.',
      ),
    pipelines: z
      .array(pipelineEntrySchema)
      .describe(
        'The pipelines in the root, sorted by directory name in byte order. A root that holds pipeline.yaml itself is the one pipeline listed, with no directory, and its subdirectories are not looked at. Empty for an unavailable root.',
      ),
  })
  .strict()
  .describe(
    'One stored project root and the pipelines in it. An unavailable root has a reason and no pipelines: it is never left out, and never shown with the pipelines it had before.',
  );

export const pipelineDocumentSchema = z
  .object({
    root: z.string().min(1),
    directory: z.string().min(1).optional(),
    pipeline: z
      .record(z.string(), z.any())
      .describe(
        'The pipeline, which passed the pipeline schema in the agent. It is a schema/pipeline.schema.json document, which a $ref cannot reach from here, so the page parses it with the generated pipeline Zod before drawing it.',
      ),
    sha256: z
      .string()
      .regex(new RegExp('^[0-9a-f]{64}$'))
      .describe('The SHA-256 of the bytes of pipeline.yaml, in lowercase hex.'),
  })
  .strict()
  .describe(
    'The answer to GET /api/pipelines/open?root=<root>&directory=<directory> (directory left out for a root that is itself the pipeline), and to a successful POST /api/pipelines: one pipeline as a JSON document, and the SHA-256 of the exact bytes of pipeline.yaml it was read from, which a later save compares (#78).',
  );

export const createPipelineRequestSchema = z
  .object({
    root: z
      .string()
      .min(1)
      .describe('The root to create the pipeline in, as listed.'),
    name: z
      .string()
      .describe("The pipeline's name, the pipeline schema's name."),
    language: z.string().describe("The pipeline schema's language."),
    sourceTopic: z.string().describe("The source block's topic."),
    destinationTopic: z.string().describe("The destination block's topic."),
    networkMode: z
      .string()
      .describe("The pipeline schema's runtimeConfiguration.networkMode."),
  })
  .strict()
  .describe(
    'The body of POST /api/pipelines. It is sent as Content-Type: application/json, is at most 8192 bytes, and carries each field exactly once, spelled exactly so. The values are checked against the pipeline schema by the agent, which derives the id from the name and writes a pipeline with a source block named source, a destination block named destination and no processing steps.',
  );

export const pipelinesErrorKindSchema = z
  .enum([
    'invalid',
    'notAuthorized',
    'rootUnavailable',
    'notFound',
    'unreadable',
    'rootIsPipeline',
    'changed',
    'noFreeId',
    'io',
  ])
  .describe(
    'Why a pipeline request failed; a client branches on this, not on the message. invalid: the request is not well formed, the directory name is empty, ., .. or holds /, \\ or NUL, or a create field fails the pipeline schema. notAuthorized: the root is not an authorized project root, or was revoked. rootUnavailable: the root is stored but unavailable, which reason says why. notFound: there is no pipeline at that root and directory, or it is not one the list would show. unreadable: pipeline.yaml is there and did not load. rootIsPipeline: a create into a root that is itself a pipeline. changed: the directory a create made was replaced before it was written into, so nothing was. noFreeId: every id derived from the name is taken. io: anything else.',
  );

export const pipelinesErrorSchema = z
  .object({
    error: pipelinesErrorKindSchema,
    message: z
      .string()
      .min(1)
      .describe(
        'A sentence for a person, the same for every request with this kind.',
      ),
    reason: pipelineRootReasonSchema.optional(),
    unreadable: unreadablePipelineSchema.optional(),
    problems: z
      .array(pipelineProblemSchema)
      .min(1)
      .describe(
        'Where the pipeline a create would write fails the pipeline schema, by JSON Pointer into that pipeline.',
      )
      .optional(),
    directoryLeftBehind: z
      .boolean()
      .describe(
        'Whether the directory the create made is still there. True when it could not be shown to be the empty directory the create made, which is then never removed.',
      )
      .optional(),
  })
  .strict()
  .describe(
    "The answer to a pipeline request that could not be served: a kind and a fixed message for that kind, and nothing from the underlying error. No path is carried but the root and directory the request named. reason is present exactly when error is rootUnavailable, unreadable exactly when error is unreadable, problems only when error is invalid because a field fails the pipeline schema, and directoryLeftBehind only on a create that failed after it made the pipeline's directory.",
  );

export const pipelineListSchema = z
  .object({
    roots: z
      .array(pipelineRootSchema)
      .describe(
        'Every stored project root, in the order the roots store holds them, available or not. The list is read from disk again for every request.',
      ),
  })
  .strict()
  .describe(
    "Every pipeline under the authorized project roots, as the agent's pipeline endpoints answer it (#48): GET /api/pipelines lists them, GET /api/pipelines/open answers a PipelineDocument, and POST /api/pipelines takes a CreatePipelineRequest and answers the PipelineDocument it created. A request that could not be served is answered with a PipelinesError instead. A pipeline is a directory holding pipeline.yaml: an authorized root itself, or a directory directly inside one. It is addressed by its root and directory name, never by its id, because two directories can hold the same id. This file is the single source of truth for every request and response shape of those routes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.",
  );
