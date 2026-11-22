# internal/workcopy

The managed working copy of a pipeline directory and its apply flow (#43).
What it may import, and that no HTTP route stages, are tests in
`source_test.go`: it imports `internal/catalog` and `internal/projectfs` and
nothing else in the agent, nor `os` or `path/filepath`; no lower package
(`projectfs`, `pipeline`, `catalog`, `codegen`, `secrets`, `channel`,
`connections`, `process`, `parser`) imports it; `internal/httpapi` names no
`Stage`.

Which writes are staged and which are direct

- staged: only the edits the tool composes on the user's behalf, #77's
  rewrites of other pipelines' `pipeline.yaml` and #97's write-back into
  imported source. They call `Store.Stage`, an in-process Go method (as
  `catalog.List` is); no route stages
- direct: everything the user typed and saved, with a hash check, and what
  the user asked for in one step: `catalog.WriteSource` (#87, so #14's and
  #15's Save), #78's save of `pipeline.yaml`, #48's create, `codegen.Run`
  (#16 owns `generated/`) and `secrets`. None of them goes through here, and a
  direct save of a file that has a staged change is allowed: the change then
  shows as drift at apply and is not overwritten

The working copy

- an overlay, not a copy of the tree: per pipeline (`catalog.Location`, root
  and directory, never the id) only the staged files, each with its path
  relative to the pipeline directory, the SHA-256 of the project file it was
  first staged from (or none), and the new bytes or a deletion. A file nothing
  staged is never copied, so `.env` is never duplicated
- paths keep `catalog.CheckStagedPath` (#87's rules, plus `pipeline.yaml` at
  the top allowed and `generated/` refused, ignoring case); bytes keep
  `catalog.CheckStagedText` (`tooLarge`, `notText`). A deletion of
  `pipeline.yaml` is `reserved`; at most `MaxFiles` (32) files, `tooMany`.
  A change back to its base unstages the path
- the review's `Revision` is 128 random bits, new on every stage, apply and
  discard and never reused; apply and discard send it, another is `changed`
- staged text is shown only by `Review`; `ReadSource`, open and the editor
  answer the disk

Where it is kept

- `os.UserConfigDir()/kpv/workcopies/`, a `projectfs.PrivateTree` beside the
  roots store (`projectfs.OpenPrivateTree`, `0700`, files `0600`): one
  `copy-<key>.json` per working copy and one `snap-<key>-<id>.json` per
  snapshot, `<key>` a hash of root and directory, bytes base64. #63 refuses a
  root holding the store directory, so neither is ever inside a project, and
  nothing is added to any `.gitignore`
- no config directory (`storeFile` answers ""): the tree is nil and every
  method answers `unavailable`, writing nothing
- it survives a restart. A file that cannot be read, does not parse, is not
  of the location it is named for, or holds an entry whose path breaks the
  rules (`../x`, `.env`, `/etc/passwd`) is logged by kind (`corrupt`,
  `invalidPath`, `unreadable`), moved aside to `<name>.corrupt-<UTC time>`,
  never followed; the pipeline then has nothing staged and `Review.Unreadable`
  says so until something is staged there again. `List.Unreadable` counts them
- a working copy whose pipeline is no longer listed is still in `List` with
  its location and the reason; it can be discarded, not applied

Apply and restore (`apply.go`)

- `Apply(location, revision)` and `Restore(location, snapshot)` run inside
  `catalog.WithFiles`, which holds `Catalog.source`, so they never interleave
  with `WriteSource`. Before anything is written: the root is checked again
  (#67), the pipeline directory and every directory on each path are pinned
  one name at a time (`pinOne`), and every staged file is compared with the
  hash it was staged from, absent included (restore: with what the apply
  wrote, or still the snapshot's bytes, which is `unchanged`). Every
  differing file is in one `drift` error, nothing is written, and nothing
  offers to overwrite
- then `Files.Verify` checks every pinned directory is still under its name
  (a link `link`, a swap `changed`), and the snapshot of every file the apply
  changes (its bytes, or that there was none) is written to the store and
  synced; one that cannot be written is `snapshot` with nothing written
- files are written in path order with `Path.WriteAtomically` (`0644` new,
  missing directories `0755` inside their pin); a deletion removes only that
  file, and a directory it leaves empty is kept (so is one an apply made and
  a restore emptied). The first failure stops the run: the report says each
  path `written`, `failed` (with its kind) or `notWritten`; the written ones
  leave the working copy, the rest stay staged, and the snapshot stays.
  Nothing removes a snapshot but `RemoveSnapshot`
- serialization is within one agent; two agents on one project are out of
  scope, as `codegen` documents for its lock

Tests

- every test uses a scratch home and store (`helpers_test.go`, `newWorld`)
  and compares the whole root byte for byte (`snapshot`, `sameTree`, as
  `internal/codegen`'s helpers do). Hooks: `afterCheck` (between the check
  and the write: swap a link in, rename the root away, break the store) and
  `beforeFile` (fail a write partway)

Checking the review in the real app (QA)

Until #77 or #97 stages anything, `TestQAStage` in `qa_test.go` prepares a
store. Start the agent with scratch `HOME` and `XDG_CONFIG_HOME` (`pnpm dev`
with both set), authorize the project root in the page, then stage with the
same two:

```
cd agent && HOME=$SCRATCH/home KPV_QA_CONFIG=$SCRATCH/config \
  KPV_QA_ROOT=$SCRATCH/home/project KPV_QA_DIRECTORY=app \
  KPV_QA_FILES=src/main.py,src/new.py KPV_QA_DELETE=src/old.py \
  go test ./internal/workcopy/ -run TestQAStage -count=1 -v
```

`KPV_QA_CONFIG` is the `XDG_CONFIG_HOME` the agent runs with. Each path in
`KPV_QA_FILES` gets a line appended (a new file holds the one line), each in
`KPV_QA_DELETE` is staged as a deletion; leave `KPV_QA_DIRECTORY` out for a
root that is itself the pipeline. Then open the pipeline in the page (go back
to the list and open it again if it was open): the review is read when it is opened. Without `KPV_QA_CONFIG` the test is skipped.
