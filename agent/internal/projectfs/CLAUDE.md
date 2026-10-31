# internal/projectfs

The agent's only door to the filesystem (`agent/CLAUDE.md` has the test that
holds that). The package comment in `guard.go` explains the two decisions it
rests on: paths are resolved on their real path, and every operation goes
through a held `*os.Root` descriptor, so the kernel refuses anything that
leaves a root.

- `Guard` holds the authorized roots and performs operations through them; it
  never hands out a path to open. `PipelineSeam` (`seam.go`) is the one
  exception, kept for `internal/pipeline` until #48
- `Browser` is read-only browsing of the authorized roots plus the home
  directory (`browse.go`, at most `MaxListing` entries)
- `Roots` (#63) is the user's set of project roots, kept in step with a file.
  `Roots.Authorize` and `Roots.Revoke` go through `Guard.Authorize` and
  `Guard.Revoke` on the guard the `Browser` shares, so a root is browsable the
  moment it is authorized
- a `Path` taken before `Guard.Revoke` or `Guard.Close` stops working
  (`Path.usable`); an operation already past that check finishes
  (`revoke_test.go`)
- a directory swapped between the check and the open is refused as `changed`,
  never followed (`os.SameFile`; `toctou_test.go`,
  `TestADirectorySwappedBetweenTheCheckAndTheOpenIsRefused`)

Project roots (`roots.go`, `store.go`, tests in `roots_test.go`, `gone_test.go`)

- a root is a readable directory strictly inside home. Outside home is
  `forbidden` whatever is there; `/`, home and anything above home are
  `tooBroad`; inside home a missing path is `notFound`, a file `notDirectory`,
  an unreadable directory `unreadable`. A link authorizes its real target, and
  the real path is what is answered and stored
- a directory inside or containing a stored root is `overlaps`, naming that
  root. A directory holding the agent's store is `forbidden`
- the store is `os.UserConfigDir()/kpv/roots.json`, chosen by `storeFile` in
  `agent/cmd/agent/main.go` (no config directory: roots last as long as the
  process). It is `{"version": 1, "roots": [...]}` and never the token,
  written atomically as `0600` in a `0700` directory. A corrupt or unreadable
  store is logged by kind, the agent starts with no roots, and it is moved to
  `roots.json.corrupt-<UTC time>` before anything is written; if it cannot be
  moved, nothing is written over it and an authorization answers `io`
- every stored root is checked again at startup, and again whenever something
  asks about it: every list, every authorize, every browse that lands in it
  (#67). One that is gone is `notFound`, `notDirectory` or `unreadable`; another
  directory at its path is `changed`. It is released from the guard at once,
  stays listed unavailable with its reason, and is available again only after
  an authorize of its path or a restart, never by being looked at. Watching the
  filesystem, and re-checking before each write (#43), are not done
- logs carry kinds, never a path
- tests pass `OpenRoots` a scratch store file; nothing here reads the real one
