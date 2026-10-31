// Package catalog is where pipelines live on disk (#48): the list of every
// pipeline under the authorized project roots, opening one, creating one, and
// reading and writing a pipeline.yaml through the path guard.
//
// A pipeline is a directory holding a file named pipeline.yaml, and that
// directory is either an authorized root itself or a directory directly inside
// one (<root>/<dir>/pipeline.yaml). Nothing deeper is looked at. A root that
// holds pipeline.yaml at its top is that one pipeline, and its subdirectories
// are not scanned. A pipeline is addressed by its root and its directory name,
// never by its id, because the same id can be in two directories.
//
// Every filesystem operation goes through internal/projectfs: each directory
// is pinned (projectfs.Path.Pin) before a file in it is read or written by its
// single name, so a directory swapped for a link while it is in use is never
// read or written through. Parsing, validating and rendering a pipeline is
// internal/pipeline's; this package makes no decision about what a valid
// pipeline is.
//
// Nothing is cached and nothing watches the filesystem: every List, Open and
// Create reads the disk again.
package catalog

import (
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// FileName is the name of the file that makes a directory a pipeline.
const FileName = "pipeline.yaml"

// MaxFileSize is the largest pipeline.yaml that is read. A larger one is
// UnreadableTooLarge and its content is never read.
const MaxFileSize = 1 << 20

// Location is where a pipeline is: the real path of its authorized root, as
// the roots store holds it, and the name of its directory inside that root.
// Directory is "" when the root itself is the pipeline.
type Location struct {
	Root      string
	Directory string
}

// UnreadableKind says why a pipeline.yaml did not load.
type UnreadableKind string

const (
	// UnreadableParse is a file that is not one well-formed YAML document the
	// JSON data model can hold.
	UnreadableParse UnreadableKind = "parse"
	// UnreadableSchema is a document that is not a valid pipeline.
	UnreadableSchema UnreadableKind = "schema"
	// UnreadableTooLarge is a file over MaxFileSize, which is not read.
	UnreadableTooLarge UnreadableKind = "tooLarge"
	// UnreadableLink is a pipeline.yaml that is a symbolic link, which is
	// never read, whatever it points to.
	UnreadableLink UnreadableKind = "link"
	// UnreadableNotRegularFile is a directory, FIFO, socket or device named
	// pipeline.yaml.
	UnreadableNotRegularFile UnreadableKind = "notRegularFile"
	// UnreadableUnreadable is a pipeline directory or file the agent may not
	// read or search.
	UnreadableUnreadable UnreadableKind = "unreadable"
)

// Problem is one problem in a pipeline document, as internal/pipeline's
// FileError reports it: a JSON Pointer into the document, the 1-based line
// and column in the file when they are known (0 when not), and a message that
// describes the document, never the disk.
type Problem struct {
	Pointer string
	Line    int
	Column  int
	Message string
}

// Unreadable is why a pipeline did not load, and every problem in it.
// Problems is empty, never nil, for the kinds that are about the file rather
// than its content.
type Unreadable struct {
	Kind     UnreadableKind
	Problems []Problem
}

// Entry is one pipeline directory in the list.
type Entry struct {
	Location
	// Dir is the real, absolute path of the pipeline directory: the root for
	// a root that is the pipeline, root/Directory otherwise. It is what
	// internal/codegen and internal/secrets are handed, resolved through the
	// guard by the caller.
	Dir string
	// Modified is when pipeline.yaml was last modified, in UTC; the zero time
	// when it could not be described.
	Modified time.Time
	// ID, Name and Language are set for a readable entry only (Name only when
	// the file gives one): a file that did not load is not trusted for them.
	ID, Name, Language string
	// Pipeline and Steps are the parsed model, for a readable entry only.
	Pipeline *pipeline.Pipeline
	Steps    []pipeline.Step
	// Unreadable is set exactly when the entry did not load.
	Unreadable *Unreadable
	// Duplicates are the other listed pipelines whose id is this one's,
	// compared ignoring letter case, in list order. Empty when there are none.
	Duplicates []Location
}

// Readable reports whether the entry's pipeline.yaml loaded.
func (e Entry) Readable() bool { return e.Unreadable == nil }

// Root is one stored project root and the pipelines in it.
type Root struct {
	// Path is the real path of the root, as the roots store holds it.
	Path string
	// Available is true when the root was authorized and could be scanned
	// for the whole of this list. An unavailable root has a Reason and no
	// Pipelines, never the ones it had before.
	Available bool
	// Reason is why the root is unavailable, "" when it is available. It is a
	// projectfs.RootStatus reason, or the kind the scan met when the root
	// went away or became unreadable while it was being scanned.
	Reason projectfs.ErrorKind
	// Truncated is true when the root holds more than projectfs.MaxListing
	// entries, so not every directory in it was looked at. A caller that has
	// to know every pipeline (#24) treats such a root as unchecked.
	Truncated bool
	// SkippedLinks is how many symbolic links directly inside the root were
	// not followed.
	SkippedLinks int
	// Pipelines are sorted by directory name, byte order. Never nil.
	Pipelines []Entry
}

// List is every stored root, in the roots store's order.
type List struct {
	Roots []Root
}

// Document is one opened pipeline.
type Document struct {
	Location
	// Dir is the real path of the pipeline directory, as Entry.Dir.
	Dir      string
	Modified time.Time
	Pipeline *pipeline.Pipeline
	Steps    []pipeline.Step
	// JSON is the pipeline as a JSON document, converted from the file's
	// bytes, so it holds what the file holds and no default it left out.
	JSON []byte
	// SHA256 is the lowercase hex SHA-256 of the exact bytes read.
	SHA256 string
}

// hooks lets this package's tests act at a point inside an operation, which
// is the only way to swap a directory between two system calls. The agent
// sets none.
type hooks struct {
	// scanning is called with a root's path once it is pinned for a list,
	// before anything in it is read.
	scanning func(root string)
	// beforePin is called with a directory's name right before the list pins
	// it.
	beforePin func(name string)
	// afterMkdir is called with the claimed directory's name right after
	// Create's Mkdir, before the directory is pinned.
	afterMkdir func(name string)
	// beforeWrite is called right before Create writes pipeline.yaml in the
	// pinned directory.
	beforeWrite func(name string)
}

// Catalog lists, opens and creates pipelines under the roots a
// projectfs.Roots holds, through the Guard those roots are authorized into.
// It is safe for concurrent use. Creates are serialized, so two creates of the
// same name in one agent never claim the same id.
type Catalog struct {
	roots  *projectfs.Roots
	guard  *projectfs.Guard
	logger *slog.Logger
	create sync.Mutex
	hook   hooks
}

// New returns a catalog over roots, reading through guard, which must be the
// Guard the roots are authorized into (the one the Browser behind the roots
// was built over). A nil logger discards everything.
func New(roots *projectfs.Roots, guard *projectfs.Guard, logger *slog.Logger) (*Catalog, error) {
	if roots == nil || guard == nil {
		return nil, &Error{Kind: KindInvalid}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return &Catalog{roots: roots, guard: guard, logger: logger}, nil
}

// validDirectory reports whether name is a single directory name a request
// may carry: not empty, ".", "..", and holding no "/", "\" or NUL.
func validDirectory(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00")
}
