# internal/secrets

Credentials (#23): the one type a value travels in, and the merged writes of
`.env` and `.gitignore` at a pipeline root. The package comment in `doc.go`
explains the model; nothing in the running agent calls it yet (the first
caller is deploy, #68; #24 uses `Secret` for stored credentials, #74 writes the
`.gitignore` block when a pipeline is created).

- `Secret` (`secret.go`) formats, logs and marshals as `Secret(redacted)` and
  keeps the plaintext in a closure, not a field, for the reason
  `internal/httpapi/token.go` gives. `Reveal` is the only way to the value;
  its one caller here is the `.env` writer (`secret_test.go`, including a
  panic in a child process)
- `Files` (`files.go`) takes the guard and a pipeline directory as a string,
  so a directory outside the roots is the guard's `forbidden`, carried as
  `KindRefused` with `Guard` set. It writes only `.env` and `.gitignore` in
  that directory, never under `generated/`, and is not #16's generator or
  manifest (`TestNothingIsWrittenOutsideTheTwoFiles`)
- each file's entries sit between `StartMarker` and an end marker holding the
  SHA-256 of the lines between (`block.go`). A block whose lines do not hash to
  it was edited by hand and is refused, as are markers that do not make one
  block. Only the block is rewritten; every other byte is kept, BOM, CRLF and a
  missing final newline included, and the block uses the file's line ending
- `WriteEnv` is a merge: the block keeps its entries and gains or replaces the
  given keys, sorted. There is no way to remove a key yet. Everything is
  checked before anything is written; `.gitignore` is written before `.env`,
  and a failed `.gitignore` write means no `.env` write
- a key a user line outside the block sets is refused (`KindSetOutsideBlock`);
  #68 relies on that to stop a deploy
- the value quoting rule is in `env.go`: always `KEY="..."` with `\`, `"` and
  `$` backslash-escaped. Checked by hand against `docker compose config`
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
- tests that act during the `.env` write set `Files.hooks`; `New` sets none
- imports only `internal/projectfs` from the agent, the standard library
  otherwise, and no `os` or `path/filepath` (`source_test.go`). Tests run
  `git`, but only in scratch directories with no user or system git config
