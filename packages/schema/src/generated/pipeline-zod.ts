// Code generated from schema/pipeline.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/pipeline-zod.ts is stale.

import { z } from 'zod';

export const identifierSchema = z
  .string()
  .regex(new RegExp('^[A-Za-z][A-Za-z0-9_-]{0,62}$'))
  .describe(
    'A stable, human-readable identifier, unique among its siblings: starts with a letter, then letters, digits, hyphens or underscores.',
  );

export const languageSchema = z
  .enum(['python', 'typescript', 'go'])
  .describe(
    "The language the pipeline's processing code is written in and generated for. The three languages the MVP supports.",
  );

export const messageFormatSchema = z
  .enum(['json', 'text'])
  .describe(
    'Payload formats the MVP supports. A block that leaves it out is read as json.',
  )
  .default('json');

export const kafkaTopicSchema = z
  .string()
  .regex(new RegExp('^[A-Za-z0-9._-]+$'))
  .min(1)
  .max(249)
  .describe('A Kafka topic name, using the characters Kafka itself accepts.');

export const fieldPathSchema = z
  .string()
  .min(1)
  .describe('A dotted path into a JSON payload, for example order.total.');

export const expressionSchema = z
  .string()
  .min(1)
  .describe(
    'A language-neutral expression over the message payload, evaluated by generated code.',
  );

export const customCodeReferenceSchema = z
  .object({
    path: z
      .string()
      .regex(new RegExp('^[A-Za-z0-9][A-Za-z0-9._/-]*$'))
      .min(1)
      .max(512)
      .describe(
        'Project-relative path of the file holding the code. Relative by construction: it cannot start with a dot or a slash.',
      ),
    symbol: z
      .string()
      .min(1)
      .describe(
        'Name of the function or class inside that file, when the file holds more than one.',
      )
      .optional(),
  })
  .strict()
  .describe(
    'Points at user-owned code that implements a step. Optional on every step: a step without it is fully described by its configuration.',
  );

export const kafkaSourceBlockSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('kafkaSource')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    topic: kafkaTopicSchema,
    consumerGroupId: z
      .string()
      .min(1)
      .max(255)
      .describe(
        'Consumer group to join. Absent means the agent generates one per pipeline.',
      )
      .optional(),
    autoOffsetReset: z
      .enum(['earliest', 'latest'])
      .describe(
        'Where to start when the consumer group has no committed offset.',
      )
      .default('latest'),
    messageFormat: messageFormatSchema.optional(),
  })
  .strict()
  .describe(
    'Block table: Kafka Source. Configures the input topic and consumer behavior. Cluster connection settings live outside the pipeline file.',
  );

export const kafkaDestinationBlockSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('kafkaDestination')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    topic: kafkaTopicSchema,
    acks: z
      .enum(['0', '1', 'all'])
      .describe("Producer acknowledgement setting, using Kafka's own values.")
      .default('all'),
    preserveKey: z
      .boolean()
      .describe('Keep the original message key unless custom code replaced it.')
      .default(true),
    messageFormat: messageFormatSchema,
  })
  .strict()
  .describe(
    'Block table: Kafka Destination. Configures the output topic and producer behavior.',
  );

export const filterStepSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('filter')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    configuration: z
      .object({
        expression: expressionSchema,
        keepOnMatch: z
          .boolean()
          .describe('True keeps matching messages, false drops them.')
          .default(true),
      })
      .strict(),
    customCode: customCodeReferenceSchema.optional(),
  })
  .strict()
  .describe(
    'Block table: Filter. Decides whether a message continues through the pipeline.',
  );

export const transformStepSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('transform')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    configuration: z
      .object({
        mappings: z
          .array(
            z
              .object({
                targetField: fieldPathSchema,
                expression: expressionSchema,
              })
              .strict(),
          )
          .min(1)
          .describe('Target fields and the expressions that produce them.'),
        dropUnmappedFields: z
          .boolean()
          .describe(
            'True emits only mapped fields, false merges them into the incoming payload.',
          )
          .default(false),
      })
      .strict(),
    customCode: customCodeReferenceSchema.optional(),
  })
  .strict()
  .describe('Block table: Transform / Map. Modifies a message payload.');

