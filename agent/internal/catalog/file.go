package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"sort"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

// fileMode is the mode a new pipeline.yaml gets; an existing one keeps its own
// (Path.WriteAtomically).
const fileMode fs.FileMode = 0o644

// errNoPipeline is a directory with no pipeline.yaml in it.
var errNoPipeline = &Error{Kind: KindNotFound}

// loaded is a pipeline.yaml that loaded.
type loaded struct {
	data     []byte
	model    *pipeline.Pipeline
	steps    []pipeline.Step
	json     []byte
	modified time.Time
}

// readIn reads pipeline.yaml in dir, which the caller has pinned, by its
// single name. It answers exactly one of: the loaded file; why it did not
// load, with the time it was modified when the file could be described; or an
// error, which is errNoPipeline for a directory without the file, and a
// *projectfs.PathError of KindForbidden when the pin or its root stopped
// working.
//
// The file is described before it is opened and read only when it is a
// regular file of at most MaxFileSize. It is opened through the pinned
// directory, which keeps the open inside that directory; the open file is
// described again, and the name once more after the read, so a link or
// another kind of file swapped in at that moment is reported rather than
// trusted. A swap there and back between the Lstat and the open, to a link to
// another file in the same pipeline directory, is the window left; reading
// that file is all it could do.
func readIn(dir projectfs.Path) (*loaded, *Unreadable, time.Time, error) {
	none := time.Time{}
	file, err := dir.Join(FileName)
	if err != nil {
		return nil, nil, none, err
	}
	info, err := file.Lstat()
	if err != nil {
		switch projectfs.KindOf(err) {
		case projectfs.KindNotFound, projectfs.KindNotDirectory:
			return nil, nil, none, errNoPipeline
		case projectfs.KindForbidden:
			return nil, nil, none, err
		}
		return nil, unreadable(UnreadableUnreadable), none, nil
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return nil, unreadable(UnreadableLink), none, nil
	}
	if !info.Mode().IsRegular() {
		return nil, unreadable(UnreadableNotRegularFile), none, nil
	}
	modified := info.ModTime().UTC()
	if info.Size() > MaxFileSize {
		return nil, unreadable(UnreadableTooLarge), modified, nil
	}

	opened, err := file.Open()
	if err != nil {
		if projectfs.KindOf(err) == projectfs.KindForbidden {
			// The pinned directory refuses a link that leaves it.
			if again, lstatErr := file.Lstat(); lstatErr == nil && again.Mode()&fs.ModeSymlink != 0 {
				return nil, unreadable(UnreadableLink), none, nil
			}
			return nil, nil, none, err
		}
		if projectfs.KindOf(err) == projectfs.KindNotFound {
			return nil, nil, none, errNoPipeline
		}
		return nil, unreadable(UnreadableUnreadable), modified, nil
	}
	defer func() { _ = opened.Close() }()
	described, err := opened.Stat()
	if err != nil {
		return nil, unreadable(UnreadableUnreadable), modified, nil
	}
	if !described.Mode().IsRegular() {
		return nil, unreadable(UnreadableNotRegularFile), none, nil
	}
	if described.Size() > MaxFileSize {
		return nil, unreadable(UnreadableTooLarge), described.ModTime().UTC(), nil
	}
	data, err := io.ReadAll(io.LimitReader(opened, MaxFileSize+1))
	if err != nil {
		return nil, unreadable(UnreadableUnreadable), modified, nil
	}
	if len(data) > MaxFileSize {
		return nil, unreadable(UnreadableTooLarge), modified, nil
	}
	if after, err := file.Lstat(); err == nil && after.Mode()&fs.ModeSymlink != 0 {
		return nil, unreadable(UnreadableLink), none, nil
	}
	modified = described.ModTime().UTC()

	model, steps, err := pipeline.ParseYAML(data)
	if err != nil {
		return nil, problemsOf(data, err), modified, nil
	}
	encoded, err := pipeline.YAMLToJSON(data)
	if err != nil {
		return nil, problemsOf(data, err), modified, nil
	}
	return &loaded{data: data, model: model, steps: steps, json: encoded, modified: modified}, nil, modified, nil
}

func unreadable(kind UnreadableKind) *Unreadable {
	return &Unreadable{Kind: kind, Problems: []Problem{}}
}

// problemsOf turns a parse or schema failure into an Unreadable, with a line
// and column for every problem whose pointer can be found in the file.
func problemsOf(data []byte, err error) *Unreadable {
	var fault *pipeline.FileError
	if !errors.As(err, &fault) {
		return &Unreadable{Kind: UnreadableParse, Problems: []Problem{{Message: "the document could not be read as a pipeline"}}}
	}
	if fault.Kind == pipeline.KindSchema && len(fault.Problems) > 0 {
		return &Unreadable{Kind: UnreadableSchema, Problems: schemaProblems(data, fault.Problems)}
	}
	message := fault.Message
	if fault.Kind == pipeline.KindSyntax && fault.Err != nil {
		// The parser's own words, which say what is wrong with the document.
		message += ": " + fault.Err.Error()
	}
	problem := Problem{Pointer: fault.Location, Line: fault.Line, Column: fault.Column, Message: message}
	if problem.Line == 0 && problem.Pointer != "" {
		problem.Line, problem.Column, _ = pipeline.PositionInYAML(data, problem.Pointer)
	}
	kind := UnreadableParse
	if fault.Kind == pipeline.KindSchema || fault.Kind == pipeline.KindModel {
		kind = UnreadableSchema
	}
	return &Unreadable{Kind: kind, Problems: []Problem{problem}}
}

