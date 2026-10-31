// The combination rules of a Kafka connection (#24), which
// schema/connection.schema.json states in its descriptions and not as
// keywords, because the Zod generator drops if/then. The Go agent carries the
// same rules in agent/internal/connections/rules.go; both are held to the
// same answers by schema/fixtures/connections.json.
//
// A rule looks at which fields are present, never at a credential's value, so
// the same function checks a record a person is typing, with a stored
// credential standing in as present, and the agent checks the connection an
// update makes.

import { Validator } from '@cfworker/json-schema';

import { connectionJsonSchema } from './generated/connection-json-schema.js';
import { connectionSchema } from './generated/connection-zod.js';
import type { SecurityProtocol } from './generated/connection-types.js';

/** The rules, by the id the shared fixtures name them with. */
export type ConnectionRule =
  | 'saslNotAllowed'
  | 'saslRequired'
  | 'tlsNotAllowed'
  | 'clientKeyRequired'
  | 'clientCertificateRequired'
  | 'duplicateBootstrapServer';

/** One rule a connection breaks, at the field it is about. */
export interface ConnectionRuleViolation {
  /** JSON Pointer to the field, present or the one that is missing. */
  instanceLocation: string;
  rule: ConnectionRule;
  /** A sentence naming the conflict; it never quotes a field's value. */
  message: string;
}

/** The SASL fields, in the order a missing one is reported. */
export const saslFields = [
  'saslMechanism',
  'saslUsername',
  'saslPassword',
] as const;

/** The TLS fields. */
export const tlsFields = [
  'tlsCaCertificate',
  'tlsClientCertificate',
  'tlsClientKey',
] as const;

const protocols: readonly string[] = [
  'PLAINTEXT',
  'SSL',
  'SASL_PLAINTEXT',
  'SASL_SSL',
];

/** Whether a protocol carries SASL, so allows and needs the SASL fields. */
export const protocolAllowsSasl = (protocol: SecurityProtocol): boolean =>
  protocol === 'SASL_PLAINTEXT' || protocol === 'SASL_SSL';

/** Whether a protocol uses TLS, so allows the TLS fields. */
export const protocolAllowsTls = (protocol: SecurityProtocol): boolean =>
  protocol === 'SSL' || protocol === 'SASL_SSL';

