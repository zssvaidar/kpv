# internal/catalog

Where pipelines live (#48): the list of every pipeline under the authorized
project roots, open, create, and `Load`/`Save` of a `pipeline.yaml`. Parsing,
validating and rendering are `internal/pipeline`'s; every filesystem operation
is `internal/projectfs`'s. What it may import is in `agent/CLAUDE.md`
(`source_test.go`).

Where a pipeline lives

- a pipeline is a directory holding a file named `pipeline.yaml`: an
  authorized root itself, or a directory directly inside one
  (`<root>/<dir>/pipeline.yaml`). Nothing deeper is looked at. A root that
  holds `pipeline.yaml` at its top is that one pipeline, and its
  subdirectories are not scanned (nor opened, nor created in:
  `rootIsPipeline`)
- a pipeline is addressed by its root and directory name (`Location`), never
  by its id: two directories can hold the same id (`Entry.Duplicates`).
  `internal/codegen` and `internal/secrets` already take the pipeline
  directory from their caller; `Entry.Dir` and `Document.Dir` are its real
  path, which `secrets` requires
- a subdirectory whose name starts with `.` is not scanned, a symbolic link is
  not followed (`Root.SkippedLinks` counts them), one that cannot be read or
  searched is an `unreadable` entry, and one without `pipeline.yaml` is not
  listed. `.pipeline.yaml.tmp-*` is never a pipeline

The list (`list.go`)

- `Catalog.List` is an in-process Go method, no HTTP, so #24 and #77 call it.
  Every root `Roots.List` holds is in the answer, in its order; pipelines are
  sorted by directory name, byte order
- `Roots.List` is asked before the scan and again after it. A root unavailable
  in either, or whose scan failed part-way, has its reason and no pipelines,
  never a partial list (`TestARootThatGoesAwayWhileItIsScannedIsUnavailableNotPartial`)
- at most `projectfs.MaxListing` entries of a root are examined; more is
  `Truncated`, which #24 treats as unchecked
- each root and each pipeline directory is pinned (`Path.Pin`) before
  `pipeline.yaml` is read by its single name through the pin. A
  `pipeline.yaml` that is a link is `link`, a directory, FIFO or device
  `notRegularFile`, over 1 MiB `tooLarge`, and none of them is read
  (`file.go`, `readIn`, which says what window is left)
- a file that fails to parse or fails the schema is `parse` or `schema` with
  every problem (pointer, line, column, message) and no id, name or language
- nothing is cached or watched, and the list never changes the disk

Open and create (`open.go`, `create.go`)

- `Open` answers only what the list would show, re-reads it, and checks the
  root again after the read. The JSON is converted from the file's bytes, so
  no default is filled in; `SHA256` is of the exact bytes, for #78
- `Create` validates and renders with `pipeline.MarshalYAML` before touching
  anything, claims `<root>/<id>` with one `Mkdir` (`0755`, never an existing
  name), pins it, and writes `pipeline.yaml` with `Path.WriteAtomically`
  (`0644`). The id is `deriveID` of the name, then `-2`, `-3`... for an id a
  name in the root or any listed pipeline (ignoring case) has. Creates are
  serialized by a mutex
- after a failure past the `Mkdir`, the directory is removed only when it is
  empty and the same inode as the pin (`sameFile`, by reflection on
  `Sys()`, because `os` may not be imported); `DirectoryLeftBehind` says which
- errors are `*Error`, a kind plus the document's problems, never a path or
  an underlying error's text; logs carry kinds and counts
- tests reach inside an operation through `Catalog.hook` (`afterMkdir`,
  `beforeWrite`, `scanning`, `beforePin`); every test uses a scratch home
  and roots store
