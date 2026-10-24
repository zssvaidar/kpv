package pipeline

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind says what went wrong with a pipeline file. It exists so a caller
// branches on a value rather than on the wording of a message.
type ErrorKind string

const (
	// KindForbidden is a path the guard refused. Nothing in this package
	// produces it yet; the guard that does is #6.
	KindForbidden ErrorKind = "forbidden"
	// KindIO is the file itself: it could not be read, written or replaced.
	KindIO ErrorKind = "io"
	// KindSyntax is a file that is not well-formed YAML.
	KindSyntax ErrorKind = "syntax"
	// KindEmptyDocument is a file with no YAML document in it: empty, or
	// nothing but comments and whitespace. It is deliberately not the same as
	// a document whose content is null, which is a schema failure.
	KindEmptyDocument ErrorKind = "emptyDocument"
	// KindMultipleDocuments is a file holding more than one YAML document.
	KindMultipleDocuments ErrorKind = "multipleDocuments"
	// KindDuplicateKey is a mapping that defines the same key twice. It is an
	// error rather than a last-one-wins resolution, because which of the two
	// values the author meant is not something to guess.
	KindDuplicateKey ErrorKind = "duplicateKey"
	// KindUnsupportedValue is a value YAML can express and JSON cannot, such
	// as a non-string mapping key or a tag outside the JSON data model.
	KindUnsupportedValue ErrorKind = "unsupportedValue"
	// KindSchema is a document that is valid YAML but not a valid pipeline.
	// Problems holds every violation with its locations.
	KindSchema ErrorKind = "schema"
	// KindModel is a document the schema accepted that still could not be
	// decoded into the generated model, which means the two disagree.
	KindModel ErrorKind = "model"
)

// FileError is the one structured failure this package reports for reading or
// writing a pipeline file. Every field is optional except Kind and Message, so
// a caller can report as much as the failure actually knows: which file, where
// in the document, which line and column, and which schema rules were broken.
//
// It is a struct and not a formatted string so that nothing has to parse a
// message back apart, and no failure path has to panic to signal a problem.
type FileError struct {
	// Op is what was being done: "load" or "save".
	Op string
	// Path is the pipeline file, empty when the failure is about bytes that
	// did not come from a file.
	Path string
	// Kind says what went wrong.
	Kind ErrorKind
	// Location is a JSON Pointer into the document, naming the offending
	// field; "" is the document as a whole.
	Location string
	// Line and Column are 1-based positions in the file, 0 when the failure
	// has no position.
	Line, Column int
	// Message is a human-readable explanation, not a stable identifier.
	Message string
	// Problems is every schema violation, each with its instance and keyword
	// location. Set when Kind is KindSchema.
	Problems ValidationErrors
	// Err is the underlying cause, when there is one.
	Err error
}

func (e *FileError) Error() string {
	var out strings.Builder
	if e.Op != "" {
		out.WriteString(e.Op)
		out.WriteString(" ")
	}
	if e.Path != "" {
		out.WriteString(e.Path)
	} else {
		out.WriteString("pipeline")
	}
	out.WriteString(": ")
	out.WriteString(e.Message)
	if e.Location != "" {
		fmt.Fprintf(&out, " at %s", e.Location)
	}
	if e.Line > 0 {
		fmt.Fprintf(&out, " (line %d, column %d)", e.Line, e.Column)
	}
	if len(e.Problems) > 0 {
		out.WriteString(": ")
		out.WriteString(e.Problems.Error())
	} else if e.Err != nil {
		out.WriteString(": ")
		out.WriteString(e.Err.Error())
	}
	return out.String()
}

// Unwrap exposes the underlying cause, so errors.Is and errors.As reach it.
// For a schema failure the cause is the ValidationErrors themselves.
func (e *FileError) Unwrap() error {
	return e.Err
}

// at fills in the file-level context a failure found while decoding bytes could
// not know, and wraps anything that is not already a FileError.
func at(op, path string, err error) error {
	if err == nil {
		return nil
	}
	var fault *FileError
	if errors.As(err, &fault) {
		fault.Op = op
		if fault.Path == "" {
			fault.Path = path
		}
		return fault
	}
	return &FileError{Op: op, Path: path, Kind: KindIO, Message: "the pipeline file could not be processed", Err: err}
}