const words: Record<string, string> = {
  saslMechanism: 'a SASL mechanism',
  saslUsername: 'a SASL username',
  saslPassword: 'a SASL password',
  tlsCaCertificate: 'a CA certificate',
  tlsClientCertificate: 'a client certificate',
  tlsClientKey: 'a client key',
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === 'object' && value !== null && !Array.isArray(value);

/**
 * Every combination rule value breaks. A field counts as present when it is
 * not undefined; the protocol rules are only applied once the protocol is one
 * of the four, because a protocol the schema refuses says nothing about which
 * fields belong.
 */
export function connectionRuleViolations(
  value: unknown,
): ConnectionRuleViolation[] {
  if (!isRecord(value)) return [];
  const found: ConnectionRuleViolation[] = [];
  const has = (field: string): boolean => value[field] !== undefined;
  const protocol = value['securityProtocol'];

  if (typeof protocol === 'string' && protocols.includes(protocol)) {
    const known = protocol as SecurityProtocol;
    for (const field of saslFields) {
      if (protocolAllowsSasl(known)) {
        if (!has(field)) {
          found.push({
            instanceLocation: `/${field}`,
            rule: 'saslRequired',
            message: `${known} needs ${words[field]}`,
          });
        }
      } else if (has(field)) {
        found.push({
          instanceLocation: `/${field}`,
          rule: 'saslNotAllowed',
          message: `${words[field]} cannot be used with ${known}, which carries no SASL; choose SASL_PLAINTEXT or SASL_SSL`,
        });
      }
    }
    if (!protocolAllowsTls(known)) {
      for (const field of tlsFields) {
        if (has(field)) {
          found.push({
            instanceLocation: `/${field}`,
            rule: 'tlsNotAllowed',
            message: `${words[field]} cannot be used with ${known}, which does not use TLS; choose SSL or SASL_SSL`,
          });
        }
      }
    }
  }

  if (has('tlsClientCertificate') && !has('tlsClientKey')) {
    found.push({
      instanceLocation: '/tlsClientKey',
      rule: 'clientKeyRequired',
      message: 'a client certificate needs its private key',
    });
  }
  if (has('tlsClientKey') && !has('tlsClientCertificate')) {
    found.push({
      instanceLocation: '/tlsClientCertificate',
      rule: 'clientCertificateRequired',
      message: 'a client key needs its certificate',
    });
  }

  const servers = value['bootstrapServers'];
  if (Array.isArray(servers)) {
    const seen = new Set<string>();
    servers.forEach((server: unknown, index) => {
      if (typeof server !== 'string') return;
      const key = server.toLowerCase();
      if (seen.has(key)) {
        found.push({
          instanceLocation: `/bootstrapServers/${index}`,
          rule: 'duplicateBootstrapServer',
          message: 'this bootstrap server is already listed',
        });
      }
      seen.add(key);
    });
  }
  return found;
}

/**
 * One way a connection record fails: a schema keyword (keywordLocation, an
 * absolute pointer into schema/connection.schema.json) or a combination rule
 * (rule).
 */
export interface ConnectionValidationError {
  instanceLocation: string;
  keywordLocation?: string;
  rule?: ConnectionRule;
  message: string;
}

// The record is validated as a reference into the schema's definitions, for
// the reason directory-fixtures.test.ts gives.
const recordValidator = new Validator(
  {
    $schema: connectionJsonSchema['$schema'] as string,
    $defs: structuredClone(connectionJsonSchema['$defs']) as Record<
      string,
      unknown
    >,
    $ref: '#/$defs/connection',
  },
  '2020-12',
  false,
);

/**
 * Turns a walked keyword location (`#/$ref/properties/name/$ref/pattern`)
 * into the absolute one the Go side reports (`/$defs/connectionName/pattern`).
 */
const absoluteKeywordLocation = (walked: string): string => {
  const tokens = walked
    .replace(/^#/, '')
    .split('/')
    .filter((t) => t !== '');
  let absolute: string[] = [];
  let node: unknown = {
    $ref: '#/$defs/connection',
    $defs: connectionJsonSchema['$defs'],
  };
  const defs = connectionJsonSchema['$defs'] as Record<string, unknown>;
  for (const token of tokens) {
    if (token === '$ref' && isRecord(node) && typeof node.$ref === 'string') {
      const name = node.$ref.replace('#/$defs/', '');
      absolute = ['$defs', name];
      node = defs[name];
      continue;
    }
    if (!isRecord(node)) return walked.replace(/^#/, '');
    node = node[token];
    absolute.push(token);
  }
  return `/${absolute.join('/')}`;
};

/**
 * Every way value fails as a connection record: the JSON Schema, then the
 * combination rules. Empty means valid. The PEM text of a certificate or key
 * is only checked by the agent.
 */
export function validateConnection(
  value: unknown,
): ConnectionValidationError[] {
  const found: ConnectionValidationError[] = [];
  const seen = new Set<string>();
  for (const error of recordValidator.validate(value).errors) {
    const entry = {
      instanceLocation: error.instanceLocation.replace(/^#/, ''),
      keywordLocation: absoluteKeywordLocation(error.keywordLocation),
      message: error.error,
    };
    const key = `${entry.instanceLocation}\u0000${entry.keywordLocation}`;
    if (seen.has(key)) continue;
    seen.add(key);
    found.push(entry);
  }
  return [...found, ...connectionRuleViolations(value)];
}

/** Turns a JSON Pointer into the path shape Zod reports issues with. */
const issuePath = (pointer: string): (string | number)[] =>
  pointer
    .split('/')
    .filter((t) => t !== '')
    .map((t) => (/^(0|[1-9][0-9]*)$/.test(t) ? Number(t) : t));

/**
 * The generated connection Zod with the combination rules applied, which is
 * what the connection form checks a record with as it is typed.
 */
export const connectionZodSchema = connectionSchema.superRefine(
  (value, ctx) => {
    for (const violation of connectionRuleViolations(value)) {
      ctx.addIssue({
        code: 'custom',
        message: violation.message,
        path: issuePath(violation.instanceLocation),
      });
    }
  },
);

/** One problem with a connection being typed, at the field it is about. */
export interface ConnectionIssue {
  /** JSON Pointer to the field. */
  pointer: string;
  message: string;
}

/**
 * Every problem with value as a connection record, schema and combination
 * rules both, for a form to show next to each field. Unlike
 * connectionZodSchema, a rule is reported even while another field fails the
 * schema, so a person sees everything at once.
 */
export function connectionIssues(value: unknown): ConnectionIssue[] {
  const found: ConnectionIssue[] = [];
  const result = connectionSchema.safeParse(value);
  if (!result.success) {
    for (const issue of result.error.issues) {
      found.push({
        pointer: issue.path.length === 0 ? '' : `/${issue.path.join('/')}`,
        message: issue.message,
      });
    }
  }
  for (const violation of connectionRuleViolations(value)) {
    found.push({
      pointer: violation.instanceLocation,
      message: violation.message,
    });
  }
  return found;
}
