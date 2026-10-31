# Schemas

Each schema here is the single source of truth for its shape.
`scripts/generate-schema.mjs` (`pnpm generate`) writes the TypeScript types,
Zod schemas and raw schema into `packages/schema/src/generated/<prefix>-*.ts`,
and Go structs plus a copy of the schema into the Go package. Adding a schema
is a row in that script's `schemas` table.

| Schema                   | Go output (plus the schema copy beside it)                                                            | Shared fixtures                     | Fixture tests                                                                                         |
| ------------------------ | ----------------------------------------------------------------------------------------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------------------- |
| `pipeline.schema.json`   | `agent/internal/pipeline/models_gen.go`                                                               | `fixtures/pipelines.json`           | `packages/schema/src/fixtures.test.ts`, `pipeline/fixtures_test.go`                                   |
| `channel.schema.json`    | `agent/internal/channel/models_gen.go`                                                                | `fixtures/channel-messages.json`    | `channel-fixtures.test.ts`, `channel/fixtures_test.go`                                                |
| `directory.schema.json`  | `agent/internal/httpapi/models_gen.go`                                                                | `fixtures/directory-responses.json` | `directory-fixtures.test.ts`, `httpapi/directory_fixtures_test.go`                                    |
| `roots.schema.json`      | `agent/internal/httpapi/roots_models_gen.go`                                                          | `fixtures/roots-responses.json`     | `roots-fixtures.test.ts`, `httpapi/roots_fixtures_test.go`                                            |
| `pipelines.schema.json`  | `agent/internal/httpapi/pipelines_models_gen.go`                                                      | `fixtures/pipelines-responses.json` | `pipelines-fixtures.test.ts`, `httpapi/pipelines_fixtures_test.go`                                    |
| `connection.schema.json` | `agent/internal/httpapi/connections_models_gen.go`, and a schema copy in `agent/internal/connections` | `fixtures/connections.json`         | `connection-fixtures.test.ts`, `connections/fixtures_test.go`, `httpapi/connections_fixtures_test.go` |

The generators are pinned: `go-jsonschema` as a `tool` directive in
`agent/go.mod`, `json-schema-to-typescript` and `json-schema-to-zod` as root
devDependencies.

What a schema change can break without the change looking wrong

- The pipeline id pattern is written twice, `pipeline.schema.json`
  `$defs/identifier` and `channel.schema.json` `$defs/pipelineId`, because a
  `$ref` cannot cross files. Generation fails when they differ
  (`generate-schema.mjs`)
- A required property may not carry a `default`: the generated Zod would make
  it optional and disagree with a JSON Schema validator. Generation refuses it
  (`generate-schema.mjs`)
- `x-kpv-uniqueBy` (step ids unique within a pipeline) is not JSON Schema, so
  it is implemented twice: `agent/internal/pipeline/validate.go` and
  `packages/schema/src/unique-by.ts`. A new `x-kpv-*` keyword needs both, and
  fixtures in `pipelines.json` that hold them to the same answer
- The Zod generator drops `if`/`then` and the Go one turns `oneOf` into
  `interface{}`, so two `roots.schema.json` pairings are held by tests instead:
  a root has a `reason` exactly when it is unavailable, and an error names a
  `root` exactly when it is `overlaps` (`roots-fixtures.test.ts`,
  `httpapi/roots_fixtures_test.go`, `httpapi/roots_test.go`)
- `pipelines.schema.json` `$defs/pipelineRootReason` copies `roots.schema.json`
  `$defs/rootUnavailableReason`, so the pipeline list says an unavailable root
  in the picker's words; generation fails when they differ. Its open answer's
  `pipeline` is a plain object, because a `$ref` cannot reach
  `pipeline.schema.json`: the page parses it with the generated pipeline Zod.
  The pairings its descriptions state (a `reason` exactly when unavailable or
  `rootUnavailable`, `id` and `language` exactly when readable, `unreadable`
  exactly when not) are held by `pipelines-fixtures.test.ts` and
  `httpapi/pipelines_fixtures_test.go`
- A connection name is written twice, `pipeline.schema.json`
  `$defs/kafkaConnectionName` (the `connection` a Kafka block names) and
  `connection.schema.json` `$defs/connectionName`; generation fails when their
  type, length and pattern differ, and `fixtures/connections.json` `names`
  holds both to the same names. The connection schema's `uncheckedReason`
  starts with `pipelines.schema.json`'s `unreadableKind`, also checked by
  generation
- The connection combination rules (which fields a security protocol allows
  and needs, a client certificate and its key together, no bootstrap server
  twice) are stated in the schema's descriptions, not as `if`/`then`, which
  the Zod generator drops. They are implemented twice:
  `agent/internal/connections/rules.go` and
  `packages/schema/src/connection-rules.ts`, held to the same answers by
  `fixtures/connections.json` `records` (a failure names a `keywordLocation`
  or a `rule`). Whether PEM text parses is the agent's check only
- `connection.schema.json`'s generated Go `Connection` and `ConnectionUpdate`
  hold the credentials as strings, so `httpapi` reads those bodies token by
  token into `secrets.Secret` and never uses the two structs
  (`httpapi/source_test.go`); `internal/connections` gets only the schema
  copy, no structs
- The channel envelope is its own schema because it carries a pipeline id and
  is not a pipeline, and `internal/channel` may not import `internal/pipeline`
  (`agent/CLAUDE.md`)
- `httpapi` may not import `embed`, so its schema copies are read by its tests
  only
