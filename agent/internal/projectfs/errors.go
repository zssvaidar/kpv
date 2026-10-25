package projectfs

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// ErrorKind says why a path was refused. It exists so a caller branches on a
// value rather than on the wording of a message, and it is the same shape
// internal/pipeline uses for a pipeline file, down to the name of the kind a
// guard refusal carries.
type ErrorKind string

const (
	// KindForbidden is the refusal this package exists for: the path is not
	// inside any authorized root, or it tried to leave one. It is deliberately
	// not the same as KindNotFound, and a KindForbidden error never answers
	// yes to errors.Is(err, fs.ErrNotExist), so a caller cannot mistake a
	// refusal for a file that is simply not there.
	//
	// A path outside every root is refused with this kind whether or not it
	// exists, so the refusal says nothing about what is out there.
	KindForbidden ErrorKind = "forbidden"
	// KindNotFound is a path inside an authorized root that does not exist.
	KindNotFound ErrorKind = "notFound"
	// KindUnreadable is a path inside an authorized root, or on the way to
	// one, that this process is not permitted to read. #7 reports an entry it
	// cannot read rather than dropping it, which is why this is not KindIO.
	KindUnreadable ErrorKind = "unreadable"
	// KindNotDirectory is a path whose ancestor is not a directory, or a root
	// being authorized that is not a directory.
	KindNotDirectory ErrorKind = "notDirectory"
	// KindInvalid is a path this package will not reason about at all: empty,
	// carrying a NUL byte, climbing back through a component that does not
	// exist, or with more symbolic links on it than will be followed.
	KindInvalid ErrorKind = "invalid"
	// KindIO is any other failure of the filesystem itself.
	KindIO ErrorKind = "io"
)

// PathError is the one structured failure this package reports. Every field
// except Kind and Message is optional, so a refusal reports as much as it
// actually knows: the path as the caller wrote it, the real path link
// resolution arrived at, which authorized root it was judged against, and
// which segment it came apart on.
//
// It is a struct and not a formatted string so that nothing has to parse a
// message back apart, following the *pipeline.FileError house style.
type PathError struct {
	// Op is what was being attempted: "resolve", "read", "write", "open",
	// "authorize" and so on.
	Op string
	// Path is the path as the caller gave it, before any resolution.
	Path string
	// Resolved is the real path after link resolution, when resolution got
	// far enough to have one. It is empty when it did not.
	Resolved string
	// Root is the authorized root the path was judged against, when one
	// applies. It is empty for a path that belongs to no root.
	Root string
	// Kind says what went wrong.
	Kind ErrorKind
	// Location is the part of the path the failure is about: the segment
	// resolution stopped on, or the path relative to its root. "" is the path
	// as a whole.
	Location string
	// Message is a human-readable explanation, not a stable identifier.
	Message string
	// Err is the underlying cause, when there is one.
	Err error
}

func (e *PathError) Error() string {
	var out strings.Builder
	if e.Op != "" {
		out.WriteString(e.Op)
		out.WriteString(" ")
	}
	if e.Path != "" {
		out.WriteString(e.Path)
	} else {
		out.WriteString("path")
	}
	out.WriteString(": ")
	out.WriteString(e.Message)
	if e.Location != "" {
		fmt.Fprintf(&out, " at %s", e.Location)
	}
	if e.Resolved != "" && e.Resolved != e.Path {
		fmt.Fprintf(&out, " (resolved to %s)", e.Resolved)
	}
	if e.Root != "" {
		fmt.Fprintf(&out, " (root %s)", e.Root)
	}
	if e.Err != nil {
		out.WriteString(": ")
		out.WriteString(e.Err.Error())
	}
	return out.String()
}

// Unwrap exposes the underlying cause, so errors.Is and errors.As reach it.
func (e *PathError) Unwrap() error { return e.Err }

// Is maps a kind onto the io/fs sentinels, so an ordinary caller can write
// errors.Is(err, fs.ErrNotExist) and get the answer the kind gives rather than
// whatever errno happens to be underneath.
//
// A refusal is never fs.ErrNotExist. That is the criterion this package has to
// hold: a caller must be able to tell "you may not look there" apart from
// "there is nothing there".
func (e *PathError) Is(target error) bool {
	switch target {
	case fs.ErrNotExist:
		return e.Kind == KindNotFound
	case fs.ErrPermission:
		return e.Kind == KindForbidden || e.Kind == KindUnreadable
	case fs.ErrInvalid:
		return e.Kind == KindInvalid
	}
	return false
}

// KindOf reports the kind of a refusal anywhere in err's chain, or "" when err
// is not one of this package's errors.
func KindOf(err error) ErrorKind {
	var fault *PathError
	if errors.As(err, &fault) {
		return fault.Kind
	}
	return ""
}

// IsForbidden reports whether err is this package refusing a path: outside
// every authorized root, or trying to leave one.
func IsForbidden(err error) bool { return KindOf(err) == KindForbidden }

// IsNotFound reports whether err is a path inside an authorized root that does
// not exist, which is not the same thing as a refusal.
func IsNotFound(err error) bool { return KindOf(err) == KindNotFound }
