// @kpv/schema is the pipeline model for the TypeScript side of kpv, plus the
// envelope the agent pushes over its WebSocket channel and the answer its
// directory browsing endpoint gives.
//
// Everything about either comes from a JSON Schema in schema/, which is the
// single source of truth: the types and the Zod schemas in ./generated are
// generated from schema/pipeline.schema.json, schema/channel.schema.json and
// schema/directory.schema.json by
// `pnpm run generate` and are never hand-edited. This file only decides what
// the package exposes.

/** The name of this package, which the web skeleton reports. */
export const schemaPackageName = '@kpv/schema';

// The generated TypeScript types: Pipeline and one interface or alias per
// block type and per definition in the schema.
export type * from './generated/pipeline-types.js';

// The generated Zod schemas: pipelineSchema plus one per definition, including
// processingStepSchema, the discriminated union over the block types.
export * from './generated/pipeline-zod.js';

// The authoritative JSON Schema itself, for a JSON Schema validator.
export { pipelineJsonSchema } from './generated/pipeline-json-schema.js';

// The channel envelope (#5): the message the agent pushes over the WebSocket
// channel, and the one command a client may send back. Generated from
// schema/channel.schema.json, the same file the agent's Go structs come from.
export type * from './generated/channel-types.js';
export * from './generated/channel-zod.js';
export { channelJsonSchema } from './generated/channel-json-schema.js';

// The directory browsing answer (#7): a page of a directory, or the error a
// request that could not be served gets. Generated from
// schema/directory.schema.json, the same file the agent's Go structs come from.
export type * from './generated/directory-types.js';
export * from './generated/directory-zod.js';
export { directoryJsonSchema } from './generated/directory-json-schema.js';

// Validation: the schema plus the annotation rules it carries.
export type { PipelineValidationError } from './validate.js';
export { pipelineZodSchema, validatePipeline } from './validate.js';
export type { AnnotationViolation } from './unique-by.js';
export { uniqueByViolations } from './unique-by.js';
