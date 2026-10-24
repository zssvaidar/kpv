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

Reading and writing `pipeline.yaml`

`agent/internal/pipeline` reads and writes a pipeline file. YAML is bridged
through JSON (`YAMLToJSON`, `JSONToYAML`) so the one JSON Schema validator
covers both formats, and the model is the generated one.

- `pipeline.Store` is given a path and reads or writes it; `LoadFile` and
  `SaveFile` are the convenience wrappers. Which directory a pipeline lives
  in is #48, and `Store.resolve` is the single seam #6's path guard plugs
  into, so a guard arrives without reshaping callers
- failures are one `*pipeline.FileError`: a `Kind` to branch on, the
  offending field as a JSON Pointer, a line and column, and the schema
  violations with the same pointers `validate.go` reports
- the reader is strict where YAML is permissive: a duplicate key, a file with
  no document in it, more than one document, a non-string key and a tag
  outside the JSON data model are all rejected. The parser is
  `gopkg.in/yaml.v3`, chosen on measurements written down in `yaml.go`
- writing is atomic and reproducible: a temporary file in the same directory
  then a rename, and no map iteration in the output, so the same pipeline
  always produces the same bytes. Comments are not preserved

The local HTTP API

`agent/internal/httpapi` is the agent's HTTP server: standard library
`net/http` and `http.ServeMux`, no framework. `agent/cmd/agent` binds it,
prints the URL to open and serves.

- it binds `127.0.0.1` on port 0, and `New` binds before it returns, so the
  chosen port is known and reportable (`Addr`, `Port`, `URL`) before anything
  is served and nothing has to wait to find out
- a session token is minted per run from `crypto/rand`, 256 bits, base64url.
  It is written in exactly one place, the startup line on stdout, and never to
  the log
- `Token` cannot be printed, and the guarantee is worth stating precisely
  because a weaker version of it was wrong where it mattered. `Format`,
  `LogValue` and `MarshalText` cover `fmt`, `log/slog` and `encoding/json`,
  and the plaintext is held in a closure rather than a field for the paths
  those methods cannot cover: `fmt` reaches a value through an *unexported*
  field without being able to call any method on it, and `Server.token` and
  `guard.token` are exactly that shape, so `%v` on a `Server` used to print
  the token in full; `%p` bypasses `Formatter` the same way. With no plaintext
  field, all any of those paths can show is a function address and the token's
  SHA-256 digest. A `Token` is consequently not comparable with `==`, which is
  the point: the one comparison it offers is the constant-time one
- the token and origin checks are one `guard` wrapped around the whole mux.
  Routes are rows in `apiRoutes()`, `newHandler` is the only place a mux
  exists and it returns that mux already wrapped, so a new route cannot be
  registered outside the checks. Two assertions keep that true from both
  ends: no `Handle` call outside `newHandler`, and `http.Server.Handler` is
  exactly `newHandler(g, apiRoutes())`, so a hand-rolled router or a wrapper
  around the guarded handler fails the suite rather than shipping
- `DisableGeneralOptionsHandler` is set, because `net/http` answers
  `OPTIONS *` from its own built-in handler before `Server.Handler` is
  consulted and an unauthorized caller got a `200` out of it. The two shapes
  it still answers ahead of any handler, `400` with no `Host` and `431` over
  the header cap, cannot be taken back and distinguish none of the checks
- a request presents the token as `Authorization: Bearer` or in the `token`
  query parameter, which is how the startup URL carries it to the browser
- the `Origin` header is checked against an exact allowlist: the agent's own
  origin plus anything in `Options.AllowedOrigins`. A request with no `Origin`
  is rejected, fail closed; `originRequired` in `origin.go` is the rule and
  says why. What a browser really does: it sends `Origin` on a request whose
  mode is CORS, on any request whose method is not `GET`/`HEAD`, and on a
  WebSocket handshake. It sends none on a same-origin `GET` -- and none on a
  *cross-origin no-CORS* `GET` either, so an `<img>`, `<script>`, `<iframe>`
  or navigation a hostile page drives arrives here with no `Origin` at all
- #58 decided the one exception, and it keys on `Sec-Fetch-Site` rather than
  on `Origin` being absent, precisely because absence does not mean "not
  hostile". With no `Origin` header, a request is accepted when
  `Sec-Fetch-Site` is exactly `same-origin` (the agent's own page, which is
  the only document this origin can serve) or `none` (user-initiated, which no
  page can cause: the startup URL being opened). A page cannot set that
  header, it is a forbidden header name, and it cannot be laundered through a
  redirect, because it is computed over the whole chain and downgraded to the
  least trusted value in it. The exception is narrowed to what a browser
  really sends: `GET`/`HEAD` only, one header value only, never an upgrade, and
  the token is still required because the guard conjoins both checks. So a
  state-changing route gets no relaxation (its same-origin request carries an
  `Origin`) and #5's handshake gets none. A client that sends neither header
  is rejected, which is `curl`, any script, and a browser older than fetch
  metadata; `fetchSiteVouches` in `origin.go` is the rule, the two values, the
  narrowing and the trade-off accepted
- every failed check answers the same 404 with the same body and headers, the
  same answer an unauthorized request to a path that does not exist gets, so
  nothing can tell the three failures apart. Both checks run before either is
  acted on, so the timing does not tell them apart either
- read, read-header, write and idle timeouts are set, and the header size is
  capped well below `net/http`'s 1MB default
- no route here reads the filesystem: `internal/pipeline` is deliberately not
  wired up, because a route that reads a path needs the path guard in #6.
  `source_test.go` asserts that, and the two claims a response cannot show:
  the token source is `crypto/rand` and the comparison is
  `subtle.ConstantTimeCompare` over digests, which does not leak length

Rules
- TODO
