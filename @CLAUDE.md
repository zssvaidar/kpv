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
- `agent/internal/pipeline/CLAUDE.md` - reading and writing `pipeline.yaml`
- `agent/internal/projectfs/CLAUDE.md` - the path guard and authorized project
  roots
- `agent/internal/codegen/CLAUDE.md` - generating files into `generated/`, the
  manifest, and the boundary with the user's `src/`
- `agent/internal/httpapi/CLAUDE.md` - the local HTTP API: token, origin,
  routes
- `agent/internal/channel/CLAUDE.md` - the WebSocket push channel
- `packages/web/CLAUDE.md` - the web app, its one agent client, `pnpm dev`

The schema is the source of truth

What a pipeline is, and every shape the agent and the page exchange, is a
JSON Schema in `schema/`. Go and TypeScript are generated from it, so a model
change is a schema edit followed by `pnpm generate`. Never edit by hand:

- `packages/schema/src/generated/`
- `agent/internal/*/models_gen.go`, `agent/internal/httpapi/roots_models_gen.go`
- the `*.schema.json` copies under `agent/internal/`

`pnpm generate:check` (`scripts/check-generated.mjs`, run by CI) regenerates
and fails on any diff, which catches both a stale schema and a hand edit.

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
- `pnpm format` - Prettier rewrites everything `pnpm lint` checks
- `pnpm dev` - start a fresh agent and the Vite dev server, print the URL to
  open (`packages/web/CLAUDE.md`)
- one TypeScript test file: `pnpm exec vitest run <path>`
- one Go package: `cd agent && go test ./internal/<pkg>/ -run <TestName>`

The Go module is `agent/` (`github.com/zssvaidar/kpv/agent`), so plain `go`
commands run from there.

Versions are pinned once: Node in `.nvmrc` plus `engines`, pnpm in
`packageManager`, Go in the `go` directive of `agent/go.mod`. CI reads the
same files.
