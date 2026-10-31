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
// A file is reached with Path.Join and Lstat, so a .env or .gitignore that is
// a symbolic link is reported and never read, written through or replaced.
// What is not caught: a link swapped in at one of those names in the moment
// between the Lstat and the open or the rename. The root's descriptor still
// keeps that inside the authorized root; closing the window needs a no-follow
// open, which projectfs does not offer.
//
// Every message, error and log line names files, line numbers and keys, never
// a value or the content of a line.
package secrets
