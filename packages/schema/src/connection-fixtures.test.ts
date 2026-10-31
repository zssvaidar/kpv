// The TypeScript half of the shared connection fixtures (#24). The Go half is
// agent/internal/connections/fixtures_test.go (records and names) and
// agent/internal/httpapi/connections_fixtures_test.go (responses), which read
// the same file, so the combination rules and the shapes are held to the same
// answers in both languages.

import { readFileSync } from 'node:fs';

import { Validator } from '@cfworker/json-schema';
import { describe, expect, it } from 'vitest';
import type { ZodType } from 'zod';

import {
  connectionDeleteSchema,
  connectionIssues,
  connectionJsonSchema,
  connectionListSchema,
  connectionNameListSchema,
  connectionNameSchema,
  connectionUpdateSchema,
  connectionZodSchema,
  connectionsErrorSchema,
  kafkaConnectionNameSchema,
  pipelineJsonSchema,
  validateConnection,
} from './index.js';

interface ExpectedError {
  instanceLocation: string;
  keywordLocation?: string;
  rule?: string;
}

interface Case {
  name: string;
  why: string;
  body: unknown;
}

interface CaseSet {
  valid: Case[];
  invalid: Case[];
}

interface Fixtures {
  records: {
    valid: { name: string; why: string; connection: unknown }[];
    invalid: {
      name: string;
      why: string;
      errors: ExpectedError[];
      connection: unknown;
    }[];
  };
  names: { valid: string[]; invalid: string[] };
  responses: {
    lists: CaseSet;
    names: CaseSet;
    updates: CaseSet;
    deletes: CaseSet;
    errors: CaseSet;
  };
}

const read = (path: string): string =>
  readFileSync(new URL(path, import.meta.url), 'utf8');

const fixtures = JSON.parse(
  read('../../../schema/fixtures/connections.json'),
) as Fixtures;

const definitionValidator = (
  schema: Readonly<Record<string, unknown>>,
  definition: string | null,
): Validator =>
  definition === null
    ? new Validator(structuredClone(schema), '2020-12', false)
    : new Validator(
        {
          $schema: schema['$schema'] as string,
          $defs: structuredClone(schema['$defs']) as Record<string, unknown>,
          $ref: `#/$defs/${definition}`,
        },
        '2020-12',
        false,
      );

describe('the schema shipped in @kpv/schema', () => {
  it('is the authoritative file, verbatim', () => {
    expect(connectionJsonSchema).toEqual(
      JSON.parse(read('../../../schema/connection.schema.json')),
    );
  });
});

describe('valid connection records', () => {
  it.each(fixtures.records.valid.map((f) => [f.name, f] as const))(
    '%s passes the schema, the rules and the Zod',
    (_name, fixture) => {
      expect(validateConnection(fixture.connection)).toEqual([]);
      expect(connectionZodSchema.safeParse(fixture.connection).success).toBe(
        true,
      );
      expect(connectionIssues(fixture.connection)).toEqual([]);
    },
  );

  it('cover every protocol and every SASL mechanism on both SASL protocols', () => {
    const seen = new Set(
      fixtures.records.valid.map((f) => {
        const c = f.connection as {
          securityProtocol: string;
          saslMechanism?: string;
        };
        return `${c.securityProtocol}/${c.saslMechanism ?? ''}`;
      }),
    );
    for (const protocol of ['PLAINTEXT', 'SSL']) {
      expect(seen.has(`${protocol}/`)).toBe(true);
    }
    for (const protocol of ['SASL_PLAINTEXT', 'SASL_SSL']) {
      for (const mechanism of ['PLAIN', 'SCRAM-SHA-256', 'SCRAM-SHA-512']) {
        expect(seen.has(`${protocol}/${mechanism}`)).toBe(true);
      }
    }
  });
});

/**
 * Whether two pointers are the same place or one is inside the other: Zod
 * reports a missing property at the property and JSON Schema at its object.
 */
