package secrets

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// Kind says what a Problem is about, so a caller branches on a value rather
// than on the wording of a message.
type Kind string

const (
	// KindInvalidKey is a key that is not an environment variable name,
	// [A-Za-z_][A-Za-z0-9_]*. Nothing is written.
	KindInvalidKey Kind = "invalidKey"
	// KindInvalidValue is a value holding a line break or a NUL byte, which no
	// .env line can carry. Nothing is written.
	KindInvalidValue Kind = "invalidValue"
	// KindSetOutsideBlock is a key the write was asked to set that a user line
	// outside the marker block already sets. Nothing is written, and the
	// user's line is left alone; #68 relies on this to stop a deploy.
	KindSetOutsideBlock Kind = "setOutsideBlock"
	// KindDuplicateKey is a key set on more than one user line. A warning.
	KindDuplicateKey Kind = "duplicateKey"
	// KindMalformedLine is a .env line that is not blank, a comment or an
	// entry. A warning; the line is kept as it is.
	KindMalformedLine Kind = "malformedLine"
	// KindBlockEdited is a marker block whose lines are not the ones this
	// package last wrote there: a user line moved or edited inside it.
	// Nothing is written.
	KindBlockEdited Kind = "blockEdited"
	// KindMarkers is a file whose markers do not make exactly one block: a
	// start with no end, an end before a start, or two blocks. Nothing is
	// written.
	KindMarkers Kind = "markers"
	// KindLink is a .env or .gitignore that is a symbolic link. It is not
	// read, written through, or replaced. Nothing is written.
	KindLink Kind = "link"
	// KindNotRegularFile is a .env or .gitignore that is a directory or
	// anything else that is not a regular file. Nothing is written.
	KindNotRegularFile Kind = "notRegularFile"
	// KindTooLarge is a file larger than MaxFileSize. It is not read.
	KindTooLarge Kind = "tooLarge"
	// KindRefused is a refusal or failure of the projectfs guard; Guard holds
	// its kind (forbidden, unreadable, notFound, io, ...).
	KindRefused Kind = "refused"
	// KindNotIgnored is a .gitignore with a user line that un-ignores a name
	// the block would ignore. A warning; the line is kept, and the block does
	// not override it.
	KindNotIgnored Kind = "notIgnored"
	// KindExposedMode is an existing .env readable by group or others. A
	// warning; the mode is kept.
	KindExposedMode Kind = "exposedMode"
	// KindLeftover is a temporary file from an interrupted write that could
	// not be removed. A warning.
	KindLeftover Kind = "leftover"
)

// Problem is one thing a read or write found. It names a file, line numbers
// and a key, and never a value or the content of a line, because a line of a
// .env file may hold one.
type Problem struct {
	Kind Kind
	// Warning is true for a problem that did not stop the write.
	Warning bool
	// File is the file's name inside the pipeline directory, EnvFile or
	// GitignoreFile, or "" for the directory itself.
	File string
	// Lines are the 1-based line numbers the problem is about, if any.
	Lines []int
	// Key is the entry's key, if the problem is about one.
	Key string
	// Guard is the projectfs kind of a KindRefused problem.
	Guard projectfs.ErrorKind
	// Message is a human-readable explanation, not a stable identifier.
	Message string
}

func (p Problem) String() string {
	var out strings.Builder
	if p.File != "" {
		out.WriteString(p.File)
	} else {
		out.WriteString("pipeline directory")
	}
	if len(p.Lines) > 0 {
		out.WriteString(" line")
		if len(p.Lines) > 1 {
			out.WriteString("s")
		}
		for i, line := range p.Lines {
			if i > 0 {
				out.WriteString(",")
			}
			out.WriteString(" ")
			out.WriteString(strconv.Itoa(line))
		}
	}
	if p.Key != "" {
		fmt.Fprintf(&out, " key %s", p.Key)
	}
	out.WriteString(": ")
	out.WriteString(p.Message)
	return out.String()
}

// Error is a read or write that was refused. Problems lists every reason
// found; nothing named in Report as notWritten was changed.
type Error struct {
	// Op is "read", "write" or "gitignore".
	Op string
	// Problems are the problems that stopped it, warnings excluded.
	Problems []Problem
	// guard is the projectfs error behind the first KindRefused problem, so
	// projectfs.KindOf and errors.As reach it.
	guard error
}

func (e *Error) Error() string {
	parts := make([]string, len(e.Problems))
	for i, problem := range e.Problems {
		parts[i] = problem.String()
	}
	return "secrets " + e.Op + ": " + strings.Join(parts, "; ")
}

// Unwrap returns the guard's own error, when a guard refusal is among the
// problems. Its message carries paths and kinds, never file content.
func (e *Error) Unwrap() error { return e.guard }

// Kinds lists the kinds of the problems, which is what a test or a caller
// branching on the outcome usually wants.
func (e *Error) Kinds() []Kind {
	out := make([]Kind, len(e.Problems))
	for i, problem := range e.Problems {
		out[i] = problem.Kind
	}
	return out
}

// Outcome is what happened to one file.
type Outcome string

const (
	// Created is a file that was not there and now is.
	Created Outcome = "created"
	// Updated is a file whose block was added or rewritten.
	Updated Outcome = "updated"
	// Unchanged is a file that already held exactly the bytes to write.
	Unchanged Outcome = "unchanged"
	// NotWritten is a file that was left exactly as it was because the write
	// was refused or failed.
	NotWritten Outcome = "notWritten"
)

// Report is what a write did.
type Report struct {
	Gitignore Outcome
	// Env is "" for WriteGitignore, which does not touch .env.
	Env Outcome
	// EnvIgnored is whether the pipeline-root .gitignore, as written, ignores
	// .env under gitignore's last-matching-pattern rule. It is false when a
	// user line un-ignores it, which is also reported as KindNotIgnored.
	EnvIgnored bool
	// Problems holds the warnings, and for a refused write also the problems
	// that refused it.
	Problems []Problem
}

// refusal is the error for problems, carrying the first guard error found.
func refusal(op string, problems []Problem, guardErr error) error {
	blocking := make([]Problem, 0, len(problems))
	for _, problem := range problems {
		if !problem.Warning {
			blocking = append(blocking, problem)
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	return &Error{Op: op, Problems: blocking, guard: guardErr}
}

// guardProblem turns a projectfs error into a Problem that carries its kind.
// The message is the kind's own wording, not the error's, so nothing the
// error happens to hold travels further than the Unwrap chain.
func guardProblem(file string, err error) Problem {
	kind := projectfs.KindOf(err)
	if kind == "" {
		kind = projectfs.KindIO
	}
	var message string
	switch kind {
	case projectfs.KindForbidden:
		message = "refused by the path guard: not inside an authorized project directory"
	case projectfs.KindNotFound:
		message = "does not exist"
	case projectfs.KindUnreadable:
		message = "cannot be read or written by this process"
	case projectfs.KindNotDirectory:
		message = "a component of the path is not a directory"
	default:
		message = "the filesystem operation failed (" + string(kind) + ")"
	}
	return Problem{Kind: KindRefused, File: file, Guard: kind, Message: message}
}
