# internal/process

The one package in the agent that may start a process (#37). The package
comment in `process.go` gives the reasons; these are the rules, and
`internal/projectfs/boundary_test.go` holds the module-wide half of them.

- only this package's non-test files may import `os/exec` or `syscall`. The
  boundary test allows it, through one `processAllowance` entry with its
  reason, to import those two. Of `os/exec` it may use only
  `CommandContext`, `LookPath`, `ErrWaitDelay` and `Cmd`, the last only as
  `*exec.Cmd`, so no `exec.Cmd` literal, `new(exec.Cmd)` or `Cmd` variable can
  start a process beside the one `exec.CommandContext` reference (`Run`). Of
  `syscall` it may use only `SysProcAttr`, `Kill`, `SIGKILL`, `Signal` and
  `WaitStatus`. `exec.Command`, `os.StartProcess`, `syscall.Exec`,
  `syscall.ForkExec`, `syscall.StartProcess`, `syscall.CreateProcess`, every
  `golang.org/x/sys` import and every shell or `-c` string stay banned here
  too. The allowance fails as stale if this package stops importing `os/exec`
- the boundary test resolves every name through the file's imports, and
  refuses `os`, `os/exec`, `syscall`, `path/filepath`, `io/ioutil` and `io/fs`
  imported under any other name (renamed, `.` or `_`) anywhere in the module.
  A reference is banned, called or not (`var f = os.StartProcess`). A string
  is compared unquoted (`"\x2dc"` is `-c`) and folded through `+` and string
  constants (`"s" + "h"`); a string built at run time is not seen, which is
  why a child's arguments come only from the table.
  `TestTheCommandScanCatchesEveryPlantedViolation` feeds each of these to the
  same scan
- a file here carries no build constraint, `//go:build unix` or
  `//go:build !unix`, and nothing else: no other tag (`cgo`, `linux && cgo`,
  a custom `-tags` name), no `// +build` line and no `_<GOOS>` or
  `_<GOARCH>` file name. So there are two file sets, and both are type
  checked (`processTypeViolations`, go/types with the compiler's export data
  for the standard library the test runs with), against allowlists rather
  than bans:
  - it may import only `bytes`, `context`, `errors`, `fmt`, `os`, `os/exec`,
    `path/filepath`, `strings`, `sync`, `syscall` and `time`, so `reflect`,
    `unsafe`, `plugin` and cgo's `"C"` fail by being absent. It declares no
    type parameter or generic type and instantiates no generic, so no type
    argument can make an `exec.Cmd`
  - of `os/exec` it uses only `CommandContext`, `LookPath`, `ErrWaitDelay` and
    `Cmd`; of `os` only `Getenv`, `LookupEnv` and `ProcessState`; of
    `syscall` only the five names above. Each is resolved by the type
    checker, so no alias or function value reaches another
  - `exec.CommandContext` appears once, as the callee of a call, in `Run`;
    as a value it fails. Every `*exec.Cmd` value and every use of the name
    `exec.Cmd` is inside `Run` (the process group's attributes come from
    `processGroup()`, so no helper holds the command). No expression has type
    `exec.Cmd` anywhere, so a literal, a copy (`d := *c`) or a dereference
    fails, and so does `new(exec.Cmd)`
  - of `exec.Cmd`'s fields and methods it selects only `Args` (as
    `Args[...]`), `Env`, `Dir`, `Stdin`, `Stdout`, `Stderr`, `WaitDelay`,
    `SysProcAttr`, `Cancel`, `Process`, `ProcessState`, `Start` and `Wait`,
    through an embedding too, so the command cannot be pointed at another
    `Path` or started by `Run` or `Output`. `Start` and `Wait` only as the
    callee of a call (no method value or expression), `Cancel` only as an
    assignment's target
  - nothing made in `Run` can leave it: the package's one function literal is
    the command's `Cancel` assigned in `Run` (the stdout cap holds the
    run's cancel function, not a closure), and nothing reads `Cancel` back.
    No package-level variable can hold a function (no func, chan, pointer or
    non-`error` interface in its type), none is written to after its
    declaration (assignment, `++`, `range`) and none has its address taken
  - a file that does not type check fails.
    `TestTheTypedProcessScanCatchesEveryPlantedRoute` (round 2) and
    `TestTheTypedProcessScanCatchesEveryRoundFourRoute` plant each route
    review found next to the shipped files, among them a closure in `Run`
    handing the call to an exported variable, and `cgo`, `linux && cgo` and
    custom-tagged files
  - not seen: the non-unix set is checked against the standard library of the
    platform the test runs on, not that of Windows; `GOOS=windows go vet`
    covers the rest. A string built at run time is not seen by the literal
    ban; a child's arguments come only from the table
