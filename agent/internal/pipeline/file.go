package pipeline

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// PathGuard authorizes a filesystem path before a pipeline file is read or
// written, and returns the path to actually use.
//
// Nothing in this package implements it: path authorization and traversal
// checks are #6. This is the seam it plugs into. Every read and write in this
// package goes through Store.resolve, so when #6 lands it is a guard handed to
// a Store, and no caller of Load or Save changes shape.
type PathGuard interface {
	ResolvePipelinePath(path string) (string, error)
}

// Store reads and writes pipeline files. A Store with no guard accepts every
// path, which is what the agent does until #6 lands; the zero value is usable.
//
// Which directory a pipeline lives in, and how pipelines are listed, is #48:
// a Store is given a path, it does not invent one.
type Store struct {
	Guard PathGuard
}

// pipelineFileMode is the mode a new pipeline file gets. An existing file
// keeps the mode it already has.
const pipelineFileMode fs.FileMode = 0o644

// LoadFile reads the pipeline file at path, with no path guard.
func LoadFile(path string) (*Pipeline, []Step, error) {
	return Store{}.Load(path)
}

// SaveFile writes p to the pipeline file at path, with no path guard.
func SaveFile(path string, p *Pipeline) error {
	return Store{}.Save(path, p)
}

// Load reads the pipeline file at path, validates it against the pipeline
// schema and decodes it into the generated model with its typed processing
// steps. A file that fails anywhere yields a *FileError naming the file and
// the offending field; no half-built pipeline is ever returned.
func (s Store) Load(path string) (*Pipeline, []Step, error) {
	resolved, err := s.resolve("load", path)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, nil, &FileError{
			Op: "load", Path: resolved, Kind: KindIO,
			Message: "the pipeline file could not be read", Err: err,
		}
	}
	parsed, steps, err := ParseYAML(data)
	if err != nil {
		return nil, nil, at("load", resolved, err)
	}
	return parsed, steps, nil
}

// Save writes p to the pipeline file at path, atomically: either the file is
// the new pipeline or it is still the previous one, never a half-written mix
// of the two.
//
// The pipeline is validated before anything is written, so an invalid pipeline
// does not reach the disk at all.
func (s Store) Save(path string, p *Pipeline) error {
	resolved, err := s.resolve("save", path)
	if err != nil {
		return err
	}
	data, err := MarshalYAML(p)
	if err != nil {
		return at("save", resolved, err)
	}

	mode := pipelineFileMode
	if info, statErr := os.Stat(resolved); statErr == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}
	if err := writeFileAtomically(resolved, mode, func(w io.Writer) error {
		_, writeErr := w.Write(data)
		return writeErr
	}); err != nil {
		return at("save", resolved, err)
	}
	return nil
}

// resolve is the one place a path enters the filesystem, and the one place
// #6's guard has to be consulted.
func (s Store) resolve(op, path string) (string, error) {
	if path == "" {
		return "", &FileError{Op: op, Kind: KindIO, Message: "no pipeline file was named"}
	}
	if s.Guard == nil {
		return filepath.Clean(path), nil
	}
	resolved, err := s.Guard.ResolvePipelinePath(path)
	if err != nil {
		var fault *FileError
		if errors.As(err, &fault) {
			fault.Op = op
			if fault.Path == "" {
				fault.Path = path
			}
			return "", fault
		}
		return "", &FileError{
			Op: op, Path: path, Kind: KindForbidden,
			Message: "the path guard refused this path", Err: err,
		}
	}
	return resolved, nil
}

// writeFileAtomically replaces path with whatever emit writes, or leaves the
// file that is already there exactly as it was.
//
// The new content goes to a temporary file in the same directory, so the
// rename that commits it stays on one filesystem and is therefore atomic. The
// content is flushed before the rename and the directory is flushed after it,
// so a crash leaves either the old file or the new one. Any failure before the
// rename removes the temporary file and the destination is never touched, so a
// truncated write cannot become the pipeline file.
//
// emit is a function rather than a byte slice so a test can fail halfway
// through a real write, and so the failure it proves is the one the production
// path takes.
func writeFileAtomically(path string, mode fs.FileMode, emit func(io.Writer) error) error {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return &FileError{
			Path: path, Kind: KindIO,
			Message: "a temporary file could not be created next to the pipeline file", Err: err,
		}
	}
	tempName := temp.Name()

	committed := false
	defer func() {
		if committed {
			return
		}
		// Nothing was renamed, so the destination still holds the previous
		// good file; drop the temporary file and leave it that way.
		_ = temp.Close()
		_ = os.Remove(tempName)
	}()

	fault := func(message string, err error) error {
		return &FileError{Path: path, Kind: KindIO, Message: message, Err: err}
	}
	if err := emit(temp); err != nil {
		return fault("the pipeline could not be written to the temporary file", err)
	}
	if err := temp.Sync(); err != nil {
		return fault("the temporary file could not be flushed to disk", err)
	}
	if err := temp.Chmod(mode); err != nil {
		return fault("the temporary file could not be given the pipeline file's mode", err)
	}
	if err := temp.Close(); err != nil {
		return fault("the temporary file could not be closed", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fault("the temporary file could not be renamed over the pipeline file", err)
	}
	committed = true

	// The rename is already visible; flushing the directory only decides
	// whether it survives a power cut, so a failure here is not a failed save.
	if dir, err := os.Open(directory); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
