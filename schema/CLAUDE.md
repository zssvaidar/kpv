# Schemas

Each schema here is the single source of truth for its shape.
`scripts/generate-schema.mjs` (`pnpm generate`) writes the TypeScript types,
Zod schemas and raw schema into `packages/schema/src/generated/<prefix>-*.ts`,
and Go structs plus a copy of the schema into the Go package. Adding a schema
is a row in that script's `schemas` table.

| Schema                  | Go output (plus the schema copy beside it)   | Shared fixtures                     | Fixture tests                                                       |
| ----------------------- | -------------------------------------------- | ----------------------------------- | ------------------------------------------------------------------- |
| `pipeline.schema.json`  | `agent/internal/pipeline/models_gen.go`      | `fixtures/pipelines.json`           | `packages/schema/src/fixtures.test.ts`, `pipeline/fixtures_test.go` |
| `channel.schema.json`   | `agent/internal/channel/models_gen.go`       | `fixtures/channel-messages.json`    | `channel-fixtures.test.ts`, `channel/fixtures_test.go`              |
| `directory.schema.json` | `agent/internal/httpapi/models_gen.go`       | `fixtures/directory-responses.json` | `directory-fixtures.test.ts`, `httpapi/directory_fixtures_test.go`  |
| `roots.schema.json`     | `agent/internal/httpapi/roots_models_gen.go` | `fixtures/roots-responses.json`     | `roots-fixtures.test.ts`, `httpapi/roots_fixtures_test.go`          |

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
- The channel envelope is its own schema because it carries a pipeline id and
  is not a pipeline, and `internal/channel` may not import `internal/pipeline`
  (`agent/CLAUDE.md`)
- `httpapi` may not import `embed`, so its schema copies are read by its tests
  only
