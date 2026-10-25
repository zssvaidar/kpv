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
- `pnpm test` - Vitest for the workspace, then `go test ./...` for the agent.
  `@kpv/web`'s `dev/` tests build and start a real agent, so they need Go too
- `pnpm typecheck` - `tsc` at the root, then each package's own `typecheck`
  (`@kpv/web` has two projects: the browser `src/`, and Node's `dev/` plus
  `vite.config.ts`), then `go vet ./...`
- `pnpm lint` - ESLint, Prettier, then golangci-lint
- `pnpm build` - generate from the schema, `tsc` per package plus `vite build`
  for `@kpv/web`, `go build ./...`
- `pnpm generate` - regenerate everything derived from the pipeline schema
- `pnpm generate:check` - regenerate and fail if the committed output moved
- `pnpm format` - Prettier writes the TypeScript workspace
- `pnpm dev` - build and start a fresh agent, start the Vite dev server in
  front of it, and print the one URL to open (see "The web shell")

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

`schema/channel.schema.json` is the second schema, and the same rules apply to
it: the envelope the agent pushes over its WebSocket channel (#5), generated
into `packages/schema/src/generated/channel-*.ts` and
`agent/internal/channel/models_gen.go` plus the copy that package embeds, with
shared fixtures in `schema/fixtures/channel-messages.json`. It is a separate
file because the envelope is transport and not part of the pipeline model: it
carries a pipeline id, it is not a pipeline. Keeping it separate is also what
lets the channel's Go code live outside `internal/pipeline`, which matters
because the HTTP boundary is not allowed to import that package until #6
exists. The one thing the two schemas share is what a pipeline id looks like,
and a `$ref` cannot cross files, so the pattern is written twice and
`scripts/generate-schema.mjs` fails generation when the two copies drift.

`schema/directory.schema.json` is the third, the same rules again: what the
directory browsing endpoint answers (#7), a `DirectoryListing` or a
`DirectoryError`, generated into `packages/schema/src/generated/directory-*.ts`
and `agent/internal/httpapi/models_gen.go` plus a copy of the schema there,
with shared fixtures in `schema/fixtures/directory-responses.json`. The copy is
read by the tests only: `httpapi` may not import `embed`.

`schema/roots.schema.json` is the fourth, same rules: every request and
response of the project root endpoints (#63), a `RootList`, a `RootRequest`,
an `AuthorizedRoot` or a `RootsError`, generated into
`packages/schema/src/generated/roots-*.ts` and
`agent/internal/httpapi/roots_models_gen.go` (its own file, since `httpapi`
already holds the directory schema's `models_gen.go`) plus a copy of the
schema there, with shared fixtures in `schema/fixtures/roots-responses.json`.
Two pairings are stated in words rather than in the schema, because the Zod
generator drops `if`/`then` and the Go one turns `oneOf` into `interface{}`:
a root has a `reason` exactly when it is unavailable, and an error names a
`root` exactly when it is `overlaps`. Both fixture tests and every HTTP test
answer hold them.

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
- no route here reaches the filesystem except through `internal/projectfs`:
  nothing imports `os`, `io/fs` or `path/filepath`, and the only projectfs
  names used are the read-only browsing ones and the root authorization ones
  (#63). `internal/pipeline` is still
  not wired up. `source_test.go` asserts both, and the two claims a response
  cannot show: the token source is `crypto/rand` and the comparison is
  `subtle.ConstantTimeCompare` over digests, which does not leak length
- `GET /api/directories` is directory browsing (#7), registered by
  `newHandler` beside the channel for the same reason, and covered by the same
  rejection matrix in `directories_test.go`. `path` (absolute, default the
  home directory) and `limit` (1 to 1000, default 1000). It reads through a
  `projectfs.Browser`: the authorized roots plus the home directory,
  read-only. An error answers a kind and a fixed sentence and nothing from the
  underlying error, because a projectfs refusal can describe what is outside
  the roots
- `GET`, `POST` and `DELETE /api/roots` are the project root endpoints (#63),
  registered by `newHandler` the same way and held to the same rejection
  matrix in `roots_test.go`. `GET` lists the stored roots, each with its real
  path and `available`; `POST {"path": "<absolute>"}` authorizes a directory
  and answers the real path; `DELETE {"path": ...}` revokes a root and answers
  what is left. A body is `Content-Type: application/json`, at most 4096 bytes,
  exactly one object with `path` once and nothing else (it is read token by
  token, because `encoding/json` keeps the last of two keys and matches keys
  case-insensitively); anything else is `400 invalid`. #58's relaxation does
  not reach `POST` or `DELETE`: they need an allowed `Origin`. The handler
  reads through a `projectfs.Roots` and answers a kind and a fixed sentence,
  plus, for `overlaps` only, the stored root it overlaps. Statuses: invalid
  400, forbidden/tooBroad/unreadable 403, notFound 404, notDirectory/overlaps
  409, io 500. `httpapi` may use projectfs's read-only browsing names and the
  authorization ones (`Roots`, `RootStatus`, `OverlappedRoot`,
  `KindTooBroad`, `KindOverlaps`) and nothing else, which
  `TestThisPackageUsesOnlyTheReadOnlyAndAuthorizationPartsOfProjectfs` holds
- `GET /api/channel` is the push channel (#5). It is registered by
  `newHandler`, on the same mux, inside the same wrap, rather than being a row
  in `apiRoutes()`: every row is a request-and-an-answer route and the table's
  tests hold each one to answering `200` with a JSON object, which a handshake
  is not. `channel_test.go` walks what `newHandler` registers and fails if
  anything is registered that no test there covers, so the table's "every
  route was tested" guarantee is not quietly narrowed
- a refused upgrade answers the same uniform `404`. The library answers a
  handshake it will not accept, and helpfully: `426` with the header it
  wanted, `400` with the version it supports, `403` naming the origin it
  rejected. `handshakeWriter` in `channel.go` passes a `101` through and turns
  everything else -- any status, the headers the library had set, the
  explanation it was writing, and writing nothing at all -- into that `404`
- #58's fetch-metadata exception does not reach it, because that rule excludes
  a request carrying an `Upgrade` header: a browser sends `Origin` on a
  handshake, so the channel requires one through the allowlist like any
  state-changing route

Authorized project roots

`projectfs.Roots` (#63) is the user's set of project roots: which directories
the `Guard` allows, kept in step with a file. Its `Authorize` goes through
`Guard.Authorize` on the guard the `Browser` shares, so a root is browsable
the moment it is authorized; its `Revoke` is `Guard.Revoke`.

- a root is a directory strictly inside the home directory, readable and
  searchable, resolved through the browser's home guard. A path outside home
  is `forbidden` with the same answer whatever is there (an existing
  directory, a file, nothing, an unsearchable directory, a link cycle, a link
  out); `/`, the home directory under any spelling and anything above it are
  `tooBroad` (an ancestor is recognised from the cleaned text, so the answer
  depends on nothing outside home); inside home a missing path is `notFound`,
  a file `notDirectory`, an unreadable or unsearchable directory `unreadable`.
  Authorizing a link authorizes its real target, and the real path is what is
  answered, listed and stored
- a directory inside a stored root, or containing one, is `overlaps`, naming
  that root: one file belongs to one root. The guard still supports nesting;
  `Roots` does not use it. A directory that holds the agent's store, or is
  inside its directory, is `forbidden`, because a project that could write the
  store could rewrite the set of roots
- the directory the guard opens is held to being the one that was checked
  (`os.SameFile` against what the check saw), so a component swapped between
  the two is refused as `changed`, not authorized
- `Guard.Revoke` marks the root revoked and closes its descriptor, so a
  `projectfs.Path` taken before the revoke stops working exactly as after
  `Guard.Close`: `Path.usable` refuses it, and `os.Root`'s own reference count
  means an operation already past that check finishes and nothing starts
  after. Revoking deletes nothing on disk, and the directory stays browsable
  read-only because it is under home. A path that is not a stored root is
  `notFound`, the same answer whether or not the directory exists
- the store is `os.UserConfigDir()/kpv/roots.json`, which on Linux is
  `$XDG_CONFIG_HOME/kpv/roots.json` or `~/.config/kpv/roots.json`:
  `{"version": 1, "roots": ["/real/path", ...]}`, nothing else and never the
  token. It is written atomically (a `0600` temporary file beside it, flushed,
  then renamed) in a `0700` directory, which is made by the first write. No
  config directory means the roots last as long as the process
- at startup every stored root is checked again the same way, through
  `Guard.Authorize`. One that is now missing, not a directory, unreadable,
  outside home, too broad, overlapping an earlier one, or resolves to a
  different real path (`changed`) is not authorized; it is listed with
  `available: false` and the reason, and revoking it removes it from the store.
  `available` is that decision, made at startup or at authorization, not a
  fresh look at the disk on every `GET`
- a missing store is no roots. One that cannot be read, or is not exactly the
  document above, is logged once by kind (`corrupt` or `unreadable`), the agent
  starts with no roots, and the file is renamed to `roots.json.corrupt-<UTC
  time>` (never over an earlier backup) before anything is written; if it
  cannot be moved aside, nothing is written over it and an authorization
  answers `io`. The log carries kinds, never a path
- the tests point `HOME` and `XDG_CONFIG_HOME` at scratch directories, so no
  test reads or writes the real store

The push channel

`agent/internal/channel` is one WebSocket per client, the generated envelope on
it, and a subscription per pipeline. `github.com/coder/websocket` is the
library: no dependencies of its own, and a context on every read and write,
which is what makes a bounded write a bounded write rather than a goroutine
with a timer beside it.

- authorization is not here. The token and the origin are the HTTP boundary's,
  checked before the upgrade is accepted, and this package is never given a
  credential -- which is the strongest form of "no credential in a payload"
  and is what `source_test.go` asserts: it cannot import `httpapi`, and
  nothing in it is named after a credential
- every frame, message or ping, is written by the connection's write loop and
  by nothing else. The library locks per frame, so a second writer would look
  fine until the day a message went out in pieces; `serialization_test.go`
  reads the package's syntax tree and fails if a write appears anywhere else
- a publisher never blocks: queueing is a non-blocking send on a buffered
  channel, and a client whose queue is full is disconnected rather than waited
  for. Every write is bounded by `WriteTimeout`, so a client that stopped
  reading cannot hold the write loop either
- a dead peer is found within `DefaultPingInterval + DefaultPongTimeout`, 25
  seconds, and the connection is closed. The intervals are `Options`, so a
  test injects milliseconds instead of sitting out the real ones
- closing is abrupt, by closing the socket: a close frame would be a second
  thing writing to the connection, and the status it would carry is nothing
  the agent owes a client it is hanging up on
- `Shutdown` and `Close` on the HTTP server close the channel first, because a
  hijacked connection is one `net/http` has forgotten about: it neither closes
  a WebSocket nor waits for one
- a message that will not serialize is logged and dropped, once, in `Publish`,
  where it reaches no connection at all. A client that subscribed to nothing
  receives nothing, so there is no broadcast to forget about

The web shell

`packages/web` is `@kpv/web`, a Vite + React app in strict TypeScript (#8):
the layout shell, the one typed client for the agent, and the connection state.

- `src/api/client.ts` is the only place the page reaches the agent. The token
  is handed to `createAgentClient` once and put on every request by the one
  function that sends anything, as `Authorization: Bearer`; no method takes
  headers or a URL. ESLint refuses `fetch`, `WebSocket`, `XMLHttpRequest` and
  `EventSource` anywhere else in `src/`. Answers are parsed with the generated
  Zod schemas from `@kpv/schema`, never hand-written types
- a rejection and an absent agent are told apart by whether a response
  arrived at all: the agent answers every failed check with one uniform `404`,
  so a `404` that is not the endpoint's own typed error is a rejection, shown
  as an authentication problem, and a request that gets no answer is an agent
  that is not there. Known consequence: the rejection is also what a path the
  agent never served gets, so a mistyped or too-new route reads as an
  authentication problem too
- `src/connection/monitor.ts` holds the connection state the status bar
  always shows. The push channel's WebSocket is the liveness signal; when it
  drops, one request through the client says why, and the page reconnects
  with backoff (250ms doubling, at most 10s) unless it was a rejection, which
  waiting cannot fix
- the session token is held in memory only. `src/session/token.ts` takes it
  out of the address bar and the session history with `history.replaceState`
  before anything renders, and ESLint refuses every storage API in `src/`. So
  a reload has no token and shows the authentication state; the way back is
  re-opening the agent's startup URL, which in development is the one
  `pnpm dev` printed. Since #58 a link carrying the token is a link that works,
  which is why the stripping matters
- the page sends no `Referer`: a `<meta>` in `index.html` and, in
  development, a `Referrer-Policy` header, because Vite's own client script
  loads ahead of the `<meta>`

Development (`pnpm dev`, `dev/`)

- the agent binds an ephemeral port per run and prints a URL carrying the
  token that points at the agent's own origin, not at the dev server. So
  `pnpm dev` builds the agent from this checkout, starts it, reads its
  startup lines for the port and the token, starts Vite (port 5173, or the
  next free one) and prints `open http://127.0.0.1:<port>/?token=...`. Open
  that. Nothing is configured or committed, and every run is a new agent, a
  new port and a new token. Stopping it stops both; the dev server outlives an
  agent that dies, so the page shows what that looks like
- `/api` is proxied by `dev/agent-proxy.ts`, a small proxy of its own rather
  than Vite's `server.proxy`, which answers `502` for a dead agent (an answer,
  which is the wrong signal) and logs failed URLs (the handshake's carries the
  token). The browser only talks to the dev server, so nothing is CORS and
  nothing is preflighted, and the agent is unchanged
- the proxy translates `Origin` rather than forging it: a request that is
  same-origin to the dev server (its own `Origin` and `Host`, or no `Origin`
  with `Sec-Fetch-Site: same-origin` and its own `Host`) is presented with the
  agent's origin, and anything else is passed through untouched for the
  agent's own rules to reject. It never adds, reads or checks the token
- `dev/dev.test.ts` runs all of that against a real agent, twice in a row,
  on ports read from the processes that chose them

Rules
- TODO
