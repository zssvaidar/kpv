# The Go agent

Module `github.com/zssvaidar/kpv/agent`; run `go` from this directory. Each
package under `internal/` has its own `CLAUDE.md`.

## Which package may use which

These rules are tests that read the non-test source, so breaking one fails
`go test ./...` rather than review. `cmd/agent` wires `httpapi`, `catalog`,
`connections`, `workcopy` and `projectfs` together.

- `internal/httpapi` may not import `os`, `io/fs`, `io/ioutil`,
  `path/filepath`, `embed`, `os/exec` or `internal/pipeline`, and uses only the
  `projectfs` names listed in
  `TestThisPackageUsesOnlyTheReadOnlyAndAuthorizationPartsOfProjectfs`
  (`internal/httpapi/source_test.go`)
- `internal/channel` may not import `os`, `io/fs`, `io/ioutil`, `path`,
  `path/filepath`, `os/exec`, `internal/pipeline` or `internal/httpapi`, and no
  identifier or string in it may contain token, secret, password, credential,
  authorization or bearer (`internal/channel/source_test.go`)
- `internal/codegen` imports `internal/projectfs` and `internal/pipeline` and
  nothing else in the agent, nor `os` or `path/filepath`; `internal/httpapi`,
  `internal/channel`, `internal/projectfs` and `internal/pipeline` do not
  import it (`internal/codegen/source_test.go`)
- `internal/secrets` imports `internal/projectfs` and nothing else in the
  agent, nor `os` or `path/filepath`; `internal/projectfs`, `internal/pipeline`,
  `internal/codegen` and `internal/channel` do not import it
  (`internal/secrets/source_test.go`)
- `internal/catalog` imports `internal/projectfs` and `internal/pipeline` and
  nothing else in the agent, nor `os` or `path/filepath`; `internal/projectfs`,
  `internal/pipeline`, `internal/codegen`, `internal/secrets` and
  `internal/channel` do not import it (`internal/catalog/source_test.go`).
  `internal/httpapi` holds a `*catalog.Catalog` and never touches the parsed
  model it carries
- `internal/connections` imports `internal/projectfs`, `internal/secrets` and
  `internal/catalog` and nothing else in the agent, nor `os` or
  `path/filepath`; `internal/projectfs`, `internal/pipeline`,
  `internal/catalog`, `internal/codegen`, `internal/secrets` and
  `internal/channel` do not import it, and only `encodeStore` calls `Reveal`
  (`internal/connections/source_test.go`). `internal/httpapi` holds a
  `*connections.Store`
- `internal/workcopy` (#43) imports `internal/catalog` and
  `internal/projectfs` and nothing else in the agent, nor `os` or
  `path/filepath`; no lower package imports it, and `internal/httpapi`, which
  holds a `*workcopy.Store`, names no staging method
  (`internal/workcopy/source_test.go`)
- `internal/projectfs` and `internal/pipeline` import no other agent package.
  No test holds this; it is true today (`go list -f '{{.Imports}}' ./...`)
- `internal/process` imports no other agent package
  (`internal/process/source_test.go`). Only the packages on
  `processImporters` in `internal/projectfs/boundary_test.go` may import it:
  after #37, only `internal/parser`
- `internal/parser` imports `internal/process` and nothing else in the agent,
  nor `os` or `path/filepath` (`internal/parser/source_test.go`): it is handed
  source bytes, never a path

Module-wide, in `internal/projectfs/boundary_test.go`:

- no filesystem call (`os.Open`, `os.Stat`, `filepath.Abs`, ...) outside
  `internal/projectfs`
- one package may start a process, `internal/process` (#37), and only a
  program from its fixed table (`internal/process/programs.go`), with no
  shell and no argument, environment, working directory or executable taken
  from a request: input reaches a child only as stdin bytes. Every other
  package keeps every ban: no `os/exec`, `syscall`, `golang.org/x/sys`, no
  `os.StartProcess` or other spawning call, and no string literal naming a
  shell or its `-c` flag, which stays banned in `internal/process` too
  (`internal/process/CLAUDE.md` lists what it may use). Names are resolved
  through each file's imports, and `os`, `os/exec`, `syscall`,
  `path/filepath`, `io/ioutil` and `io/fs` may not be imported under another
  name anywhere, which also holds the filesystem test below; a reference is
  banned whether it is called or not. `internal/process` is also type checked
  against allowlists of imports, names and `exec.Cmd` selections, and no file
  in the module, test files included, may import `"C"`, `net/http/cgi` or
  `plugin`, nor a non-test file `go/build` or `go/importer`, the module
  holds no non-Go source, and no `.go` file sits in a `testdata`,
  `.`-prefixed or symlinked directory the walks skip; `go list -deps -test`
  is cross-checked against what they read (`internal/process/CLAUDE.md` says
  exactly what is and is not seen). A later issue that needs a program adds
  its entry to `programs.go` and its package, with the reason, to
  `processImporters` in the boundary test, instead of making a new exception;
  `internal/httpapi` never imports it

## Startup lines

`cmd/agent/main.go` prints three lines to stdout: `kpv agent <version>`,
`listening on <addr>`, `open <url with token>`. The URL is the only place the
token is written. `packages/web/dev/agent-process.ts` parses the `open` line
(`/^open (\S+)$/`), so a change here breaks `pnpm dev`, caught by
`packages/web/dev/dev.test.ts`.

The agent reads the real `HOME` and `os.UserConfigDir()` (the roots store,
`internal/projectfs/CLAUDE.md`, the connection store beside it,
`internal/connections/CLAUDE.md`, and the working copies in `workcopies/`
beside them, `internal/workcopy/CLAUDE.md`). `cmd/agent/main_test.go` points `HOME` and
`XDG_CONFIG_HOME` at scratch directories. `pnpm dev` and
`packages/web/dev/dev.test.ts` start the agent with the environment they were
given (`spawn` in `packages/web/dev/agent-process.ts`), so unless `HOME` and
`XDG_CONFIG_HOME` are set to scratch directories they read, and an authorize,
a connection save or a corrupt store moved aside writes, the developer's real
`~/.config/kpv/roots.json` and `connections.json`. A root must be strictly
inside `HOME` (`internal/projectfs/CLAUDE.md`), so a manual check that keeps
the real `HOME` needs its project directories under `~`.
