// Code generated from schema/connection.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/connection-zod.ts is stale.

import { z } from 'zod';

export const connectionNameSchema = z
  .string()
  .regex(new RegExp('^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$'))
  .min(1)
  .max(64)
  .describe(
    "What a connection is called, and what a pipeline's Kafka block names it by: a letter or digit, then letters, digits, dots, underscores or hyphens, at most 64 characters. Two connections may not have names that differ only in letter case. schema/pipeline.schema.json $defs/kafkaConnectionName holds the same rule, and generation fails when the two differ.",
  );

export const bootstrapServerSchema = z
  .string()
  .regex(
    new RegExp(
      '^(?:\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)*):(?:[1-9][0-9]{0,3}|[1-5][0-9]{4}|6[0-4][0-9]{3}|65[0-4][0-9]{2}|655[0-2][0-9]|6553[0-5])$',
    ),
  )
  .min(1)
  .max(261)
  .describe(
    'One broker to connect to first, as host:port. The host is a DNS name, an IPv4 address, or an IPv6 address in brackets ([::1]:9092); the port is 1 to 65535 with no leading zero.',
  );

export const bootstrapServersSchema = z
  .array(bootstrapServerSchema)
  .min(1)
  .max(64)
  .describe(
    'The brokers to connect to first, at least one. No entry may appear twice, compared ignoring letter case; the later one is refused at its own index (a combination rule, see the top-level description).',
  );

export const securityProtocolSchema = z
  .enum(['PLAINTEXT', 'SSL', 'SASL_PLAINTEXT', 'SASL_SSL'])
  .describe(
    "Kafka's security.protocol. PLAINTEXT: no TLS and no SASL. SSL: TLS, no SASL. SASL_PLAINTEXT: SASL without TLS. SASL_SSL: SASL over TLS. SASL fields are allowed, and the mechanism, username and password required, exactly for SASL_PLAINTEXT and SASL_SSL; TLS fields are allowed exactly for SSL and SASL_SSL.",
  );

export const saslMechanismSchema = z
  .enum(['PLAIN', 'SCRAM-SHA-256', 'SCRAM-SHA-512'])
  .describe("Kafka's sasl.mechanism.");

export const saslUsernameSchema = z
  .string()
  .min(1)
  .max(1024)
  .describe('The SASL username. Not a credential: it is answered back.');

export const saslPasswordSchema = z
  .string()
  .min(1)
  .max(1024)
  .describe('The SASL password, a credential: write-only, never answered.');

export const certificatePemSchema = z
  .string()
  .min(1)
  .max(65536)
  .describe(
    'One or more PEM CERTIFICATE blocks, as text held in the connection, never a path on disk. The agent refuses text that does not parse as certificates, with a message that does not quote it.',
  );

export const privateKeyPemSchema = z
  .string()
  .min(1)
  .max(65536)
  .describe(
    'One PEM private key block (PRIVATE KEY, RSA PRIVATE KEY or EC PRIVATE KEY), unencrypted, as text held in the connection. A credential: write-only, never answered. The agent refuses text that does not parse as a private key, with a message that does not quote it.',
  );

export const credentialFieldSchema = z
  .enum(['saslPassword', 'tlsClientKey'])
  .describe('A credential an update can remove.');

export const connectionSchema = z
  .object({
    name: connectionNameSchema,
    bootstrapServers: bootstrapServersSchema,
    securityProtocol: securityProtocolSchema,
    saslMechanism: saslMechanismSchema.optional(),
    saslUsername: saslUsernameSchema.optional(),
    saslPassword: saslPasswordSchema.optional(),
    tlsCaCertificate: certificatePemSchema.optional(),
    tlsClientCertificate: certificatePemSchema.optional(),
    tlsClientKey: privateKeyPemSchema.optional(),
  })
  .strict()
  .describe(
    'A connection record with its credentials, and the body of POST /api/connections, which creates one. Sent as Content-Type: application/json, at most 262144 bytes, each field once and spelled exactly. A SASL mechanism, username or password is allowed exactly with SASL_PLAINTEXT and SASL_SSL, which need all three; a CA certificate, client certificate or client key is allowed exactly with SSL and SASL_SSL; a client certificate and its key come together.',
  );