// schemaProblems is one Problem per schema violation; data, when there is a
// file, gives each a line.
func schemaProblems(data []byte, violations pipeline.ValidationErrors) []Problem {
	out := make([]Problem, 0, len(violations))
	for _, violation := range violations {
		problem := Problem{Pointer: violation.InstanceLocation, Message: violation.Message}
		if data != nil {
			problem.Line, problem.Column, _ = pipeline.PositionInYAML(data, problem.Pointer)
		}
		out = append(out, problem)
	}
	sortProblems(out)
	return out
}

// sortProblems puts problems in one total order: by line, then column, then
// pointer, then message, so a problem with no known position (line 0) comes
// first. The validator reports a document's violations in an order that
// depends on how it walked a oneOf, which differs from one validation to the
// next, and two lists of an unchanged disk have to be identical.
func sortProblems(problems []Problem) {
	sort.SliceStable(problems, func(i, j int) bool {
		a, b := problems[i], problems[j]
		switch {
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Column != b.Column:
			return a.Column < b.Column
		case a.Pointer != b.Pointer:
			return a.Pointer < b.Pointer
		}
		return a.Message < b.Message
	})
}

// digest is the lowercase hex SHA-256 of data.
func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Load reads the pipeline.yaml in the pipeline directory dir: dir is pinned
// first, and the file is read by its single name through the pin, as the list
// reads it. It answers a *Error: KindNotFound when there is no pipeline.yaml
// or no directory, KindUnreadable with why when it did not load, and
// KindNotAuthorized when dir's root is no longer authorized.
func Load(dir projectfs.Path) (*pipeline.Pipeline, []pipeline.Step, error) {
	pin, err := dir.Pin()
	if err != nil {
		return nil, nil, pinFailure(err)
	}
	defer func() { _ = pin.Close() }()
	file, problem, _, err := readIn(pin.Path())
	switch {
	case err != nil:
		return nil, nil, pinFailure(err)
	case problem != nil:
		return nil, nil, &Error{Kind: KindUnreadable, Unreadable: problem}
	}
	return file.model, file.steps, nil
}

// Save writes p as the pipeline.yaml in the pipeline directory dir,
// atomically: p is validated and rendered by internal/pipeline before
// anything is touched, dir is pinned, and the file is replaced by its single
// name through the pin with Path.WriteAtomically, so it is either the new
// pipeline or still the previous one. A new file is 0644; an existing one
// keeps its mode. An invalid p is KindInvalid with its problems, and nothing
// is written.
//
// Saving an edited pipeline over one a user opened, with a check against the
// SHA-256 it was opened with, is #78.
func Save(dir projectfs.Path, p *pipeline.Pipeline) error {
	data, err := render(p)
	if err != nil {
		return err
	}
	pin, err := dir.Pin()
	if err != nil {
		return pinFailure(err)
	}
	defer func() { _ = pin.Close() }()
	return writeIn(pin.Path(), data)
}

// render is MarshalYAML with its schema failure turned into KindInvalid.
func render(p *pipeline.Pipeline) ([]byte, error) {
	data, err := pipeline.MarshalYAML(p)
	if err != nil {
		var fault *pipeline.FileError
		if errors.As(err, &fault) && len(fault.Problems) > 0 {
			return nil, &Error{Kind: KindInvalid, Problems: schemaProblems(nil, fault.Problems)}
		}
		return nil, &Error{Kind: KindInvalid}
	}
	return data, nil
}

// writeIn writes data as pipeline.yaml in the pinned directory dir.
func writeIn(dir projectfs.Path, data []byte) error {
	file, err := dir.Join(FileName)
	if err != nil {
		return &Error{Kind: KindIO}
	}
	if err := file.WriteAtomically(fileMode, func(w io.Writer) error {
		_, writeErr := w.Write(data)
		return writeErr
	}); err != nil {
		if projectfs.KindOf(err) == projectfs.KindForbidden {
			return &Error{Kind: KindNotAuthorized}
		}
		return &Error{Kind: KindIO}
	}
	return nil
}

// pinFailure is the *Error for a directory that could not be pinned or a pin
// that stopped working.
func pinFailure(err error) error {
	if fault, ok := err.(*Error); ok {
		return fault
	}
	switch projectfs.KindOf(err) {
	case projectfs.KindForbidden:
		return &Error{Kind: KindNotAuthorized}
	case projectfs.KindNotFound, projectfs.KindNotDirectory, projectfs.KindChanged:
		return &Error{Kind: KindNotFound}
	case projectfs.KindUnreadable:
		return &Error{Kind: KindUnreadable, Unreadable: unreadable(UnreadableUnreadable)}
	}
	return &Error{Kind: KindIO}
}
