kpv project guide

Read this page, then only the `CLAUDE.md` of the area your task touches.
Claude Code loads a nested `CLAUDE.md` by itself when you read a file under
its directory, so the area pages below arrive when you open the code.

Documents

- `_docs/process.md` - how work is organized
- `_docs/task-template.md` - the shape a task has once it is groomed
- `_docs/team/` - one file per role, read the one you are acting as
- `_docs/outdated/` - superseded documents, not current; take no facts from them
- Testing guidelines and a design system are not written yet (#52, #53)

Areas

- `schema/CLAUDE.md` - the JSON Schemas, everything generated from them, the
  shared fixtures
- `agent/CLAUDE.md` - the Go module: which package may import which, the
  module-wide bans, the startup lines
- `agent/internal/pipeline/CLAUDE.md` - parsing, validating and rendering a
  pipeline document
- `agent/internal/catalog/CLAUDE.md` - where a pipeline lives: the pipeline
  list, open, create, and reading and writing `pipeline.yaml`
- `agent/internal/connections/CLAUDE.md` - named Kafka connections: the
  record and its rules, the credential store, the reference check
- `agent/internal/projectfs/CLAUDE.md` - the path guard and authorized project
  roots
- `agent/internal/codegen/CLAUDE.md` - generating files into `generated/`, the
  manifest, and the boundary with the user's `src/`
- `agent/internal/secrets/CLAUDE.md` - the secret type, and the merged writes
  of `.env` and `.gitignore` at a pipeline root
- `agent/internal/httpapi/CLAUDE.md` - the local HTTP API: token, origin,
  routes
- `agent/internal/channel/CLAUDE.md` - the WebSocket push channel
- `agent/internal/process/CLAUDE.md` - the one package that may start a
  process: the fixed program table, `Run` and its bounds
- `agent/internal/parser/CLAUDE.md` - the parser harness: sidecar and
  in-process transports, and the validation of the parser contract
- `packages/web/CLAUDE.md` - the web app, its one agent client, `pnpm dev`

The schema is the source of truth

What a pipeline is, and every shape the agent and the page exchange, is a
JSON Schema in `schema/`. Go and TypeScript are generated from it, so a model
change is a schema edit followed by `pnpm generate`. Never edit by hand
what `generatedPaths` in `scripts/check-generated.mjs` lists, apart from
`schema/` itself:

- `packages/schema/src/generated/`
- every `agent/internal/**/*models_gen.go` (`pipelines_models_gen.go`,
  `connections_models_gen.go` and `parser/models_gen.go` included)
- the `*.schema.json` copies under `agent/internal/`

`pnpm generate:check` (`scripts/check-generated.mjs`, run by CI) regenerates
and fails on any diff, which catches both a stale schema and a hand edit. It
asks `git status`, so it needs a git work tree: in a tarball copy, run
`pnpm generate` and diff against an untouched copy, or `git init` and commit
the copy first.

Commands

Root scripts in `package.json` are the single entry point; CI
(`.github/workflows/`) runs the same ones.

- `pnpm install` - install the TypeScript workspace (`@kpv/web`, `@kpv/schema`)
- `pnpm test` - Vitest for the workspace (`vitest.config.ts`), then
  `go test ./...`. `packages/web/dev/` tests build and start a real agent, so
  they need Go too
- `pnpm typecheck` - `tsc` at the root, each package's `typecheck`, `go vet`
- `pnpm lint` - ESLint, `prettier --check .`, golangci-lint (`.golangci.yml`).
  Prettier checks every Markdown file except `_docs/` and `@CLAUDE.md`
  (`.prettierignore`), so the area `CLAUDE.md` pages are formatted
- `pnpm build` - generate, `tsc` per package plus `vite build`, `go build`
- `pnpm generate`, `pnpm generate:check` - see above
- `pnpm format` - Prettier rewrites everything `pnpm lint` checks; in the
  main checkout it would also rewrite the user's untracked root `CLAUDE.md`,
  so run `pnpm exec prettier --write <your files>` there instead
- `pnpm dev` - start a fresh agent and the Vite dev server, print the URL to
  open (`packages/web/CLAUDE.md`)
- one TypeScript test file: `pnpm exec vitest run <path>`
- `cd agent && go test -race ./...` - CI does not run `-race`
  (`.github/workflows/ci.yml`), so run it yourself

The whole suite, which a commit and a QA verdict need, is `pnpm lint`,
`pnpm typecheck`, `pnpm test`, `pnpm build`, `pnpm generate:check` and
`cd agent && go test -race ./...`. In the main checkout `pnpm lint` exits 1 on
one file only: Prettier flags the user's untracked root `CLAUDE.md`, which is
local tool state and not repo content (`.prettierignore` names `@CLAUDE.md`,
not it). There, `pnpm exec prettier --check . '!CLAUDE.md'` plus `pnpm lint:ts`
and `pnpm lint:go` stand in for `pnpm lint`; a clean clone and CI run
`pnpm lint` as it is.
- one Go package: `cd agent && go test ./internal/<pkg>/ -run <TestName>`
- the security boundary suite alone (#46, `agent/CLAUDE.md`):
  `cd agent && go test ./cmd/agent -run '^TestSecurity' -count=1 -v`. It
  builds and starts the real agent binary, so it takes about 20 seconds

The Go module is `agent/` (`github.com/zssvaidar/kpv/agent`), so plain `go`
commands run from there.

Versions are pinned once: Node in `.nvmrc` plus `engines`, pnpm in
`packageManager`, Go in the `go` directive of `agent/go.mod`. CI reads the
same files.
