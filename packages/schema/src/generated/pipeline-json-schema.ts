// Code generated from schema/pipeline.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/pipeline-json-schema.ts is stale.

/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */
export const pipelineJsonSchema: Readonly<Record<string, unknown>> = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://github.com/zssvaidar/kpv/schema/pipeline.schema.json',
  title: 'Pipeline',
  description:
    'A linear Kafka message-processing pipeline: one Kafka source, zero or more ordered processing steps, and one Kafka destination. This file is the single source of truth for the pipeline model; the Go structs in the agent and the TypeScript types and Zod schema in @kpv/schema are generated from it and must never be hand-edited.',
  type: 'object',
  additionalProperties: false,
  required: ['id', 'language', 'source', 'destination', 'runtimeConfiguration'],
  properties: {
    id: {
      $ref: '#/$defs/identifier',
    },
    language: {
      $ref: '#/$defs/language',
    },
    source: {
      $ref: '#/$defs/kafkaSourceBlock',
      description: 'The single Kafka source block the pipeline consumes from.',
    },
    processingSteps: {
      description:
        'Ordered processing steps between source and destination. Absent and empty both mean a pipeline that only moves messages from its source to its destination.',
      type: 'array',
      items: {
        $ref: '#/$defs/processingStep',
      },
      'x-kpv-uniqueBy': 'id',
    },
    destination: {
      $ref: '#/$defs/kafkaDestinationBlock',
      description:
        'The single Kafka destination block the pipeline produces to.',
    },
    runtimeConfiguration: {
      $ref: '#/$defs/runtimeConfiguration',
      description: "How the pipeline's container is run locally.",
    },
  },
  $defs: {
    identifier: {
      title: 'Identifier',
      description:
        'A stable, human-readable identifier, unique among its siblings: starts with a letter, then letters, digits, hyphens or underscores.',
      type: 'string',
      pattern: '^[A-Za-z][A-Za-z0-9_-]{0,62}$',
    },
    language: {
      title: 'Language',
      description:
        "The language the pipeline's processing code is written in and generated for. The three languages the MVP supports.",
      type: 'string',
      enum: ['python', 'typescript', 'go'],
    },
    messageFormat: {
      title: 'MessageFormat',
      description:
        'Payload formats the MVP supports. A block that leaves it out is read as json.',
      type: 'string',
      enum: ['json', 'text'],
      default: 'json',
    },
    kafkaTopic: {
      title: 'KafkaTopic',
      description:
        'A Kafka topic name, using the characters Kafka itself accepts.',
      type: 'string',
      minLength: 1,
      maxLength: 249,
      pattern: '^[A-Za-z0-9._-]+$',
    },
    fieldPath: {
      title: 'FieldPath',
      description:
        'A dotted path into a JSON payload, for example order.total.',
      type: 'string',
      minLength: 1,
    },
    expression: {
      title: 'Expression',
      description:
        'A language-neutral expression over the message payload, evaluated by generated code.',
      type: 'string',
      minLength: 1,
    },
    customCodeReference: {
      title: 'CustomCodeReference',
      description:
        'Points at user-owned code that implements a step. Optional on every step: a step without it is fully described by its configuration.',
      type: 'object',
      additionalProperties: false,
      required: ['path'],
      properties: {
        path: {
          description:
            'Project-relative path of the file holding the code. Relative by construction: it cannot start with a dot or a slash.',
          type: 'string',
          minLength: 1,
          maxLength: 512,
          pattern: '^[A-Za-z0-9][A-Za-z0-9._/-]*$',
        },
        symbol: {
          description:
            'Name of the function or class inside that file, when the file holds more than one.',
          type: 'string',
          minLength: 1,
        },
      },
    },
    kafkaSourceBlock: {
      title: 'KafkaSourceBlock',
      description:
        'Block table: Kafka Source. Configures the input topic and consumer behavior. Cluster connection settings live outside the pipeline file.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'topic'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['kafkaSource'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        topic: {
          $ref: '#/$defs/kafkaTopic',
        },
        consumerGroupId: {
          description:
            'Consumer group to join. Absent means the agent generates one per pipeline.',
          type: 'string',
          minLength: 1,
          maxLength: 255,
        },
        autoOffsetReset: {
          description:
            'Where to start when the consumer group has no committed offset.',
          type: 'string',
          enum: ['earliest', 'latest'],
          default: 'latest',
        },
        messageFormat: {
          $ref: '#/$defs/messageFormat',
        },
      },
    },
    kafkaDestinationBlock: {
      title: 'KafkaDestinationBlock',
      description:
        'Block table: Kafka Destination. Configures the output topic and producer behavior.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'topic'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['kafkaDestination'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        topic: {
          $ref: '#/$defs/kafkaTopic',
        },
        acks: {
          description:
            "Producer acknowledgement setting, using Kafka's own values.",
          type: 'string',
          enum: ['0', '1', 'all'],
          default: 'all',
        },
        preserveKey: {
          description:
            'Keep the original message key unless custom code replaced it.',
          type: 'boolean',
          default: true,
        },
        messageFormat: {
          $ref: '#/$defs/messageFormat',
          default: 'json',
        },
      },
    },
    processingStep: {
      title: 'ProcessingStep',
      description:
        'One processing block between source and destination. A closed discriminated union on type: an unknown type matches no branch and fails validation instead of parsing as a partial object.',
      oneOf: [
        {
          $ref: '#/$defs/filterStep',
        },
        {
          $ref: '#/$defs/transformStep',
        },
        {
          $ref: '#/$defs/validateStep',
        },
        {
          $ref: '#/$defs/customCodeStep',
        },
      ],
    },
    filterStep: {
      title: 'FilterStep',
      description:
        'Block table: Filter. Decides whether a message continues through the pipeline.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'configuration'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['filter'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        configuration: {
          title: 'FilterConfiguration',
          type: 'object',
          additionalProperties: false,
          required: ['expression'],
          properties: {
            expression: {
              $ref: '#/$defs/expression',
            },
            keepOnMatch: {
              description: 'True keeps matching messages, false drops them.',
              type: 'boolean',
              default: true,
            },
          },
        },
        customCode: {
          $ref: '#/$defs/customCodeReference',
        },
      },
    },
    transformStep: {
      title: 'TransformStep',
      description: 'Block table: Transform / Map. Modifies a message payload.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'configuration'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['transform'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        configuration: {
          title: 'TransformConfiguration',
          type: 'object',
          additionalProperties: false,
          required: ['mappings'],
          properties: {
            mappings: {
              description:
                'Target fields and the expressions that produce them.',
              type: 'array',
              minItems: 1,
              items: {
                title: 'FieldMapping',
                type: 'object',
                additionalProperties: false,
                required: ['targetField', 'expression'],
                properties: {
                  targetField: {
                    $ref: '#/$defs/fieldPath',
                  },
                  expression: {
                    $ref: '#/$defs/expression',
                  },
                },
              },
            },
            dropUnmappedFields: {
              description:
                'True emits only mapped fields, false merges them into the incoming payload.',
              type: 'boolean',
              default: false,
            },
          },
        },
        customCode: {
          $ref: '#/$defs/customCodeReference',
        },
      },
    },
    validateStep: {
      title: 'ValidateStep',
      description:
        'Block table: Validate. Checks message conditions and handles invalid messages according to configured behavior.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'configuration'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['validate'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        configuration: {
          title: 'ValidateConfiguration',
          type: 'object',
          additionalProperties: false,
          required: ['rules', 'onInvalid'],
          properties: {
            rules: {
              description:
                'Conditions a message must satisfy. Inferred payload fields are suggestions, so a rule is only checked when it is stated here.',
              type: 'array',
              minItems: 1,
              items: {
                title: 'ValidationRule',
                type: 'object',
                additionalProperties: false,
                required: ['field'],
                properties: {
                  field: {
                    $ref: '#/$defs/fieldPath',
                  },
                  required: {
                    description: 'The field must be present.',
                    type: 'boolean',
                    default: true,
                  },
                  fieldType: {
                    description:
                      'The JSON type the field must have, when it matters.',
                    type: 'string',
                    enum: ['string', 'number', 'boolean', 'object', 'array'],
                  },
                },
              },
            },
            onInvalid: {
              description:
                'What happens to a message that fails a rule: drop it, route it to deadLetterTopic, or fail the pipeline.',
              type: 'string',
              enum: ['drop', 'deadLetter', 'fail'],
            },
            deadLetterTopic: {
              $ref: '#/$defs/kafkaTopic',
            },
          },
        },
        customCode: {
          $ref: '#/$defs/customCodeReference',
        },
      },
    },
    customCodeStep: {
      title: 'CustomCodeStep',
      description:
        'Block table: Custom Code. Executes user-defined Python, TypeScript or Go processing logic.',
      type: 'object',
      additionalProperties: false,
      required: ['id', 'type', 'configuration'],
      properties: {
        id: {
          $ref: '#/$defs/identifier',
        },
        type: {
          type: 'string',
          enum: ['customCode'],
          description:
            'Type discriminator. Exactly one value, so a step of another type matches no branch of the union.',
        },
        configuration: {
          title: 'CustomCodeConfiguration',
          type: 'object',
          additionalProperties: false,
          required: ['entrypoint'],
          properties: {
            entrypoint: {
              description:
                'Name of the function the generated runtime calls for each message.',
              type: 'string',
              minLength: 1,
              pattern: '^[A-Za-z_][A-Za-z0-9_]*$',
            },
            timeoutMs: {
              description: 'How long one message may spend in this step.',
              type: 'integer',
              minimum: 1,
              maximum: 600000,
            },
          },
        },
        customCode: {
          $ref: '#/$defs/customCodeReference',
        },
      },
    },
    runtimeConfiguration: {
      title: 'RuntimeConfiguration',
      description:
        "How the pipeline's own container is run. Values of secrets are never stored here, only the names of the variables the agent injects.",
      type: 'object',
      additionalProperties: false,
      required: ['networkMode'],
      properties: {
        networkMode: {
          description:
            'docker is explicit Docker networking, which is what a pipeline should normally use; host is a local-development escape hatch. Required, because which one a pipeline needs is not something to guess.',
          type: 'string',
          enum: ['docker', 'host'],
        },
        envFile: {
          description:
            'Project-relative .env file the agent passes to the container.',
          type: 'string',
          minLength: 1,
          maxLength: 512,
          pattern: '^[A-Za-z0-9][A-Za-z0-9._/-]*$',
        },
        environmentVariableNames: {
          description:
            'Names of environment variables the agent injects at runtime. Names only, so secrets never land in the pipeline file.',
          type: 'array',
          items: {
            type: 'string',
            pattern: '^[A-Z][A-Z0-9_]*$',
          },
          uniqueItems: true,
        },
        logLevel: {
          description: 'Log level the generated runtime starts with.',
          type: 'string',
          enum: ['debug', 'info', 'warn', 'error'],
          default: 'info',
        },
      },
    },
  },
};
