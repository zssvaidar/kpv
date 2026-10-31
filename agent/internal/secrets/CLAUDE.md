# internal/secrets

Credentials (#23): the one type a value travels in, and the merged writes of
`.env` and `.gitignore` at a pipeline root. The package comment in `doc.go`
explains the model; the running agent uses `Secret` for stored connection
credentials (#24); deploy (#68) is the first caller of the `.env` writes, and
#74 writes the `.gitignore` block when a pipeline is created.

- `Secret` (`secret.go`) formats, logs and marshals as `Secret(redacted)` and
  keeps the plaintext in a closure, not a field, for the reason
  `internal/httpapi/token.go` gives. `Reveal` is the only way to the value;
  its one caller here is the `.env` writer (`secret_test.go`, including a
  panic in a child process). Outside this package, the one caller is the
  connection store's file write, `encodeStore` in `internal/connections`
  (#24, held by its `source_test.go`)
- `Files` (`files.go`) takes the guard and a pipeline directory as a string,
  so a directory outside the roots is the guard's `forbidden`, carried as
  `KindRefused` with `Guard` set. It writes only `.env` and `.gitignore` in
  that directory, never under `generated/`, and is not #16's generator or
  manifest (`TestNothingIsWrittenOutsideTheTwoFiles`)
- each `ReadEnv`, `WriteEnv` and `WriteGitignore` pins the pipeline directory
  once with `projectfs.Path.Pin` before reading anything, reaches every file by
  its single name through `PinnedDir.Path()`, and closes the pin on return. The
  directory must be given as its real path: a link anywhere on it, given or
  swapped in before the pin, is `KindRefused` with `Guard: changed`. A
  relative directory is resolved against the working directory, so outside
  every root it is `Guard: forbidden` (`TestARelativePipelineDirectoryIsForbidden`),
  and `changed` only when it resolves inside a root. A
  directory renamed during a call, or replaced at its name by a link, keeps
  getting that call's writes under its new name (`pin_test.go`, including a
  `-race` loop against a goroutine swapping it)
- what remains unpinned is a link swapped in at `.env` or `.gitignore` itself
  between its `Lstat` and its open or rename: at worst it reads a file inside
  the pinned directory or replaces that link with the regular file, and never
  reaches outside the pipeline directory
- each file's entries sit between `StartMarker` and an end marker holding the
  SHA-256 of the lines between (`block.go`). A block whose lines do not hash to
  it was edited by hand and is refused (`blockEdited`); deleting the whole
  block clears that, and the next write appends a new one. Markers that do not make one block are refused
  too. Only the block is rewritten; every other byte is kept, BOM and CRLF
  included, and the block uses the file's line ending. A block appended after a
  last line with no newline adds one line ending (the file's) after that line,
  and its end marker has none, so the file still does not end with a newline
- `WriteEnv(ctx, dir, set, remove)` is a merge: the block keeps its entries
  less the keys in `remove`, and gains or replaces the keys in `set`, sorted,
  in one atomic write. Removing a key the block does not hold, or one only a
  user line sets, changes nothing and says nothing; a key both set and
  removed is `KindSetAndRemoved`, an invalid name `KindInvalidKey`. Everything is
  checked before anything is written; `.gitignore` is written before `.env`,
  and a failed `.gitignore` write means no `.env` write
- a key a user line outside the block sets is refused (`KindSetOutsideBlock`);
  #68 relies on that to stop a deploy
- the value quoting rule is in `env.go`: always `KEY="..."` with `\`, `"` and
  `$` each backslash-escaped and nothing else changed; a value with a line
  feed, a carriage return or a NUL byte is refused by key. Checked by hand against `docker compose config`
  (Compose v5.6); the container check is #68's
- the `.gitignore` block holds `.env` and `..env.tmp-*`, each only when no user
  line matches that name; a user negation is kept and warned about
  (`KindNotIgnored`). The matcher in `gitignore.go` is git's rule for a file at
  the root of the `.gitignore`'s directory, and `gitignore_test.go` checks it
  against `git check-ignore` for each fixture. Only the pipeline-root
  `.gitignore` is read
- a link at either name is refused and never read, written through or
  replaced; a file over `MaxFileSize` (1 MiB) is not read. A new `.env` is
  `0600` whatever the umask, an existing one keeps its mode and is warned about
  when group or others can read it. Leftover `..env.tmp-*` and
  `..gitignore.tmp-*` regular files are removed by the next `WriteEnv`
- two writes for one directory in one agent never interleave (`lock.go`, the
  same lock shape as `internal/codegen`)
- no message, error or log line holds a value or a line's content
  (`leak_test.go` writes a known value through every failure path); log lines
  carry kinds, file names, lines and keys, never a path
- tests that act inside a call set `Files.hooks` (before the pin, before the
  `.gitignore` write, between the two writes, during the `.env` write); `New`
  sets none
- imports only `internal/projectfs` from the agent, the standard library
  otherwise, and no `os` or `path/filepath` (`source_test.go`). Tests run
  `git`, but only in scratch directories with no user or system git config
