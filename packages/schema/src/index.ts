// @kpv/schema is the pipeline model for the TypeScript side of kpv.
//
// Everything about the model itself comes from schema/pipeline.schema.json,
// which is the single source of truth: the types and the Zod schemas in
// ./generated are generated from it by `pnpm run generate` and are never
// hand-edited. This file only decides what the package exposes.

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

// Validation: the schema plus the annotation rules it carries.
export type { PipelineValidationError } from './validate.js';
export { pipelineZodSchema, validatePipeline } from './validate.js';
export type { AnnotationViolation } from './unique-by.js';
export { uniqueByViolations } from './unique-by.js';
