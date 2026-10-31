// Code generated from schema/connection.schema.json by scripts/generate-schema.mjs.
// DO NOT EDIT. Run `pnpm run generate` instead; CI fails when packages/schema/src/generated/connection-types.ts is stale.
/**
 * What a connection is called, and what a pipeline's Kafka block names it by: a letter or digit, then letters, digits, dots, underscores or hyphens, at most 64 characters. Two connections may not have names that differ only in letter case. schema/pipeline.schema.json $defs/kafkaConnectionName holds the same rule, and generation fails when the two differ.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionName".
 */
export type ConnectionName = string;
/**
 * The brokers to connect to first, at least one. No entry may appear twice, compared ignoring letter case; the later one is refused at its own index (a combination rule, see the top-level description).
 *
 * @minItems 1
 * @maxItems 64
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "bootstrapServers".
 */
export type BootstrapServers = [BootstrapServer, ...BootstrapServer[]];
/**
 * One broker to connect to first, as host:port. The host is a DNS name, an IPv4 address, or an IPv6 address in brackets ([::1]:9092); the port is 1 to 65535 with no leading zero.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "bootstrapServer".
 */
export type BootstrapServer = string;
/**
 * Kafka's security.protocol. PLAINTEXT: no TLS and no SASL. SSL: TLS, no SASL. SASL_PLAINTEXT: SASL without TLS. SASL_SSL: SASL over TLS. SASL fields are allowed, and the mechanism, username and password required, exactly for SASL_PLAINTEXT and SASL_SSL; TLS fields are allowed exactly for SSL and SASL_SSL.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "securityProtocol".
 */
export type SecurityProtocol =
  'PLAINTEXT' | 'SSL' | 'SASL_PLAINTEXT' | 'SASL_SSL';
/**
 * Kafka's sasl.mechanism.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "saslMechanism".
 */
export type SaslMechanism = 'PLAIN' | 'SCRAM-SHA-256' | 'SCRAM-SHA-512';
/**
 * The SASL username. Not a credential: it is answered back.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "saslUsername".
 */
export type SaslUsername = string;
/**
 * One or more PEM CERTIFICATE blocks, as text held in the connection, never a path on disk. The agent refuses text that does not parse as certificates, with a message that does not quote it.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "certificatePem".
 */
export type CertificatePem = string;
/**
 * The SASL password, a credential: write-only, never answered.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "saslPassword".
 */
export type SaslPassword = string;
/**
 * One PEM private key block (PRIVATE KEY, RSA PRIVATE KEY or EC PRIVATE KEY), unencrypted, as text held in the connection. A credential: write-only, never answered. The agent refuses text that does not parse as a private key, with a message that does not quote it.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "privateKeyPem".
 */
export type PrivateKeyPem = string;
/**
 * A credential an update can remove.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "credentialField".
 */
export type CredentialField = 'saslPassword' | 'tlsClientKey';
/**
 * Which Kafka block of a pipeline names a connection.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionBlock".
 */
export type ConnectionBlock = 'source' | 'destination';
/**
 * Why a place could not be checked for references. parse, schema, tooLarge, link, notRegularFile, unreadable: the pipeline list has the pipeline as unreadable for that reason. rootUnavailable: the root is unavailable, so none of its pipelines was read. truncated: the root holds more entries than the list looks at, so some pipelines may not have been read.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "uncheckedReason".
 */
export type UncheckedReason =
  | 'parse'
  | 'schema'
  | 'tooLarge'
  | 'link'
  | 'notRegularFile'
  | 'unreadable'
  | 'rootUnavailable'
  | 'truncated';
/**
 * Why a connection request failed; a client branches on this, not on the message. invalid: the body is not the request sent as JSON within the size cap with each field once and spelled exactly, or its connection is not valid (problems say where). notFound: no connection has the name. duplicateName: another connection has this name, ignoring letter case. referenced: a delete or rename that some pipeline names, or that could not be fully checked; nothing was changed, and the same request with confirm true goes ahead. io: the store could not be written, or could not be read and moved aside.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionsErrorKind".
 */
export type ConnectionsErrorKind =
  'invalid' | 'notFound' | 'duplicateName' | 'referenced' | 'io';

/**
 * Named connections to external Kafka clusters (#24), and every shape the agent's connection endpoints take and answer: GET /api/connections lists them as a ConnectionList, GET /api/connections/names answers a ConnectionNameList, POST /api/connections takes a Connection and answers a ConnectionSummary, PUT /api/connections takes a ConnectionUpdate and answers a ConnectionSummary, DELETE /api/connections takes a ConnectionDelete and answers what is left as a ConnectionList. A request that could not be served is answered with a ConnectionsError. A connection is kept by the agent in its own configuration directory, never in a pipeline file; a pipeline names it with the same name rule. Credentials (saslPassword, tlsClientKey) are write-only: a request may carry them, and no answer ever does, only whether each is set. The combination rules stated in the descriptions below (which fields a protocol allows and needs, a client certificate and its key going together, no bootstrap server twice) are not JSON Schema keywords, because the Zod generator drops if/then; they are implemented in agent/internal/connections/rules.go and packages/schema/src/connection-rules.ts and held to the same answers by schema/fixtures/connections.json. This file is the single source of truth for these shapes; the Go structs in the agent and the TypeScript types and Zod schemas in @kpv/schema are generated from it and must never be hand-edited.
 */
