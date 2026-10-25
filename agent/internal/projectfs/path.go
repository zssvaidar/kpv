package projectfs

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Path is a path that has been checked against the authorized roots, and the
// handle every operation in this package goes through.
//
// It is not a permission slip. It records which root the path belongs to and
// where it sits inside that root, and every operation on it is performed
// relative to that root's open directory descriptor, which is what re-checks
// the authorization: the kernel walks each component from the descriptor and
// refuses any component that would leave it. A symbolic link swapped in after
// the Path was made therefore fails the operation rather than redirecting it.
//
// A Path cannot be built from outside this package -- its fields are
// unexported and only Guard.Resolve and Path.Join fill them in -- and the zero
// Path reaches nothing at all.
type Path struct {
	owner    *root
	relative string
	real     string
	given    string
}

// String returns the real path, which is what a log or an error should show.
func (p Path) String() string { return p.real }

// Real returns the real, absolute, link-free path.
func (p Path) Real() string { return p.real }

// Given returns the path as the caller wrote it, before resolution.
func (p Path) Given() string { return p.given }

// Root returns the real path of the authorized root this path belongs to.
func (p Path) Root() string {
	if p.owner == nil {
		return ""
	}
	return p.owner.real
}

// Rel returns the path from its root, "." for the root itself. It is the name
// every operation is performed with, relative to the root's descriptor.
func (p Path) Rel() string { return p.relative }

// IsRoot reports whether this path is an authorized root itself, which is the
// case a path exactly equal to a root produces.
func (p Path) IsRoot() bool { return p.owner != nil && p.relative == "." }

// usable rejects the zero Path. Nothing else can produce a Path with no root,
// so this is the one way a value that was never resolved could reach an
// operation.
func (p Path) usable(op string) error {
	if p.owner == nil {
		return &PathError{
			Op: op, Path: p.given, Kind: KindForbidden,
			Message: "this path was never resolved against an authorized project directory",
		}
	}
	return nil
}

// fail turns a failure from the root's descriptor into a *PathError.
//
// The one classification that matters is the escape: the root refusing to let
// an operation leave it. That is a KindForbidden refusal and it is exactly the
// case a symbolic link swapped in between resolving and opening produces, so
// it has to arrive at the caller as a refusal and not as a missing file.
func (p Path) fail(op string, err error) error {
	fault := &PathError{
		Op: op, Path: p.given, Resolved: p.real, Root: p.Root(),
		Location: p.relative, Err: err,
	}
	switch {
	case p.owner.isEscape(err):
		fault.Kind = KindForbidden
		fault.Message = "this path leaves its authorized project directory"
	case errors.Is(err, fs.ErrClosed):
		fault.Kind = KindForbidden
		fault.Message = "the project directory this path belongs to is no longer authorized"
	case errors.Is(err, fs.ErrNotExist):
		fault.Kind = KindNotFound
		fault.Message = "this path does not exist"
	case errors.Is(err, fs.ErrExist):
		fault.Kind = KindIO
		fault.Message = "this path already exists"
	case errors.Is(err, fs.ErrPermission):
		fault.Kind = KindUnreadable
		fault.Message = "this path cannot be read or written by this process"
	default:
		fault.Kind = KindIO
		fault.Message = "this path could not be used"
	}
	return fault
}

// Join returns the path to a named entry inside this one.
//
// Each name is a single entry name, which is what reading a directory hands
// back: no separator, and not "." or "..". A caller that has a whole path to
// check uses Guard.Resolve instead, which resolves it. Joining does not follow
// links, by design: it is how a directory listing reaches an entry in order to
// Lstat it and find out that it is a symbolic link, which is something
// resolving the path would have decided for it.
func (p Path) Join(names ...string) (Path, error) {
	if err := p.usable("join"); err != nil {
		return Path{}, err
	}
	refuse := func(name, why string) (Path, error) {
		return Path{}, &PathError{
			Op: "join", Path: p.real, Root: p.Root(), Kind: KindInvalid,
			Location: name, Message: why,
		}
	}
	for _, name := range names {
		switch {
		case name == "":
			return refuse(name, "an entry name cannot be empty")
		case name == "." || name == "..":
			return refuse(name, "an entry name cannot be \".\" or \"..\"")
		case strings.ContainsRune(name, 0):
			return refuse(name, "an entry name cannot contain a NUL byte")
		case strings.ContainsRune(name, filepath.Separator):
			return refuse(name, "an entry name cannot contain a path separator")
		}
	}
	joined := p
	joined.relative = filepath.Join(append([]string{p.relative}, names...)...)
	joined.real = filepath.Join(append([]string{p.real}, names...)...)
	joined.given = joined.real
	return joined, nil
}

