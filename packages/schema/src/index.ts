// @kpv/schema is the pipeline model for the TypeScript side of kpv, plus the
// envelope the agent pushes over its WebSocket channel, the answer its
// directory browsing endpoint gives, and what its project root and pipeline
// endpoints take and answer.
//
// Everything about either comes from a JSON Schema in schema/, which is the
// single source of truth: the types and the Zod schemas in ./generated are
// generated from schema/pipeline.schema.json, schema/channel.schema.json,
// schema/directory.schema.json, schema/roots.schema.json,
// schema/pipelines.schema.json and schema/connection.schema.json by
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

// The project root endpoints (#63): the list of authorized roots, the body a
// POST or DELETE sends, what a POST answers, and the error a request that
// could not be served gets. Generated from schema/roots.schema.json, the same
// file the agent's Go structs come from.
export type * from './generated/roots-types.js';
export * from './generated/roots-zod.js';
export { rootsJsonSchema } from './generated/roots-json-schema.js';

// The pipeline endpoints (#48): the pipeline list, an opened pipeline, the
// body a create sends, and the error a request that could not be served gets.
// Generated from schema/pipelines.schema.json, the same file the agent's Go
// structs come from.
export type * from './generated/pipelines-types.js';
export * from './generated/pipelines-zod.js';
export { pipelinesJsonSchema } from './generated/pipelines-json-schema.js';

// The connection endpoints (#24): a Kafka connection record with its
// credentials, an update and a delete, the list and the name list without
// credentials, and the error a request that could not be served gets.
// Generated from schema/connection.schema.json, the same file the agent's Go
// structs come from.
export type * from './generated/connection-types.js';
export * from './generated/connection-zod.js';
export { connectionJsonSchema } from './generated/connection-json-schema.js';

// The connection combination rules the schema states in prose, which the Go
// agent implements again in internal/connections/rules.go.
export type {
  ConnectionIssue,
  ConnectionRule,
  ConnectionRuleViolation,
  ConnectionValidationError,
} from './connection-rules.js';
export {
  connectionIssues,
  connectionRuleViolations,
  connectionZodSchema,
  protocolAllowsSasl,
  protocolAllowsTls,
  saslFields,
  tlsFields,
  validateConnection,
} from './connection-rules.js';

// Validation: the schema plus the annotation rules it carries.
export type { PipelineValidationError } from './validate.js';
export { pipelineZodSchema, validatePipeline } from './validate.js';
export type { AnnotationViolation } from './unique-by.js';
export { uniqueByViolations } from './unique-by.js';
