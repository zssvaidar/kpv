package catalog

import (
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// ErrorKind says why an Open, a Create, a Load or a Save failed. The values
// are the ones schema/pipelines.schema.json's PipelinesErrorKind names.
type ErrorKind string

const (
	// KindInvalid is a request that is not well formed: an empty root, a
	// directory name that is empty, ".", ".." or holds "/", "\" or NUL, or a
	// create whose fields fail the pipeline schema (Problems says where).
	KindInvalid ErrorKind = "invalid"
	// KindNotAuthorized is a root the roots store does not hold, or no longer
	// holds because it was revoked.
	KindNotAuthorized ErrorKind = "notAuthorized"
	// KindRootUnavailable is a stored root that is unavailable; Reason says
	// why.
	KindRootUnavailable ErrorKind = "rootUnavailable"
	// KindNotFound is no pipeline at that location, or one the list would not
	// show: a directory that is a link or starts with ".", or a directory in
	// a root that is itself a pipeline.
	KindNotFound ErrorKind = "notFound"
	// KindUnreadable is a pipeline.yaml that is there and did not load;
	// Unreadable says why.
	KindUnreadable ErrorKind = "unreadable"
	// KindRootIsPipeline is a create into a root that is itself a pipeline,
	// where the list would never show a pipeline nested in it.
	KindRootIsPipeline ErrorKind = "rootIsPipeline"
	// KindChanged is a directory a create made that was replaced before it
	// could be pinned, so nothing was written.
	KindChanged ErrorKind = "changed"
	// KindNoFreeID is a create whose every derived id is taken.
	KindNoFreeID ErrorKind = "noFreeId"
	// KindIO is anything else.
	KindIO ErrorKind = "io"
)

// Error is the one failure this package reports. It carries kinds and the
// document's own problems, never a path or the text of an underlying error,
// so it can be logged and answered as it is.
type Error struct {
	Kind ErrorKind
	// Reason is why the root is unavailable, set with KindRootUnavailable.
	Reason projectfs.ErrorKind
	// Unreadable is set with KindUnreadable.
	Unreadable *Unreadable
	// Problems is where a pipeline a create or a save would write fails the
	// pipeline schema, set with KindInvalid when that is why.
	Problems []Problem
	// DirectoryLeftBehind is set on a create that failed after it made the
	// pipeline's directory: whether that directory is still there. It is
	// true when the directory could not be shown to be the empty one the
	// create made, which is then never removed.
	DirectoryLeftBehind *bool
}

func (e *Error) Error() string {
	out := "pipeline catalog: " + string(e.Kind)
	if e.Reason != "" {
		out += " (" + string(e.Reason) + ")"
	}
	if e.Unreadable != nil {
		out += " (" + string(e.Unreadable.Kind) + ")"
	}
	if e.DirectoryLeftBehind != nil {
		if *e.DirectoryLeftBehind {
			out += ", the new directory is left behind"
		} else {
			out += ", the new directory was removed"
		}
	}
	return out
}

// KindOf is the kind of a *Error, "" for anything else.
func KindOf(err error) ErrorKind {
	if fault, ok := err.(*Error); ok && fault != nil {
		return fault.Kind
	}
	return ""
}

// rootFailure is the error for a root that cannot be used: notAuthorized when
// the guard no longer holds it (forbidden: revoked or never authorized),
// rootUnavailable with the kind otherwise.
func rootFailure(kind projectfs.ErrorKind) *Error {
	if kind == projectfs.KindForbidden {
		return &Error{Kind: KindNotAuthorized}
	}
	return &Error{Kind: KindRootUnavailable, Reason: rootReason(kind)}
}

// rootReason narrows a projectfs kind to the reasons a root can be listed
// unavailable with (schema/pipelines.schema.json's PipelineRootReason).
func rootReason(kind projectfs.ErrorKind) projectfs.ErrorKind {
	switch kind {
	case projectfs.KindNotFound, projectfs.KindNotDirectory, projectfs.KindUnreadable,
		projectfs.KindForbidden, projectfs.KindTooBroad, projectfs.KindChanged,
		projectfs.KindOverlaps, projectfs.KindIO:
		return kind
	}
	return projectfs.KindIO
}