// Open opens the file for reading.
func (p Path) Open() (*os.File, error) { return p.OpenFile(os.O_RDONLY, 0) }

// OpenFile opens the file with the given flags and mode, the way os.OpenFile
// does, confined to the path's root.
func (p Path) OpenFile(flag int, perm fs.FileMode) (*os.File, error) {
	if err := p.usable("open"); err != nil {
		return nil, err
	}
	file, err := p.owner.directory.OpenFile(p.relative, flag, perm)
	if err != nil {
		return nil, p.fail("open", err)
	}
	return file, nil
}

// Create creates or truncates the file for writing.
func (p Path) Create() (*os.File, error) {
	return p.OpenFile(os.O_RDWR|os.O_CREATE|os.O_TRUNC, defaultFileMode)
}

// ReadFile reads the whole file.
func (p Path) ReadFile() ([]byte, error) {
	if err := p.usable("read"); err != nil {
		return nil, err
	}
	data, err := p.owner.directory.ReadFile(p.relative)
	if err != nil {
		return nil, p.fail("read", err)
	}
	return data, nil
}

// WriteFile writes data to the file, creating it with perm if it is not there.
// It is not atomic; WriteAtomically is.
func (p Path) WriteFile(data []byte, perm fs.FileMode) error {
	if err := p.usable("write"); err != nil {
		return err
	}
	if err := p.owner.directory.WriteFile(p.relative, data, perm); err != nil {
		return p.fail("write", err)
	}
	return nil
}

// Stat describes the file, following a symbolic link at the end of the path.
func (p Path) Stat() (fs.FileInfo, error) {
	if err := p.usable("stat"); err != nil {
		return nil, err
	}
	info, err := p.owner.directory.Stat(p.relative)
	if err != nil {
		return nil, p.fail("stat", err)
	}
	return info, nil
}

// Lstat describes the file without following a symbolic link at the end of the
// path, so a link is reported as a link rather than as what it points at.
func (p Path) Lstat() (fs.FileInfo, error) {
	if err := p.usable("lstat"); err != nil {
		return nil, err
	}
	info, err := p.owner.directory.Lstat(p.relative)
	if err != nil {
		return nil, p.fail("lstat", err)
	}
	return info, nil
}

// Readlink returns the target a symbolic link holds, without following it. The
// target is returned as it is written, which may well be a path outside the
// root: it is a string to report, not a path to use.
func (p Path) Readlink() (string, error) {
	if err := p.usable("readlink"); err != nil {
		return "", err
	}
	target, err := p.owner.directory.Readlink(p.relative)
	if err != nil {
		return "", p.fail("readlink", err)
	}
	return target, nil
}