export const validateStepSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('validate')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    configuration: z
      .object({
        rules: z
          .array(
            z
              .object({
                field: fieldPathSchema,
                required: z
                  .boolean()
                  .describe('The field must be present.')
                  .default(true),
                fieldType: z
                  .enum(['string', 'number', 'boolean', 'object', 'array'])
                  .describe(
                    'The JSON type the field must have, when it matters.',
                  )
                  .optional(),
              })
              .strict(),
          )
          .min(1)
          .describe(
            'Conditions a message must satisfy. Inferred payload fields are suggestions, so a rule is only checked when it is stated here.',
          ),
        onInvalid: z
          .enum(['drop', 'deadLetter', 'fail'])
          .describe(
            'What happens to a message that fails a rule: drop it, route it to deadLetterTopic, or fail the pipeline.',
          ),
        deadLetterTopic: kafkaTopicSchema.optional(),
      })
      .strict(),
    customCode: customCodeReferenceSchema.optional(),
  })
  .strict()
  .describe(
    'Block table: Validate. Checks message conditions and handles invalid messages according to configured behavior.',
  );

export const customCodeStepSchema = z
  .object({
    id: identifierSchema,
    type: z
      .literal('customCode')
      .describe(
        'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
      ),
    configuration: z
      .object({
        entrypoint: z
          .string()
          .regex(new RegExp('^[A-Za-z_][A-Za-z0-9_]*$'))
          .min(1)
          .describe(
            'Name of the function the generated runtime calls for each message.',
          ),
        timeoutMs: z
          .number()
          .int()
          .gte(1)
          .lte(600000)
          .describe('How long one message may spend in this step.')
          .optional(),
      })
      .strict(),
    customCode: customCodeReferenceSchema.optional(),
  })
  .strict()
  .describe(
    'Block table: Custom Code. Executes user-defined Python, TypeScript or Go processing logic.',
  );

export const processingStepSchema = z
  .discriminatedUnion('type', [
    filterStepSchema,
    transformStepSchema,
    validateStepSchema,
    customCodeStepSchema,
  ])
  .describe(
    'One processing block between source and destination. A closed discriminated union on type: an unknown type matches no branch and fails validation instead of parsing as a partial object.',
  );

export const runtimeConfigurationSchema = z
  .object({
    networkMode: z
      .enum(['docker', 'host'])
      .describe(
        'docker is explicit Docker networking, which is what a pipeline should normally use; host is a local-development escape hatch. Required, because which one a pipeline needs is not something to guess.',
      ),
    envFile: z
      .string()
      .regex(new RegExp('^[A-Za-z0-9][A-Za-z0-9._/-]*$'))
      .min(1)
      .max(512)
      .describe('Project-relative .env file the agent passes to the container.')
      .optional(),
    environmentVariableNames: z
      .array(z.string().regex(new RegExp('^[A-Z][A-Z0-9_]*$')))
      .refine(
        (arr) => arr.every((item, i) => arr.indexOf(item) == i),
        'All items must be unique!',
      )
      .describe(
        'Names of environment variables the agent injects at runtime. Names only, so secrets never land in the pipeline file.',
      )
      .optional(),
    logLevel: z
      .enum(['debug', 'info', 'warn', 'error'])
      .describe('Log level the generated runtime starts with.')
      .default('info'),
  })
  .strict()
  .describe(
    "How the pipeline's own container is run. Values of secrets are never stored here, only the names of the variables the agent injects.",
  );

export const pipelineSchema = z
  .object({
    id: identifierSchema,
    language: languageSchema,
    source: kafkaSourceBlockSchema,
    processingSteps: z
      .array(processingStepSchema)
      .describe(
        'Ordered processing steps between source and destination. Absent and empty both mean a pipeline that only moves messages from its source to its destination.',
      )
      .optional(),
    destination: kafkaDestinationBlockSchema,
    runtimeConfiguration: runtimeConfigurationSchema,
  })
  .strict()
  .describe(
    'A linear Kafka message-processing pipeline: one Kafka source, zero or more ordered processing steps, and one Kafka destination. This file is the single source of truth for the pipeline model; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited.',
  );