- module-wide, in every file test files included,
  `TestNoFileInTheModuleReachesAProcessAnotherWay` refuses `import "C"` (cgo,
  whose `C.system` runs a shell), `net/http/cgi` (its `Handler` runs the
  program it names) and `plugin`, refuses `go/build` and `go/importer` (both
  run the go command) outside test files, where the boundary test uses
  `go/importer` to type check, and refuses any non-Go source the go command
  would build into a package (`.s`, `.S`, `.sx`, `.c`, `.cc`, `.cxx`, `.cpp`,
  `.m`, `.h`, `.hh`, `.hpp`, `.hxx`, `.f`, `.F`, `.for`, `.f90`, `.swig`,
  `.swigcxx`, `.syso`), since none of the scans read it. The module-wide
  syntax scans read every `.go` file whatever its build constraint
- the walks skip `testdata` and `.`-prefixed directories, so
  `TestEveryPackageTheGoCommandBuildsIsScanned` refuses one that holds a
  `.go` file anywhere under it (a `testdata` with only fixtures is fine), and
  any symbolic link to a directory, which the walks do not follow. It then
  runs `go list -deps -test ./...` and fails on any module directory the go
  command builds that the walk did not read.
  `TestTheWalkRefusesEveryPlantedHidingPlace` plants each in a scratch module
- this package imports no other agent package (`source_test.go`)
- `Run(ctx, program, stdin)` runs only a `Program`, whose fields are
  unexported. Every non-test `Program` comes from the table in `programs.go`
  (`Lookup`), which has no entry after #37. A `Program` is built only there,
  in `newProgram`: a `Program` literal, `new(Program)`, a variable declared as
  a `Program` or an assignment to one of its fields anywhere else in the
  package fails `TestOnlyProgramsGoBuildsAProgram`, and
  `TestTheProgramCheckCatchesEveryPlantedBuild` plants each
  (`source_test.go`). A literal whose type is left out (`[]Program{{...}}`)
  is found by type checking (`programs_types_test.go`). The zero `Program`,
  the only one another package can write, is `invalidProgram` and starts
  nothing
- an entry fixes a bare executable name looked up on `PATH`, its whole
  argument list, the environment variable names passed through, a timeout and
  a stdout cap. `TestEveryProgramInTheTableIsWellFormed` (`programs_test.go`)
  refuses an empty name, a `/` or `\`, a shell name or `-c` from the boundary
  test's `bannedLiterals` (read from that file, so the lists cannot drift), and
  a timeout or cap of zero or less
- `ForTest(executable, args, env, limits)` (`fortest.go`) is the only way to
  build an arbitrary `Program`, and the boundary test fails if any non-test
  file but `fortest.go` names it. Tests run the test binary again through it:
  `TestMain` hands a run whose first argument is the fake's marker to the
  fake (`fake_test.go` here, `internal/parser/fake_test.go`)
- `Run` takes nothing else: no argument, environment, working directory or
  executable from its caller. Input reaches the child only as `stdin`, which
  is written and then closed; a child that exits 0 without reading it
  succeeds
- the executable is looked up on `PATH` on every call, so a toolchain
  installed after the agent started is found. A name found only through a
  relative `PATH` entry (the current directory included), or a relative path,
  is `notFound` and nothing is started
- the child's environment is exactly `PATH` plus the program's listed names
  that the agent has set, with the agent's values; its working directory is
  `/`
- every run is bounded: the program's timeout or the context's deadline,
  whichever is first (`timedOut`; a context canceled before its deadline is
  `canceled`), the stdout cap (one byte more kills the child: `outputTooLarge`,
  no stdout returned), and `MaxStderr` (64 KiB) of stderr kept with
  `StderrTruncated`; more stderr is read and dropped, never blocking or
  killing the child
- the child leads a process group of its own (`group_unix.go`). A context
  that ends kills the whole group, and after every `Run`, whatever the
  outcome, the group is killed again, so a grandchild in it never outlives the
  run. `WaitDelay` (250 ms) bounds the wait for pipes a grandchild that left
  the group still holds, so `Run` returns within a second of the kill
- off unix (`group_other.go`, `//go:build !unix`) `Run` answers `unsupported`
  and starts nothing; job objects on Windows are #95. Only `run_test.go` and
  `fake_test.go` are unix-only; `GOOS=windows go vet` and
  `GOOS=darwin go vet` pass for this package
- every error is a `*process.Error` with a `Kind` from a fixed set:
  `invalidProgram`, `notFound`, `exited` (with `ExitCode`), `signaled` (with
  `Signal`, the OS's description such as `terminated`), `timedOut`,
  `canceled`, `outputTooLarge`, `unsupported`, `startFailed`. `Stderr` holds
  what was kept wherever a child ran. A file that is executable but not a
  program is `startFailed`
- `Run` is safe to call concurrently (`TestRunsInParallelDoNotMix` under
  `-race`)

Adding a program (#38, #39, #51): add a `Name` constant and its entry to
`programs.go`, built with `newProgram`, and put the package that runs it on
`processImporters` in `internal/projectfs/boundary_test.go` with its reason.
`internal/httpapi` is never on that list (`internal/httpapi/source_test.go`
bans the import beside `os/exec`). A cap on concurrent children is #94.
