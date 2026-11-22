package workcopy

import (
	"errors"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// ErrorKind says why an operation of this package failed. The values are
// schema/workcopy.schema.json's workcopyErrorKind.
type ErrorKind string

const (
	// KindInvalid is a request that is not well formed, or a path the
	// staging rules refuse.
	KindInvalid ErrorKind = "invalid"
	// KindUnavailable is an agent with no config directory, so there is no
	// store to stage into, and nothing is applied.
	KindUnavailable ErrorKind = "unavailable"
	// KindNotAuthorized is a root not authorized, or revoked.
	KindNotAuthorized ErrorKind = "notAuthorized"
	// KindRootUnavailable is a stored root that is unavailable; Reason says
	// why.
	KindRootUnavailable ErrorKind = "rootUnavailable"
	// KindNotFound is no listed pipeline there, or no such snapshot.
	KindNotFound ErrorKind = "notFound"
	// KindLink is a symbolic link on the way or at a staged path.
	KindLink ErrorKind = "link"
	// KindChanged is a working copy whose revision is not the one the review
	// was drawn from (something was staged or discarded since), or a
	// directory on the way swapped while it was pinned or checked.
	KindChanged ErrorKind = "changed"
	// KindUnreadable is a file or directory the agent may not read.
	KindUnreadable ErrorKind = "unreadable"
	// KindGenerated is a path under generated/, never staged.
	KindGenerated ErrorKind = "generated"
	// KindReserved is a deletion of pipeline.yaml.
	KindReserved ErrorKind = "reserved"
	// KindNotRegularFile is a path naming something that is not a file.
	KindNotRegularFile ErrorKind = "notRegularFile"
	// KindTooLarge is bytes over 1 MiB.
	KindTooLarge ErrorKind = "tooLarge"
	// KindNotText is bytes that are not UTF-8 or hold a NUL.
	KindNotText ErrorKind = "notText"
	// KindTooMany is more than MaxFiles staged files.
	KindTooMany ErrorKind = "tooMany"
	// KindDrift is an apply or restore that found project files no longer
	// as expected; Drift lists every one, and nothing was written.
	KindDrift ErrorKind = "drift"
	// KindSnapshot is a snapshot that could not be written, so nothing in
	// the project was.
	KindSnapshot ErrorKind = "snapshot"
	// KindIO is anything else.
	KindIO ErrorKind = "io"
)

// Drift is one project file that is not what an apply or restore expects.
type Drift struct {
	Path string
	Now  State
}

// Error is the one failure this package reports: kinds, and the paths of a
// drift, never an underlying error's text.
type Error struct {
	Kind ErrorKind
	// Reason is why the root is unavailable, with KindRootUnavailable.
	Reason projectfs.ErrorKind
	// Drift is set with KindDrift, sorted by path.
	Drift []Drift
}

func (e *Error) Error() string {
	out := "working copy: " + string(e.Kind)
	if e.Reason != "" {
		out += " (" + string(e.Reason) + ")"
	}
	return out
}

// KindOf is the kind of a *Error, "" for anything else.
func KindOf(err error) ErrorKind {
	var fault *Error
	if errors.As(err, &fault) && fault != nil {
		return fault.Kind
	}
	return ""
}

// fromCatalog is err as this package's *Error: its own passed through, a
// catalog kind kept by name, anything else KindIO.
func fromCatalog(err error) *Error {
	var own *Error
	if errors.As(err, &own) {
		return own
	}
	var fault *catalog.Error
	if !errors.As(err, &fault) {
		return &Error{Kind: KindIO}
	}
	switch kind := ErrorKind(fault.Kind); kind {
	case KindInvalid, KindNotAuthorized, KindNotFound, KindLink, KindChanged, KindUnreadable,
		KindGenerated, KindReserved, KindNotRegularFile, KindTooLarge, KindNotText:
		return &Error{Kind: kind}
	case KindRootUnavailable:
		return &Error{Kind: kind, Reason: fault.Reason}
	}
	return &Error{Kind: KindIO}
}