// ReadDir reads the directory and returns its entries sorted by name, the way
// os.ReadDir does. A link among them is reported as a link; following it is
// the caller's decision and Join plus Lstat is how that decision is made.
func (p Path) ReadDir() ([]fs.DirEntry, error) {
	if err := p.usable("readdir"); err != nil {
		return nil, err
	}
	directory, err := p.owner.directory.Open(p.relative)
	if err != nil {
		return nil, p.fail("readdir", err)
	}
	defer func() { _ = directory.Close() }()
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return nil, p.fail("readdir", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

// Mkdir creates the directory.
func (p Path) Mkdir(perm fs.FileMode) error {
	if err := p.usable("mkdir"); err != nil {
		return err
	}
	if err := p.owner.directory.Mkdir(p.relative, perm); err != nil {
		return p.fail("mkdir", err)
	}
	return nil
}

// MkdirAll creates the directory and every missing parent of it, all inside
// the root.
func (p Path) MkdirAll(perm fs.FileMode) error {
	if err := p.usable("mkdir"); err != nil {
		return err
	}
	if err := p.owner.directory.MkdirAll(p.relative, perm); err != nil {
		return p.fail("mkdir", err)
	}
	return nil
}

// Remove removes the file or empty directory.
func (p Path) Remove() error {
	if err := p.usable("remove"); err != nil {
		return err
	}
	if err := p.owner.directory.Remove(p.relative); err != nil {
		return p.fail("remove", err)
	}
	return nil
}

// RemoveAll removes the path and anything under it.
func (p Path) RemoveAll() error {
	if err := p.usable("remove"); err != nil {
		return err
	}
	if err := p.owner.directory.RemoveAll(p.relative); err != nil {
		return p.fail("remove", err)
	}
	return nil
}

// RenameTo renames this path to destination. Both paths must belong to the
// same authorized root, because a rename across roots is a copy and a delete
// and not something to do behind a caller's back.
func (p Path) RenameTo(destination Path) error {
	if err := p.usable("rename"); err != nil {
		return err
	}
	if err := destination.usable("rename"); err != nil {
		return err
	}
	if p.owner != destination.owner {
		return &PathError{
			Op: "rename", Path: p.given, Resolved: p.real, Root: p.Root(), Kind: KindForbidden,
			Message:  "a rename cannot cross authorized project directories",
			Location: destination.real,
		}
	}
	if err := p.owner.directory.Rename(p.relative, destination.relative); err != nil {
		return p.fail("rename", err)
	}
	return nil
}

// defaultFileMode is the mode a file this package creates gets when the caller
// does not say.
const defaultFileMode fs.FileMode = 0o644

// WriteAtomically replaces the file with whatever emit writes, or leaves the
// file that is already there exactly as it was.
//
// The new content goes to a temporary file in the same directory and is then
// renamed over the destination, so the replacement is atomic and a failure
// part-way through cannot leave a truncated file where the previous good one
// was. Both the temporary file and the rename go through the root's
// descriptor, so neither of them can land outside the root either.
//
// An existing regular file keeps the mode it already has; a new one gets perm.
// emit is a function rather than a byte slice so that a caller can stream, and
// so a test can fail in the middle of a real write.
//
// One consequence is worth stating because it looks like an exception and is
// not: a rename replaces a name, it does not follow a symbolic link sitting
// under that name. So if someone has put a link to a file outside the root
// where the destination should be, this does not write through it -- it
// replaces it with the new file, inside the root, under the name that was
// authorized. Writing through such a link is what WriteFile would attempt, and
// the root's descriptor refuses that.
func (p Path) WriteAtomically(perm fs.FileMode, emit func(io.Writer) error) error {
	if err := p.usable("write"); err != nil {
		return err
	}
	if p.IsRoot() {
		return &PathError{
			Op: "write", Path: p.given, Resolved: p.real, Root: p.Root(), Kind: KindNotDirectory,
			Message: "an authorized project directory is not a file to write",
		}
	}
	mode := perm
	if info, err := p.owner.directory.Stat(p.relative); err == nil && info.Mode().IsRegular() {
		mode = info.Mode().Perm()
	}

	temporary, name, err := p.createTemporary(mode)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		// Nothing was renamed, so the destination still holds the previous
		// good file; drop the temporary file and leave it that way.
		_ = temporary.Close()
		_ = p.owner.directory.Remove(name)
	}()

	fault := func(message string, err error) error {
		return &PathError{
			Op: "write", Path: p.given, Resolved: p.real, Root: p.Root(),
			Location: name, Kind: KindIO, Message: message, Err: err,
		}
	}
	if err := emit(temporary); err != nil {
		return fault("the content could not be written to the temporary file", err)
	}
	if err := temporary.Sync(); err != nil {
		return fault("the temporary file could not be flushed to disk", err)
	}
	if err := temporary.Chmod(mode); err != nil {
		return fault("the temporary file could not be given the destination's mode", err)
	}
	if err := temporary.Close(); err != nil {
		return fault("the temporary file could not be closed", err)
	}
	if err := p.owner.directory.Rename(name, p.relative); err != nil {
		return p.fail("write", err)
	}
	committed = true
	return nil
}

// createTemporary makes a file nobody else has, in the destination's own
// directory and inside the root, and returns it with the name it was created
// under relative to the root.
//
// The name is random and the file is created exclusively, so an attacker who
// can write into the directory cannot pre-create it, point it at something
// else, or guess it. os.CreateTemp would do this job and would do it outside
// the root, which is the whole thing this package will not do.
func (p Path) createTemporary(perm fs.FileMode) (*os.File, string, error) {
	directory := filepath.Dir(p.relative)
	prefix := "." + filepath.Base(p.relative) + ".tmp-"
	for attempt := 0; attempt < 10; attempt++ {
		suffix := make([]byte, 8)
		if _, err := rand.Read(suffix); err != nil {
			return nil, "", &PathError{
				Op: "write", Path: p.given, Root: p.Root(), Kind: KindIO,
				Message: "a temporary file name could not be generated", Err: err,
			}
		}
		name := filepath.Join(directory, prefix+hex.EncodeToString(suffix))
		file, err := p.owner.directory.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, perm)
		if err == nil {
			return file, name, nil
		}
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return nil, "", p.fail("write", err)
	}
	return nil, "", &PathError{
		Op: "write", Path: p.given, Resolved: p.real, Root: p.Root(), Kind: KindIO,
		Message: "a temporary file could not be created next to the destination",
	}
}