const related = (a: string, b: string): boolean =>
  a === b ||
  a.startsWith(b === '' ? '/' : `${b}/`) ||
  b.startsWith(a === '' ? '/' : `${a}/`);

describe('invalid connection records', () => {
  it.each(fixtures.records.invalid.map((f) => [f.name, f] as const))(
    '%s fails where the fixture says, and the form sees it there',
    (_name, fixture) => {
      expect(fixture.errors.length).toBeGreaterThan(0);
      const errors = validateConnection(fixture.connection);
      const issues = connectionIssues(fixture.connection);
      for (const expected of fixture.errors) {
        expect(
          errors.some(
            (e) =>
              e.instanceLocation === expected.instanceLocation &&
              (expected.rule === undefined || e.rule === expected.rule) &&
              (expected.keywordLocation === undefined ||
                e.keywordLocation === expected.keywordLocation),
          ),
        ).toBe(true);
        expect(
          issues.some((i) => related(i.pointer, expected.instanceLocation)),
        ).toBe(true);
      }
      expect(connectionZodSchema.safeParse(fixture.connection).success).toBe(
        false,
      );
    },
  );

  it('name the conflict in a rule message', () => {
    const errors = validateConnection({
      name: 'a',
      bootstrapServers: ['b:1'],
      securityProtocol: 'PLAINTEXT',
      saslMechanism: 'PLAIN',
    });
    expect(errors).toHaveLength(1);
    expect(errors[0]?.message).toContain('PLAINTEXT');
    expect(errors[0]?.message).toContain('SASL');
  });
});

describe('a connection name', () => {
  const inPipeline = definitionValidator(
    pipelineJsonSchema,
    'kafkaConnectionName',
  );
  const inConnection = definitionValidator(
    connectionJsonSchema,
    'connectionName',
  );
  it.each(fixtures.names.valid)('%s is valid in both schemas', (name) => {
    expect(inPipeline.validate(name).valid).toBe(true);
    expect(inConnection.validate(name).valid).toBe(true);
    expect(kafkaConnectionNameSchema.safeParse(name).success).toBe(true);
    expect(connectionNameSchema.safeParse(name).success).toBe(true);
  });
  it.each(fixtures.names.invalid)('%j is invalid in both schemas', (name) => {
    expect(inPipeline.validate(name).valid).toBe(false);
    expect(inConnection.validate(name).valid).toBe(false);
    expect(kafkaConnectionNameSchema.safeParse(name).success).toBe(false);
    expect(connectionNameSchema.safeParse(name).success).toBe(false);
  });
});

const groups: [string, CaseSet, Validator, ZodType][] = [
  [
    'list',
    fixtures.responses.lists,
    definitionValidator(connectionJsonSchema, null),
    connectionListSchema,
  ],
  [
    'name list',
    fixtures.responses.names,
    definitionValidator(connectionJsonSchema, 'connectionNameList'),
    connectionNameListSchema,
  ],
  [
    'update',
    fixtures.responses.updates,
    definitionValidator(connectionJsonSchema, 'connectionUpdate'),
    connectionUpdateSchema,
  ],
  [
    'delete',
    fixtures.responses.deletes,
    definitionValidator(connectionJsonSchema, 'connectionDelete'),
    connectionDeleteSchema,
  ],
  [
    'error',
    fixtures.responses.errors,
    definitionValidator(connectionJsonSchema, 'connectionsError'),
    connectionsErrorSchema,
  ],
];

describe.each(groups)('the %s shape', (_group, cases, validator, zod) => {
  it.each(cases.valid.map((c) => [c.name, c] as const))(
    '%s is accepted by the JSON Schema and the Zod',
    (_name, c) => {
      expect(validator.validate(c.body).errors).toEqual([]);
      expect(zod.safeParse(c.body).success).toBe(true);
    },
  );
  it.each(cases.invalid.map((c) => [c.name, c] as const))(
    '%s is refused by the JSON Schema and the Zod',
    (_name, c) => {
      expect(validator.validate(c.body).valid).toBe(false);
      expect(zod.safeParse(c.body).success).toBe(false);
    },
  );
});
