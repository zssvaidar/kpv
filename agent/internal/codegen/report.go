package codegen

import (
	"fmt"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// Status is what a run did, or did not do, with one path.
type Status string

const (
	// StatusWritten is a file the run wrote: new, or replacing a generated
	// file the user had not changed.
	StatusWritten Status = "written"
	// StatusUnchanged is a file that already held exactly the bytes the run
	// would have written, and was left as it was.
	StatusUnchanged Status = "unchanged"
	// StatusRegenerated is a file the manifest listed and that had been
	// deleted, written again.
	StatusRegenerated Status = "regenerated"
	// StatusModified is a generated file whose bytes no longer hash to what
	// the manifest recorded: someone edited it. It was not overwritten.
	StatusModified Status = "modified"
	// StatusUnowned is a file the manifest does not list, at a path the run
	// would have written. It was not overwritten.
	StatusUnowned Status = "unowned"
	// StatusNotRegularFile is a path the run would have written that is a
	// directory, a symbolic link or anything else that is not a regular file.
	// It was left in place, whatever a link points at.
	StatusNotRegularFile Status = "notRegularFile"
	// StatusOrphaned is a file the manifest lists that no generator emits
	// any more. It stays on disk and in the manifest.
	StatusOrphaned Status = "orphaned"

	// StatusRefused is a path a generator returned that may not be written
	// at all; Reason says why.
	StatusRefused Status = "refused"
	// StatusBlocked is a path whose directory, at At under generated/, is a
	// symbolic link or not a directory; Reason says which.
	StatusBlocked Status = "blocked"
	// StatusFailed is a path the guard refused or the filesystem failed on;
	// Kind says how, and forbidden is the guard refusing it.
	StatusFailed Status = "failed"
	// StatusNotWritten is a file the run would have written and did not,
	// because the run stopped first: a conflict elsewhere, a failure, or the
	// run being cancelled. It still holds its previous bytes.
	StatusNotWritten Status = "notWritten"
)

// conflict reports whether a status stops a run from writing anything.
func (s Status) conflict() bool {
	switch s {
	case StatusModified, StatusUnowned, StatusNotRegularFile, StatusRefused, StatusBlocked, StatusFailed:
		return true
	}
	return false
}

// FileReport is what happened to one path. It never holds file contents.
type FileReport struct {
	// Path is relative to generated/, as the generator or the manifest wrote
	// it.
	Path string
	// Status is what happened.
	Status Status
	// Reason refines StatusRefused, StatusBlocked and a file that changed
	// between its check and its write.
	Reason Reason `json:",omitempty"`
	// At is the directory, relative to generated/, that StatusBlocked is
	// about.
	At string `json:",omitempty"`
	// Kind is the guard's error kind for StatusFailed, and for a file that
	// is reported with a status but could not be fully examined.
	Kind projectfs.ErrorKind `json:",omitempty"`
}

// ProblemKind is why a run, or a read of the manifest, did not complete.
type ProblemKind string

const (
	// ProblemInvalidModel is a nil pipeline model.
	ProblemInvalidModel ProblemKind = "invalidModel"
	// ProblemGeneratorFailed is a generator returning an error.
	ProblemGeneratorFailed ProblemKind = "generatorFailed"
	// ProblemInvalidOutput is one or more refused paths; each is in Files.
	ProblemInvalidOutput ProblemKind = "invalidOutput"
	// ProblemConflicts is one or more files that may not be overwritten;
	// every one found is in Files.
	ProblemConflicts ProblemKind = "conflicts"
	// ProblemGeneratedIsLink is generated/ itself being a symbolic link.
	ProblemGeneratedIsLink ProblemKind = "generatedIsLink"
	// ProblemGeneratedNotDirectory is generated/ being a regular file or
	// anything else that is not a directory.
	ProblemGeneratedNotDirectory ProblemKind = "generatedNotDirectory"
	// ProblemManifestUnreadable is a manifest that is there and cannot be
	// read: not a regular file, or the guard or filesystem refusing it.
	ProblemManifestUnreadable ProblemKind = "manifestUnreadable"
	// ProblemManifestInvalid is a manifest that does not parse, or parses to
	// something this package did not write.
	ProblemManifestInvalid ProblemKind = "manifestInvalid"
	// ProblemManifestUnknownVersion is a manifest with a version other than
	// ManifestVersion.
	ProblemManifestUnknownVersion ProblemKind = "manifestUnknownVersion"
	// ProblemGuard is the guard or the filesystem refusing the pipeline
	// directory or generated/ itself; Kind says how.
	ProblemGuard ProblemKind = "guard"
	// ProblemWriteFailed is a write failing part-way. Files says which were
	// written and which were not.
	ProblemWriteFailed ProblemKind = "writeFailed"
	// ProblemInterrupted is the run's context ending, while waiting for
	// another run or part-way through writing.
	ProblemInterrupted ProblemKind = "interrupted"
)

// Problem is a run-level failure. Like the rest of a report, it carries no
// file contents and no absolute path.
type Problem struct {
	Kind ProblemKind
	// Generator is the failing generator's name, for ProblemGeneratorFailed.
	Generator string `json:",omitempty"`
	// GuardKind is the guard's error kind, when the guard or the filesystem
	// is what failed.
	GuardKind projectfs.ErrorKind `json:",omitempty"`
}

// Report is the structured answer of a run.
type Report struct {
	// Files is one entry per path the run has something to say about,
	// sorted by path: every output, and every orphan.
	Files []FileReport
	// ManifestWritten says the run wrote a new manifest. A run that found
	// the manifest already describing what is on disk writes none.
	ManifestWritten bool
	// Problem is why the run did not complete, nil when it did.
	Problem *Problem `json:",omitempty"`
}

// Error is the error a run or an inspection returns when it does not
// complete. It holds the same Problem the report does.
type Error struct {
	Problem Problem
	// Err is the cause, when there is one: the generator's error, or the
	// guard's *projectfs.PathError. It is for the agent's own logs; the
	// report is what is meant to be passed on.
	Err error
}

func (e *Error) Error() string {
	message := fmt.Sprintf("code generation: %s", e.Problem.Kind)
	if e.Problem.Generator != "" {
		message += fmt.Sprintf(" (generator %q)", e.Problem.Generator)
	}
	if e.Problem.GuardKind != "" {
		message += fmt.Sprintf(" (%s)", e.Problem.GuardKind)
	}
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *Error) Unwrap() error { return e.Err }

// guardProblem turns a guard failure into the problem that carries its kind.
func guardProblem(kind ProblemKind, err error) *Error {
	return &Error{Problem: Problem{Kind: kind, GuardKind: kindOf(err)}, Err: err}
}

// kindOf is the guard's kind for err, io when the error is not the guard's.
func kindOf(err error) projectfs.ErrorKind {
	if kind := projectfs.KindOf(err); kind != "" {
		return kind
	}
	return projectfs.KindIO
}
