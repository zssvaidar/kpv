Documents

- `_docs/process.md` - how work is organized
- `_docs/task-template.md` - the shape a task has once it is groomed
- `_docs/team/` - one file per role, read the one you are acting as
- `_docs/outdated/` - superseded documents, kept for reference, not current
- Before writing tests, read `_docs/testing-guidelines.md` (not written yet)
- For anything touching the UI, read `_docs/design-system.md` (not written yet)

Commands

Root scripts are the single entry point; a developer and CI run the same ones.

- `pnpm install` - install the TypeScript workspace (`@kpv/web`, `@kpv/schema`)
- `pnpm test` - Vitest for the workspace, then `go test ./...` for the agent
- `pnpm typecheck` - `tsc` at the root, then `go vet ./...`
- `pnpm lint` - ESLint, Prettier, then golangci-lint
- `pnpm build` - generate from the schema, `tsc` per package, `go build ./...`
- `pnpm generate` - regenerate everything derived from the pipeline schema
- `pnpm generate:check` - regenerate and fail if the committed output moved
- `pnpm format` - Prettier writes the TypeScript workspace

The Go module lives in `agent/` (module `github.com/zssvaidar/kpv/agent`), so
plain `go` commands run from there: `cd agent && go test ./...`.

Versions are pinned once: Node in `.nvmrc` plus `engines`, pnpm in
`packageManager`, Go in the `go` directive of `agent/go.mod`. CI reads those
same files.

The pipeline model

`schema/pipeline.schema.json` is the single source of truth for what a
pipeline is. Both languages are generated from it and neither is a second
source, so a model change is a change to that file followed by
`pnpm run generate`.

- `packages/schema/src/generated/` - TypeScript types, Zod schemas and the
  raw schema, published as `@kpv/schema`
- `agent/internal/pipeline/models_gen.go` plus `pipeline.schema.json` - the Go
  structs the agent imports, and the copy it embeds because a Go module cannot
  embed a file from outside itself
- `schema/fixtures/pipelines.json` - the one set of valid and invalid
  fixtures, read by the Go tests and the Vitest tests alike

Generated output is committed, and `pnpm generate:check` regenerates it and
fails on any diff. That is what catches a stale schema and a hand-edited
generated file, so never edit anything under a generated path by hand.

The generators are pinned: `go-jsonschema` as a tool directive in
`agent/go.mod`, `json-schema-to-typescript` and `json-schema-to-zod` as root
devDependencies.

Rules
- TODO
