// Package secrets keeps credentials out of source control and out of anything
// the agent logs or returns.
//
// It holds three things. Secret is the one type a credential value travels
// in: it formats, logs and marshals as a fixed placeholder (secret.go). The
// .env reader and merged write, and the .gitignore merged write, maintain the
// two files at a pipeline root, next to pipeline.yaml (env.go, gitignore.go,
// files.go).
//
// The two files are the user's, and the tool merges into them: its entries
// sit in one block between a start and an end marker comment (block.go), only
// that block is ever rewritten, and every user line outside it is kept byte
// for byte. They are not generated files: they do not go through
// internal/codegen's generators or manifest, and nothing here writes under
// generated/ or anywhere but those two names, apart from the temporary file
// each atomic replacement passes through, which the next write removes if an
// interrupted one left it.
//
// Every filesystem operation goes through the projectfs guard, and this
// package makes no filesystem call of its own (projectfs/boundary_test.go).
// The pipeline directory must be given as its real path, and each ReadEnv,
// WriteEnv and WriteGitignore pins it once (projectfs.Path.Pin) before
// anything is read and closes the pin before it returns. A link anywhere on
// the directory's path, there when the call is made or swapped in before the
// pin, is refused as the guard's changed. Once pinned, every Lstat, read,
// listing, removal and atomic write reaches .env, .gitignore and the
// temporary files by their single names through the pin, so both files land
// in the directory that was checked: one renamed during a call, or replaced
// at its name by a link, keeps getting that call's writes under its new name,
// and a .env is never written where the .gitignore just written is not.
//
// A .env or .gitignore that is a symbolic link is reported and never read,
// written through or replaced. What remains is a link swapped in at one of
// those two names itself, between its Lstat and its open or rename. At worst
// that reads a file inside the pinned directory, or replaces that link with
// the regular file being written; the pin keeps it from reaching outside the
// pipeline directory.
//
// Every message, error and log line names files, line numbers and keys, never
// a value or the content of a line.
package secrets
