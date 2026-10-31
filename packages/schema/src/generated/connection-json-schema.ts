// Code generated from schema/connection.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/connection-json-schema.ts is stale.

/** The authoritative JSON Schema, verbatim, for JSON Schema validators. */
export const connectionJsonSchema: Readonly<Record<string, unknown>> = {
  $schema: 'https://json-schema.org/draft/2020-12/schema',
  $id: 'https://github.com/zssvaidar/kpv/schema/connection.schema.json',
  title: 'ConnectionList',
  description:
    "Named connections to external Kafka clusters (#24), and every shape the agent's connection endpoints take and answer: GET /api/connections lists them as a ConnectionList, GET /api/connections/names answers a ConnectionNameList, POST /api/connections takes a Connection and answers a ConnectionSummary, PUT /api/connections takes a ConnectionUpdate and answers a ConnectionSummary, DELETE /api/connections takes a ConnectionDelete and answers what is left as a ConnectionList. A request that could not be served is answered with a ConnectionsError. A connection is kept by the agent in its own configuration directory, never in a pipeline file; a pipeline names it with the same name rule. Credentials (saslPassword, tlsClientKey) are write-only: a request may carry them, and no answer ever does, only whether each is set. The combination rules stated in the descriptions below (which fields a protocol allows and needs, a client certificate and its key going together, no bootstrap server twice) are not JSON Schema keywords, because the Zod generator drops if/then; they are implemented in agent/internal/connections/rules.go and packages/schema/src/connection-rules.ts and held to the same answers by schema/fixtures/connections.json. This file is the single source of truth for these shapes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.",
  type: 'object',
  additionalProperties: false,
  required: ['connections'],
  properties: {
    connections: {
      type: 'array',
      items: {
        $ref: '#/$defs/connectionSummary',
      },
      description:
        'Every stored connection, sorted by name ignoring letter case.',
    },
  },
  $defs: {
    connectionName: {
      title: 'ConnectionName',
      description:
        "What a connection is called, and what a pipeline's Kafka block names it by: a letter or digit, then letters, digits, dots, underscores or hyphens, at most 64 characters. Two connections may not have names that differ only in letter case. schema/pipeline.schema.json $defs/kafkaConnectionName holds the same rule, and generation fails when the two differ.",
      type: 'string',
      minLength: 1,
      maxLength: 64,
      pattern: '^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$',
    },
    bootstrapServer: {
      title: 'BootstrapServer',
      description:
        'One broker to connect to first, as host:port. The host is a DNS name, an IPv4 address, or an IPv6 address in brackets ([::1]:9092); the port is 1 to 65535 with no leading zero.',
      type: 'string',
      minLength: 1,
      maxLength: 261,
      pattern:
        '^(?:\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*):(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$',
    },
    bootstrapServers: {
      title: 'BootstrapServers',
      description:
        'The brokers to connect to first, at least one. No entry may appear twice, compared ignoring letter case; the later one is refused at its own index (a combination rule, see the top-level description).',
      type: 'array',
      minItems: 1,
      maxItems: 64,
      items: {
        $ref: '#/$defs/bootstrapServer',
      },
    },
    securityProtocol: {
      title: 'SecurityProtocol',
      description:
        "Kafka's security.protocol. PLAINTEXT: no TLS and no SASL. SSL: TLS, no SASL. SASL_PLAINTEXT: SASL without TLS. SASL_SSL: SASL over TLS. SASL fields are allowed, and the mechanism, username and password required, exactly for SASL_PLAINTEXT and SASL_SSL; TLS fields are allowed exactly for SSL and SASL_SSL.",
      type: 'string',
      enum: ['PLAINTEXT', 'SSL', 'SASL_PLAINTEXT', 'SASL_SSL'],
    },
    saslMechanism: {
      title: 'SaslMechanism',
      description: "Kafka's sasl.mechanism.",
      type: 'string',
      enum: ['PLAIN', 'SCRAM-SHA-256', 'SCRAM-SHA-512'],
    },
    saslUsername: {
      title: 'SaslUsername',
      description: 'The SASL username. Not a credential: it is answered back.',
      type: 'string',
      minLength: 1,
      maxLength: 1024,
    },
    saslPassword: {
      title: 'SaslPassword',
      description:
        'The SASL password, a credential: write-only, never answered.',
      type: 'string',
      minLength: 1,
      maxLength: 1024,
    },
    certificatePem: {
      title: 'CertificatePem',
      description:
        'One or more PEM CERTIFICATE blocks, as text held in the connection, never a path on disk. The agent refuses text that does not parse as certificates, with a message that does not quote it.',
      type: 'string',
      minLength: 1,
      maxLength: 65536,
    },
    privateKeyPem: {
      title: 'PrivateKeyPem',
      description:
        'One PEM private key block (PRIVATE KEY, RSA PRIVATE KEY or EC PRIVATE KEY), unencrypted, as text held in the connection. A credential: write-only, never answered. The agent refuses text that does not parse as a private key, with a message that does not quote it.',
      type: 'string',
      minLength: 1,
      maxLength: 65536,
    },
    credentialField: {
      title: 'CredentialField',
      description: 'A credential an update can remove.',
      type: 'string',
      enum: ['saslPassword', 'tlsClientKey'],
    },
    connection: {
      title: 'Connection',
      description:
        'A connection record with its credentials, and the body of POST /api/connections, which creates one. Sent as Content-Type: application/json, at most 262144 bytes, each field once and spelled exactly. A SASL mechanism, username or password is allowed exactly with SASL_PLAINTEXT and SASL_SSL, which need all three; a CA certificate, client certificate or client key is allowed exactly with SSL and SASL_SSL; a client certificate and its key come together.',
      type: 'object',
      additionalProperties: false,
      required: ['name', 'bootstrapServers', 'securityProtocol'],
      properties: {
        name: {
          $ref: '#/$defs/connectionName',
        },
        bootstrapServers: {
          $ref: '#/$defs/bootstrapServers',
        },
        securityProtocol: {
          $ref: '#/$defs/securityProtocol',
        },
        saslMechanism: {
          $ref: '#/$defs/saslMechanism',
        },
        saslUsername: {
          $ref: '#/$defs/saslUsername',
        },
        saslPassword: {
          $ref: '#/$defs/saslPassword',
        },
        tlsCaCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientKey: {
          $ref: '#/$defs/privateKeyPem',
        },
      },
    },
    connectionUpdate: {
      title: 'ConnectionUpdate',
      description:
        'The body of PUT /api/connections, which changes the connection named current. Every field but the credentials is the connection as it should be, so a field left out is unset. A credential left out is kept, a credential given replaces the stored one, and a credential listed in remove is removed; an empty string is refused, and a credential both given and listed is refused. A protocol without SASL drops the stored password, and one without TLS drops the stored client key, whatever remove says. A name other than current renames the connection and keeps its credentials; a rename that some pipeline names, or that cannot be checked, is refused with a referenced error until it is sent again with confirm true. The combination rules of Connection hold for the connection this makes. Same size cap and key rules as Connection.',
      type: 'object',
      additionalProperties: false,
      required: ['current', 'name', 'bootstrapServers', 'securityProtocol'],
      properties: {
        current: {
          $ref: '#/$defs/connectionName',
        },
        name: {
          $ref: '#/$defs/connectionName',
        },
        bootstrapServers: {
          $ref: '#/$defs/bootstrapServers',
        },
        securityProtocol: {
          $ref: '#/$defs/securityProtocol',
        },
        saslMechanism: {
          $ref: '#/$defs/saslMechanism',
        },
        saslUsername: {
          $ref: '#/$defs/saslUsername',
        },
        saslPassword: {
          $ref: '#/$defs/saslPassword',
        },
        tlsCaCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientKey: {
          $ref: '#/$defs/privateKeyPem',
        },
        remove: {
          type: 'array',
          items: {
            $ref: '#/$defs/credentialField',
          },
          uniqueItems: true,
          description:
            'The credentials to remove. Removing one that is not stored changes nothing.',
        },
        confirm: {
          type: 'boolean',
          description:
            'True goes ahead with a rename that some pipeline names or that could not be checked, after the referenced error reported them. The pipelines keep the old name.',
        },
      },
    },
    connectionDelete: {
      title: 'ConnectionDelete',
      description:
        'The body of DELETE /api/connections. A connection some pipeline names, or whose references could not all be checked, is refused with a referenced error until it is sent again with confirm true. Same size cap and key rules as Connection.',
      type: 'object',
      additionalProperties: false,
      required: ['name'],
      properties: {
        name: {
          $ref: '#/$defs/connectionName',
        },
        confirm: {
          type: 'boolean',
          description:
            'True deletes even though the referenced error reported references or unchecked pipelines. The pipelines keep the name, which then names no connection.',
        },
      },
    },
    connectionSummary: {
      title: 'ConnectionSummary',
      description:
        'One stored connection as the agent answers it: everything but the credentials, and for each credential only whether it is set, never its value, its length or a hash of it.',
      type: 'object',
      additionalProperties: false,
      required: [
        'name',
        'bootstrapServers',
        'securityProtocol',
        'saslPasswordSet',
        'tlsClientKeySet',
      ],
      properties: {
        name: {
          $ref: '#/$defs/connectionName',
        },
        bootstrapServers: {
          $ref: '#/$defs/bootstrapServers',
        },
        securityProtocol: {
          $ref: '#/$defs/securityProtocol',
        },
        saslMechanism: {
          $ref: '#/$defs/saslMechanism',
        },
        saslUsername: {
          $ref: '#/$defs/saslUsername',
        },
        saslPasswordSet: {
          type: 'boolean',
          description: 'Whether a SASL password is stored.',
        },
        tlsCaCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientCertificate: {
          $ref: '#/$defs/certificatePem',
        },
        tlsClientKeySet: {
          type: 'boolean',
          description: 'Whether a client private key is stored.',
        },
      },
    },
    connectionNameList: {
      title: 'ConnectionNameList',
      description:
        'The answer to GET /api/connections/names: the name of every stored connection and nothing else, sorted ignoring letter case. Empty when there are none. A pipeline block naming a connection that is not in it names a connection that does not exist.',
      type: 'object',
      additionalProperties: false,
      required: ['names'],
      properties: {
        names: {
          type: 'array',
          items: {
            $ref: '#/$defs/connectionName',
          },
        },
      },
    },
    connectionProblem: {
      title: 'ConnectionProblem',
      description:
        "One way a request's connection is not valid: a JSON Pointer to the field in the request it is about, and a sentence that never quotes a credential or certificate.",
      type: 'object',
      additionalProperties: false,
      required: ['pointer', 'message'],
      properties: {
        pointer: {
          type: 'string',
          description:
            'A JSON Pointer into the request body, "" for the whole body.',
        },
        message: {
          type: 'string',
          minLength: 1,
        },
      },
    },
    connectionBlock: {
      title: 'ConnectionBlock',
      description: 'Which Kafka block of a pipeline names a connection.',
      type: 'string',
      enum: ['source', 'destination'],
    },
    connectionReference: {
      title: 'ConnectionReference',
      description:
        'A pipeline block that names the connection, found through the pipeline list (#48). directory is absent for a root that is itself the pipeline.',
      type: 'object',
      additionalProperties: false,
      required: ['root', 'pipelineId', 'block'],
      properties: {
        root: {
          type: 'string',
          minLength: 1,
        },
        directory: {
          type: 'string',
          minLength: 1,
        },
        pipelineId: {
          type: 'string',
          minLength: 1,
        },
        block: {
          $ref: '#/$defs/connectionBlock',
        },
      },
    },
    uncheckedReason: {
      title: 'UncheckedReason',
      description:
        'Why a place could not be checked for references. parse, schema, tooLarge, link, notRegularFile, unreadable: the pipeline list has the pipeline as unreadable for that reason. rootUnavailable: the root is unavailable, so none of its pipelines was read. truncated: the root holds more entries than the list looks at, so some pipelines may not have been read.',
      type: 'string',
      enum: [
        'parse',
        'schema',
        'tooLarge',
        'link',
        'notRegularFile',
        'unreadable',
        'rootUnavailable',
        'truncated',
      ],
    },
    uncheckedPlace: {
      title: 'UncheckedPlace',
      description:
        'A pipeline, or a whole root, that could not be checked for references and so may name the connection. directory is absent for a whole root, or for a root that is itself the pipeline.',
      type: 'object',
      additionalProperties: false,
      required: ['root', 'reason'],
      properties: {
        root: {
          type: 'string',
          minLength: 1,
        },
        directory: {
          type: 'string',
          minLength: 1,
        },
        reason: {
          $ref: '#/$defs/uncheckedReason',
        },
      },
    },
    connectionsError: {
      title: 'ConnectionsError',
      description:
        'The answer to a connection request that could not be served: a kind and a fixed sentence for it, and nothing from the underlying error and no credential. invalid carries problems, each naming the field it is about. referenced carries references and unchecked, at least one of them non-empty.',
      type: 'object',
      additionalProperties: false,
      required: ['error', 'message'],
      properties: {
        error: {
          $ref: '#/$defs/connectionsErrorKind',
        },
        message: {
          type: 'string',
          minLength: 1,
          description:
            'A sentence for a person, the same for every request with this kind.',
        },
        problems: {
          type: 'array',
          items: {
            $ref: '#/$defs/connectionProblem',
          },
          description:
            "For invalid, every way the request's connection is not valid; empty when the body itself could not be read.",
        },
        references: {
          type: 'array',
          items: {
            $ref: '#/$defs/connectionReference',
          },
          description:
            'For referenced, every pipeline block that names the connection.',
        },
        unchecked: {
          type: 'array',
          items: {
            $ref: '#/$defs/uncheckedPlace',
          },
          description:
            'For referenced, every pipeline or root that could not be checked.',
        },
      },
    },
    connectionsErrorKind: {
      title: 'ConnectionsErrorKind',
      description:
        'Why a connection request failed; a client branches on this, not on the message. invalid: the body is not the request sent as JSON within the size cap with each field once and spelled exactly, or its connection is not valid (problems say where). notFound: no connection has the name. duplicateName: another connection has this name, ignoring letter case. referenced: a delete or rename that some pipeline names, or that could not be fully checked; nothing was changed, and the same request with confirm true goes ahead. io: the store could not be written, or could not be read and moved aside.',
      type: 'string',
      enum: ['invalid', 'notFound', 'duplicateName', 'referenced', 'io'],
    },
  },
};