export const connectionUpdateSchema = z
  .object({
    current: connectionNameSchema,
    name: connectionNameSchema,
    bootstrapServers: bootstrapServersSchema,
    securityProtocol: securityProtocolSchema,
    saslMechanism: saslMechanismSchema.optional(),
    saslUsername: saslUsernameSchema.optional(),
    saslPassword: saslPasswordSchema.optional(),
    tlsCaCertificate: certificatePemSchema.optional(),
    tlsClientCertificate: certificatePemSchema.optional(),
    tlsClientKey: privateKeyPemSchema.optional(),
    remove: z
      .array(credentialFieldSchema)
      .refine(
        (arr) => arr.every((item, i) => arr.indexOf(item) == i),
        'All items must be unique!',
      )
      .describe(
        'The credentials to remove. Removing one that is not stored changes nothing.',
      )
      .optional(),
    confirm: z
      .boolean()
      .describe(
        'True goes ahead with a rename that some pipeline names or that could not be checked, after the referenced error reported them. The pipelines keep the old name.',
      )
      .optional(),
  })
  .strict()
  .describe(
    'The body of PUT /api/connections, which changes the connection named current. Every field but the credentials is the connection as it should be, so a field left out is unset. A credential left out is kept, a credential given replaces the stored one, and a credential listed in remove is removed; an empty string is refused, and a credential both given and listed is refused. A protocol without SASL drops the stored password, and one without TLS drops the stored client key, whatever remove says. A name other than current renames the connection and keeps its credentials; a rename that some pipeline names, or that cannot be checked, is refused with a referenced error until it is sent again with confirm true. The combination rules of Connection hold for the connection this makes. Same size cap and key rules as Connection.',
  );

export const connectionDeleteSchema = z
  .object({
    name: connectionNameSchema,
    confirm: z
      .boolean()
      .describe(
        'True deletes even though the referenced error reported references or unchecked pipelines. The pipelines keep the name, which then names no connection.',
      )
      .optional(),
  })
  .strict()
  .describe(
    'The body of DELETE /api/connections. A connection some pipeline names, or whose references could not all be checked, is refused with a referenced error until it is sent again with confirm true. Same size cap and key rules as Connection.',
  );

export const connectionSummarySchema = z
  .object({
    name: connectionNameSchema,
    bootstrapServers: bootstrapServersSchema,
    securityProtocol: securityProtocolSchema,
    saslMechanism: saslMechanismSchema.optional(),
    saslUsername: saslUsernameSchema.optional(),
    saslPasswordSet: z.boolean().describe('Whether a SASL password is stored.'),
    tlsCaCertificate: certificatePemSchema.optional(),
    tlsClientCertificate: certificatePemSchema.optional(),
    tlsClientKeySet: z
      .boolean()
      .describe('Whether a client private key is stored.'),
  })
  .strict()
  .describe(
    'One stored connection as the agent answers it: everything but the credentials, and for each credential only whether it is set, never its value, its length or a hash of it.',
  );

export const connectionNameListSchema = z
  .object({ names: z.array(connectionNameSchema) })
  .strict()
  .describe(
    'The answer to GET /api/connections/names: the name of every stored connection and nothing else, sorted ignoring letter case. Empty when there are none. A pipeline block naming a connection that is not in it names a connection that does not exist.',
  );

export const connectionProblemSchema = z
  .object({
    pointer: z
      .string()
      .describe('A JSON Pointer into the request body, "" for the whole body.'),
    message: z.string().min(1),
  })
  .strict()
  .describe(
    "One way a request's connection is not valid: a JSON Pointer to the field in the request it is about, and a sentence that never quotes a credential or certificate.",
  );

export const connectionBlockSchema = z
  .enum(['source', 'destination'])
  .describe('Which Kafka block of a pipeline names a connection.');

