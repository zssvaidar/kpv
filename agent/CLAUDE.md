# The Go agent

Module `github.com/zssvaidar/kpv/agent`; run `go` from this directory. Each
package under `internal/` has its own `CLAUDE.md`.

## Which package may use which

These rules are tests that read the non-test source, so breaking one fails
`go test ./...` rather than review. `cmd/agent` wires `httpapi` and
`projectfs` together.

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
- `internal/projectfs` and `internal/pipeline` import no other agent package.
  No test holds this; it is true today (`go list -f '{{.Imports}}' ./...`)

Module-wide, in `internal/projectfs/boundary_test.go`:

- no filesystem call (`os.Open`, `os.Stat`, `filepath.Abs`, ...) outside
  `internal/projectfs` and `internal/pipeline`; `pipeline` is allowed until #48
  moves its store onto the guard
- nothing can run a command: no `os/exec`, `syscall`, `golang.org/x/sys`, no
  `os.StartProcess`, and no string literal naming a shell or its `-c` flag. An
  issue that needs to run a process has to change this test, so raise it on
  the issue first

## Startup lines

`cmd/agent/main.go` prints three lines to stdout: `kpv agent <version>`,
`listening on <addr>`, `open <url with token>`. The URL is the only place the
token is written. `packages/web/dev/agent-process.ts` parses the `open` line
(`/^open (\S+)$/`), so a change here breaks `pnpm dev`, caught by
`packages/web/dev/dev.test.ts`.

The agent reads the real `HOME` and `os.UserConfigDir()` (the roots store,
`internal/projectfs/CLAUDE.md`). `cmd/agent/main_test.go` points `HOME` and
`XDG_CONFIG_HOME` at scratch directories; `packages/web/dev/dev.test.ts` does
not, so the agent it starts reads the developer's real store.
