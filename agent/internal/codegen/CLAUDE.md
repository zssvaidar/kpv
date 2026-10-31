# internal/codegen

Emits the files a pipeline is generated into, and holds the line between them
and the user's code (#16). The package comment in `codegen.go` explains the
model; nothing in the running agent calls it yet (the first caller is deploy,
#32, and pointing it at the managed working copy is #43).

- a `Generator` is a pure function: `*pipeline.Pipeline` in, `[]File` (paths
  relative to `generated/`, bytes) out. It never touches the filesystem; `Run`
  decides whether each path may be written, so the boundary does not depend on
  a generator behaving. The real generators are #17-#19; the tests use a fake
  single-file one (`fake` in `helpers_test.go`)
- `Run(ctx, dir, model, generators...)` takes the pipeline directory as a
  `projectfs.Path`; it does not decide where a pipeline lives (#48). Only
  `dir/generated/` is ever created, changed or removed. Every test compares the
  rest of the tree byte for byte (`snapshot` in `helpers_test.go`)
- the order is fixed (doc comment on `Run`): generate and check every path
  (`paths.go`), read `generated/` and the manifest, check every output against
  the disk, and only then write, each file with `Path.WriteAtomically`, the
  manifest last. A run that finds any conflict writes nothing and reports all
  of them
- the report (`report.go`) is per path: `written`, `unchanged`, `regenerated`,
  `modified`, `unowned`, `notRegularFile`, `orphaned`, plus `refused`,
  `blocked`, `failed` and `notWritten`. It never holds file contents. A guard
  refusal is carried as its `projectfs` kind, `forbidden` included
- the manifest is `generated/kpv-manifest.json` (`manifest.go` documents the
  format): version 1, entries sorted by path with a SHA-256 over the exact
  bytes, no line-ending or newline normalization, so an `autocrlf` checkout is
  `modified`. An orphan stays listed with `"orphaned": true` and its original
  hash until it is deleted. A manifest that does not read, parse or have
  version 1 stops the run and is not written over
- a file already holding exactly the bytes to be written is `unchanged` and
  recorded, whether or not the manifest lists it; that is what makes a run
  interrupted before its manifest harmless (`interrupt_test.go`)
- a `generated/` that is a link or not a directory, and a link or file where a
  directory under it should be, is refused before anything is written through
  it (`symlink_test.go`). A link swapped in between the last check and a write
  is not caught here; the root's descriptor still keeps it inside the root
- two runs, or a run and an `Inspect`, for one directory in one agent never
  interleave: a lock keyed by the directory's real path (`lock.go`,
  `concurrency_test.go`). Two agent processes are not kept apart
- `Inspect` reads the manifest against the disk without generating (#15)
- tests that need to act between two writes (revoke the root, cancel, edit a
  file) call the unexported `run` with `hooks`; `Run` passes none
- imports only `internal/projectfs` and `internal/pipeline` from the agent, the
  standard library otherwise, and no `os` (`source_test.go`)