export interface ConnectionList {
  /**
   * Every stored connection, sorted by name ignoring letter case.
   */
  connections: ConnectionSummary[];
}
/**
 * One stored connection as the agent answers it: everything but the credentials, and for each credential only whether it is set, never its value, its length or a hash of it.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionSummary".
 */
export interface ConnectionSummary {
  name: ConnectionName;
  bootstrapServers: BootstrapServers;
  securityProtocol: SecurityProtocol;
  saslMechanism?: SaslMechanism;
  saslUsername?: SaslUsername;
  /**
   * Whether a SASL password is stored.
   */
  saslPasswordSet: boolean;
  tlsCaCertificate?: CertificatePem;
  tlsClientCertificate?: CertificatePem;
  /**
   * Whether a client private key is stored.
   */
  tlsClientKeySet: boolean;
}
/**
 * A connection record with its credentials, and the body of POST /api/connections, which creates one. Sent as Content-Type: application/json, at most 262144 bytes, each field once and spelled exactly. A SASL mechanism, username or password is allowed exactly with SASL_PLAINTEXT and SASL_SSL, which need all three; a CA certificate, client certificate or client key is allowed exactly with SSL and SASL_SSL; a client certificate and its key come together.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connection".
 */
export interface Connection {
  name: ConnectionName;
  bootstrapServers: BootstrapServers;
  securityProtocol: SecurityProtocol;
  saslMechanism?: SaslMechanism;
  saslUsername?: SaslUsername;
  saslPassword?: SaslPassword;
  tlsCaCertificate?: CertificatePem;
  tlsClientCertificate?: CertificatePem;
  tlsClientKey?: PrivateKeyPem;
}
/**
 * The body of PUT /api/connections, which changes the connection named current. Every field but the credentials is the connection as it should be, so a field left out is unset. A credential left out is kept, a credential given replaces the stored one, and a credential listed in remove is removed; an empty string is refused, and a credential both given and listed is refused. A protocol without SASL drops the stored password, and one without TLS drops the stored client key, whatever remove says. A name other than current renames the connection and keeps its credentials; a rename that some pipeline names, or that cannot be checked, is refused with a referenced error until it is sent again with confirm true. The combination rules of Connection hold for the connection this makes. Same size cap and key rules as Connection.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionUpdate".
 */
export interface ConnectionUpdate {
  current: ConnectionName;
  name: ConnectionName;
  bootstrapServers: BootstrapServers;
  securityProtocol: SecurityProtocol;
  saslMechanism?: SaslMechanism;
  saslUsername?: SaslUsername;
  saslPassword?: SaslPassword;
  tlsCaCertificate?: CertificatePem;
  tlsClientCertificate?: CertificatePem;
  tlsClientKey?: PrivateKeyPem;
  /**
   * The credentials to remove. Removing one that is not stored changes nothing.
   */
  remove?: CredentialField[];
  /**
   * True goes ahead with a rename that some pipeline names or that could not be checked, after the referenced error reported them. The pipelines keep the old name.
   */
  confirm?: boolean;
}
/**
 * The body of DELETE /api/connections. A connection some pipeline names, or whose references could not all be checked, is refused with a referenced error until it is sent again with confirm true. Same size cap and key rules as Connection.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionDelete".
 */
export interface ConnectionDelete {
  name: ConnectionName;
  /**
   * True deletes even though the referenced error reported references or unchecked pipelines. The pipelines keep the name, which then names no connection.
   */
  confirm?: boolean;
}
/**
 * The answer to GET /api/connections/names: the name of every stored connection and nothing else, sorted ignoring letter case. Empty when there are none. A pipeline block naming a connection that is not in it names a connection that does not exist.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionNameList".
 */
export interface ConnectionNameList {
  names: ConnectionName[];
}
/**
 * One way a request's connection is not valid: a JSON Pointer to the field in the request it is about, and a sentence that never quotes a credential or certificate.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionProblem".
 */
export interface ConnectionProblem {
  /**
   * A JSON Pointer into the request body, "" for the whole body.
   */
  pointer: string;
  message: string;
}
/**
 * A pipeline block that names the connection, found through the pipeline list (#48). directory is absent for a root that is itself the pipeline.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionReference".
 */
export interface ConnectionReference {
  root: string;
  directory?: string;
  pipelineId: string;
  block: ConnectionBlock;
}
/**
 * A pipeline, or a whole root, that could not be checked for references and so may name the connection. directory is absent for a whole root, or for a root that is itself the pipeline.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "uncheckedPlace".
 */
export interface UncheckedPlace {
  root: string;
  directory?: string;
  reason: UncheckedReason;
}
/**
 * The answer to a connection request that could not be served: a kind and a fixed sentence for it, and nothing from the underlying error and no credential. invalid carries problems, each naming the field it is about. referenced carries references and unchecked, at least one of them non-empty.
 *
 * This interface was referenced by `ConnectionList`'s JSON-Schema
 * via the `definition` "connectionsError".
 */
export interface ConnectionsError {
  error: ConnectionsErrorKind;
  /**
   * A sentence for a person, the same for every request with this kind.
   */
  message: string;
  /**
   * For invalid, every way the request's connection is not valid; empty when the body itself could not be read.
   */
  problems?: ConnectionProblem[];
  /**
   * For referenced, every pipeline block that names the connection.
   */
  references?: ConnectionReference[];
  /**
   * For referenced, every pipeline or root that could not be checked.
   */
  unchecked?: UncheckedPlace[];
}
