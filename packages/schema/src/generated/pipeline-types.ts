// Code generated from schema/pipeline.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/pipeline-types.ts is stale.
/**
 * A stable, human-readable identifier, unique among its siblings: starts with a letter, then letters, digits, hyphens or underscores.
 */
export type Identifier = string;
/**
 * The language the pipeline's processing code is written in and generated for. The three languages the MVP supports.
 */
export type Language = 'python' | 'typescript' | 'go';
/**
 * A Kafka topic name, using the characters Kafka itself accepts.
 */
export type KafkaTopic = string;
/**
 * Payload formats the MVP supports. A block that leaves it out is read as json.
 */
export type MessageFormat = 'json' | 'text';
/**
 * One processing block between source and destination. A closed discriminated union on type: an unknown type matches no branch and fails validation instead of parsing as a partial object.
 */
export type ProcessingStep =
  FilterStep | TransformStep | ValidateStep | CustomCodeStep;
/**
 * A language-neutral expression over the message payload, evaluated by generated code.
 */
export type Expression = string;
/**
 * A dotted path into a JSON payload, for example order.total.
 */
export type FieldPath = string;
/**
 * Payload formats the MVP supports. A block that leaves it out is read as json.
 */
export type MessageFormat1 = 'json' | 'text';

/**
 * A linear Kafka message-processing pipeline: one Kafka source, zero or more ordered processing steps, and one Kafka destination. This file is the single source of truth for the pipeline model; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited.
 */
export interface Pipeline {
  id: Identifier;
  language: Language;
  source: KafkaSourceBlock;
  /**
   * Ordered processing steps between source and destination. Absent and empty both mean a pipeline that only moves messages from its source to its destination.
   */
  processingSteps?: ProcessingStep[];
  destination: KafkaDestinationBlock;
  runtimeConfiguration: RuntimeConfiguration;
}
/**
 * The single Kafka source block the pipeline consumes from.
 */
export interface KafkaSourceBlock {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'kafkaSource';
  topic: KafkaTopic;
  /**
   * Consumer group to join. Absent means the agent generates one per pipeline.
   */
  consumerGroupId?: string;
  /**
   * Where to start when the consumer group has no committed offset.
   */
  autoOffsetReset?: 'earliest' | 'latest';
  messageFormat?: MessageFormat;
}
/**
 * Block table: Filter. Decides whether a message continues through the pipeline.
 */
export interface FilterStep {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'filter';
  configuration: FilterConfiguration;
  customCode?: CustomCodeReference;
}
export interface FilterConfiguration {
  expression: Expression;
  /**
   * True keeps matching messages, false drops them.
   */
  keepOnMatch?: boolean;
}
/**
 * Points at user-owned code that implements a step. Optional on every step: a step without it is fully described by its configuration.
 */
export interface CustomCodeReference {
  /**
   * Project-relative path of the file holding the code. Relative by construction: it cannot start with a dot or a slash.
   */
  path: string;
  /**
   * Name of the function or class inside that file, when the file holds more than one.
   */
  symbol?: string;
}
/**
 * Block table: Transform / Map. Modifies a message payload.
 */
export interface TransformStep {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'transform';
  configuration: TransformConfiguration;
  customCode?: CustomCodeReference;
}
export interface TransformConfiguration {
  /**
   * Target fields and the expressions that produce them.
   *
   * @minItems 1
   */
  mappings: [FieldMapping, ...FieldMapping[]];
  /**
   * True emits only mapped fields, false merges them into the incoming payload.
   */
  dropUnmappedFields?: boolean;
}
export interface FieldMapping {
  targetField: FieldPath;
  expression: Expression;
}
/**
 * Block table: Validate. Checks message conditions and handles invalid messages according to configured behavior.
 */
export interface ValidateStep {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'validate';
  configuration: ValidateConfiguration;
  customCode?: CustomCodeReference;
}
export interface ValidateConfiguration {
  /**
   * Conditions a message must satisfy. Inferred payload fields are suggestions, so a rule is only checked when it is stated here.
   *
   * @minItems 1
   */
  rules: [ValidationRule, ...ValidationRule[]];
  /**
   * What happens to a message that fails a rule: drop it, route it to deadLetterTopic, or fail the pipeline.
   */
  onInvalid: 'drop' | 'deadLetter' | 'fail';
  deadLetterTopic?: KafkaTopic;
}
export interface ValidationRule {
  field: FieldPath;
  /**
   * The field must be present.
   */
  required?: boolean;
  /**
   * The JSON type the field must have, when it matters.
   */
  fieldType?: 'string' | 'number' | 'boolean' | 'object' | 'array';
}
/**
 * Block table: Custom Code. Executes user-defined Python, TypeScript or Go processing logic.
 */
export interface CustomCodeStep {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'customCode';
  configuration: CustomCodeConfiguration;
  customCode?: CustomCodeReference;
}
export interface CustomCodeConfiguration {
  /**
   * Name of the function the generated runtime calls for each message.
   */
  entrypoint: string;
  /**
   * How long one message may spend in this step.
   */
  timeoutMs?: number;
}
/**
 * The single Kafka destination block the pipeline produces to.
 */
export interface KafkaDestinationBlock {
  id: Identifier;
  /**
   * Type discriminator. Exactly one value, so a step of another type matches no branch of the union.
   */
  type: 'kafkaDestination';
  topic: KafkaTopic;
  /**
   * Producer acknowledgement setting, using Kafka's own values.
   */
  acks?: '0' | '1' | 'all';
  /**
   * Keep the original message key unless custom code replaced it.
   */
  preserveKey?: boolean;
  messageFormat?: MessageFormat1;
}
/**
 * How the pipeline's container is run locally.
 */
export interface RuntimeConfiguration {
  /**
   * docker is explicit Docker networking, which is what a pipeline should normally use; host is a local-development escape hatch. Required, because which one a pipeline needs is not something to guess.
   */
  networkMode: 'docker' | 'host';
  /**
   * Project-relative .env file the agent passes to the container.
   */
  envFile?: string;
  /**
   * Names of environment variables the agent injects at runtime. Names only, so secrets never land in the pipeline file.
   */
  environmentVariableNames?: string[];
  /**
   * Log level the generated runtime starts with.
   */
  logLevel?: 'debug' | 'info' | 'warn' | 'error';
}