export const connectionReferenceSchema = z
  .object({
    root: z.string().min(1),
    directory: z.string().min(1).optional(),
    pipelineId: z.string().min(1),
    block: connectionBlockSchema,
  })
  .strict()
  .describe(
    'A pipeline block that names the connection, found through the pipeline list (#48). directory is absent for a root that is itself the pipeline.',
  );

export const uncheckedReasonSchema = z
  .enum([
    'parse',
    'schema',
    'tooLarge',
    'link',
    'notRegularFile',
    'unreadable',
    'rootUnavailable',
    'truncated',
  ])
  .describe(
    'Why a place could not be checked for references. parse, schema, tooLarge, link, notRegularFile, unreadable: the pipeline list has the pipeline as unreadable for that reason. rootUnavailable: the root is unavailable, so none of its pipelines was read. truncated: the root holds more entries than the list looks at, so some pipelines may not have been read.',
  );

export const uncheckedPlaceSchema = z
  .object({
    root: z.string().min(1),
    directory: z.string().min(1).optional(),
    reason: uncheckedReasonSchema,
  })
  .strict()
  .describe(
    'A pipeline, or a whole root, that could not be checked for references and so may name the connection. directory is absent for a whole root, or for a root that is itself the pipeline.',
  );

export const connectionsErrorKindSchema = z
  .enum(['invalid', 'notFound', 'duplicateName', 'referenced', 'io'])
  .describe(
    'Why a connection request failed; a client branches on this, not on the message. invalid: the body is not the request sent as JSON within the size cap with each field once and spelled exactly, or its connection is not valid (problems say where). notFound: no connection has the name. duplicateName: another connection has this name, ignoring letter case. referenced: a delete or rename that some pipeline names, or that could not be fully checked; nothing was changed, and the same request with confirm true goes ahead. io: the store could not be written, or could not be read and moved aside.',
  );

export const connectionsErrorSchema = z
  .object({
    error: connectionsErrorKindSchema,
    message: z
      .string()
      .min(1)
      .describe(
        'A sentence for a person, the same for every request with this kind.',
      ),
    problems: z
      .array(connectionProblemSchema)
      .describe(
        "For invalid, every way the request's connection is not valid; empty when the body itself could not be read.",
      )
      .optional(),
    references: z
      .array(connectionReferenceSchema)
      .describe(
        'For referenced, every pipeline block that names the connection.',
      )
      .optional(),
    unchecked: z
      .array(uncheckedPlaceSchema)
      .describe(
        'For referenced, every pipeline or root that could not be checked.',
      )
      .optional(),
  })
  .strict()
  .describe(
    'The answer to a connection request that could not be served: a kind and a fixed sentence for it, and nothing from the underlying error and no credential. invalid carries problems, each naming the field it is about. referenced carries references and unchecked, at least one of them non-empty.',
  );

export const connectionListSchema = z
  .object({
    connections: z
      .array(connectionSummarySchema)
      .describe(
        'Every stored connection, sorted by name ignoring letter case.',
      ),
  })
  .strict()
  .describe(
    "Named connections to external Kafka clusters (#24), and every shape the agent's connection endpoints take and answer: GET /api/connections lists them as a ConnectionList, GET /api/connections/names answers a ConnectionNameList, POST /api/connections takes a Connection and answers a ConnectionSummary, PUT /api/connections takes a ConnectionUpdate and answers a ConnectionSummary, DELETE /api/connections takes a ConnectionDelete and answers what is left as a ConnectionList. A request that could not be served is answered with a ConnectionsError. A connection is kept by the agent in its own configuration directory, never in a pipeline file; a pipeline names it with the same name rule. Credentials (saslPassword, tlsClientKey) are write-only: a request may carry them, and no answer ever does, only whether each is set. The combination rules stated in the descriptions below (which fields a protocol allows and needs, a client certificate and its key going together, no bootstrap server twice) are not JSON Schema keywords, because the Zod generator drops if/then; they are implemented in agent/internal/connections/rules.go and packages/schema/src/connection-rules.ts and held to the same answers by schema/fixtures/connections.json. This file is the single source of truth for these shapes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.",
  );
