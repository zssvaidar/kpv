# internal/parser

The harness every source parser runs in (#37). A parser reports, as contract
JSON (`schema/parser.schema.json`, generated into `models_gen.go` here), the
stages it recognized in one source file, the regions it could not, and its
warnings. The real parsers and their `programs.go` entries are #38, #39 and
#40; mapping a result onto a pipeline is #42.

- one type, `Parser`, with `Parse(ctx, source []byte) (*Result, error)` and two
  constructors: `Sidecar(process.Program, Options)` writes the source to the
  program's stdin and reads the result from its stdout; `InProcess(func,
Options)` calls a function that returns the same contract bytes. `Result` is
  the generated `ParserResult`
- the harness takes bytes and never a path: it imports no agent package but
  `internal/process`, and neither `os` nor `path/filepath`
  (`source_test.go`). The caller reads the source through the path guard
  (`catalog.ReadSource`)
- before any parser starts, a source over `MaxSource` (1 MiB, the same as
  `catalog.MaxFileSize`, held by a test) is `sourceTooLarge`, and one that is
  not UTF-8 or holds a NUL is `invalidSource`, matching catalog's `tooLarge`
  and `notText`
- `Options` sets a timeout (default 10 s) and an output cap (default 8 MiB),
  applied the same to both transports: the timeout as the context's deadline,
  the cap to the bytes the transport hands back. A sidecar's program also
  carries its own timeout and stdout cap, and whichever is smaller wins
- both transports hand their output to `Validate`, the one validation
  function, and `TestEveryMalformedOutputIsRefusedTheSameWayByBothTransports`
  runs every malformed case, every invalid shared fixture among them, through
  each and gets the same kind and problems. Output passes only when it is UTF-8
  and exactly one JSON document with only whitespace after it, no object has a
  key twice, it passes the schema, every range has `0 <= start < end <=
len(source)` and starts and ends on a UTF-8 character boundary, no two
  stages or regions overlap (touching, `end == start`, is not overlapping;
  warnings' ranges are not compared), and an `unparseable` result has no
  stages and at least one warning. Otherwise it is `malformed`, with
  `Problems`: a JSON pointer and a fixed sentence each, sorted by pointer and
  then message
- on any error the `Result` is nil, so a rejected result is never partly
  returned. A successful `Result` has its stages and unrecognized regions
  sorted by `start`; warnings keep their order. An offset may be written
  `3`, `3.0` or `3e0`; a stage's configuration keeps numbers as `json.Number`
- errors are a `*parser.Error` with a `Kind` from a fixed set
  (`sourceTooLarge`, `invalidSource`, `unavailable`, `crashed`, `panicked`,
  `timedOut`, `canceled`, `outputTooLarge`, `malformed`, `unsupported`) and
  `ExitCode`, `Signal`, `Stderr`, `StderrTruncated`, `Problems`
- a sidecar maps `process` kinds: `notFound` (and the zero program's
  `invalidProgram`) is `unavailable`; `exited`, `signaled` and `startFailed`
  are `crashed` with what was kept of stderr; `timedOut`, `canceled`,
  `outputTooLarge` and `unsupported` keep their names. A sidecar that writes a
  valid result and exits non-zero is `crashed`: its stdout is never validated
  or returned. Stderr from a sidecar that exits 0 is discarded
- an in-process parser gets its own copy of the source. One that returns an
  error is `crashed` with the error's text in `Stderr`, even if it returned
  bytes too; one that panics is `panicked` with the panic value's text in
  `Stderr`, and the agent keeps running. One that ignores its context is still
  answered `timedOut` at the deadline, but its goroutine runs on until the
  function returns, because a goroutine cannot be killed; what it returns then
  is dropped
- the fakes are in `fake_test.go`: the fake sidecar is the test binary run
  again through `process.ForTest`, and both it and the in-process fake are
  told what to do by one `behaviours` table (valid, malformed, overlapping,
  out of bounds, splitting a character, a crash with stderr, a valid result
  with a failure, a signal, a hang, too much output, a panic). The source is
  `src("é")\nsink()`, so ranges around the two-byte é are checked. The
  sidecar tests are unix-only (`//go:build unix`)
