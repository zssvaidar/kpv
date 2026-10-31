package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// PinnedDir is a directory inside an authorized root, held open by its own
// descriptor from the moment it was checked.
//
// A Path names its file relative to the descriptor of its authorized root, so
// the kernel keeps every operation inside the root, but it walks the name
// again each time: a directory on the way that is renamed away and replaced by
// a symbolic link to elsewhere in the root redirects the next operation there.
// A pin closes that. Every operation on Path(), and on any Path joined from
// it, goes through the pinned directory's own descriptor, so renaming that
// directory or any directory above it, or putting a link where it was, changes
// nothing: the operations keep reaching the directory that was checked, in
// whatever place it now is. A link under the pinned directory that leads out
// of it, even to elsewhere in the same root, is refused forbidden, the way a
// link out of a root is.
//
// Path() answers Real, Given and Root as the Path it was pinned from did: the
// real path at pin time and the authorized root. IsRoot is false unless that
// Path was the authorized root. Revoking or closing the authorized root makes
// Path() and every Path from it refuse forbidden, as for any Path; Close does
// the same for this one pin. RenameTo between Paths of two different pins, or
// of a pin and its root, is refused as crossing roots.
//
// A pin holds one descriptor until Close, which a caller must call: closing
// the Guard does not close it, it only makes it refuse.
type PinnedDir struct {
	owner *root
	path  Path
}

// Pin opens the directory p names and holds it, following no symbolic link on
// the way.
//
// It walks p from the descriptor of p's own root (or of the pin p was joined
// from) one component at a time: each single name is Lstat'ed relative to the
// directory reached so far, must be a directory, is opened as a sub-root, and
// must be the same file (os.SameFile) as what the Lstat saw. A component that
// is a symbolic link, wherever it leads, or that changed between the Lstat and
// the open, is refused KindChanged; one that is not a directory
// KindNotDirectory; one that is missing KindNotFound. The zero Path, and a
// Path whose root or pin is revoked or closed, is refused KindForbidden. Every
// descriptor opened on the way is closed before it returns, except the one the
// pin holds.
func (p Path) Pin() (*PinnedDir, error) { return p.pin(nil) }

// pin is Pin with a hook called between each component's Lstat and its open,
// with that component's path from the walk's start, which is how a test swaps
// a directory in that moment. Pin passes none.
func (p Path) pin(between func(component string)) (*PinnedDir, error) {
	if err := p.usable("pin"); err != nil {
		return nil, err
	}
	components := []string{"."}
	if p.relative != "." {
		components = strings.Split(p.relative, string(filepath.Separator))
	}
	refuse := func(at string, kind ErrorKind, message string, err error) error {
		return &PathError{
			Op: "pin", Path: p.given, Resolved: p.real, Root: p.Root(),
			Location: at, Kind: kind, Message: message, Err: err,
		}
	}

	current := p.owner.directory
	owned := false
	release := func() {
		if owned {
			_ = current.Close()
		}
	}
	walked := ""
	for _, name := range components {
		walked = filepath.Join(walked, name)
		info, err := current.Lstat(name)
		if err != nil {
			release()
			fault := p.fail("pin", err)
			var pathErr *PathError
			if errors.As(fault, &pathErr) {
				pathErr.Location = walked
				if pathErr.Kind == KindNotFound {
					pathErr.Message = "a directory on the way to this path does not exist"
				}
			}
			return nil, fault
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			release()
			return nil, refuse(walked, KindChanged, "a directory on the way to this path is now a symbolic link", nil)
		}
		if !info.IsDir() {
			release()
			return nil, refuse(walked, KindNotDirectory, "a component of this path is not a directory", nil)
		}
		if between != nil {
			between(walked)
		}
		next, err := current.OpenRoot(name)
		if err != nil {
			release()
			if errors.Is(err, fs.ErrPermission) {
				return nil, refuse(walked, KindUnreadable, "a directory on the way to this path cannot be opened by this process", err)
			}
			if errors.Is(err, fs.ErrClosed) {
				return nil, p.fail("pin", err)
			}
			return nil, refuse(walked, KindChanged, "a directory on the way to this path changed while it was being opened", err)
		}
		opened, err := next.Stat(".")
		if err != nil || !os.SameFile(info, opened) {
			_ = next.Close()
			release()
			return nil, refuse(walked, KindChanged, "a directory on the way to this path changed while it was being opened", err)
		}
		release()
		current, owned = next, true
	}
	if !owned {
		// Unreachable: there is always at least one component, and each one
		// that succeeds leaves current owned. Kept so a change to the walk
		// cannot hand out the root's own descriptor as a pin.
		return nil, refuse(".", KindIO, "nothing was opened to pin", nil)
	}

	authorized := p.owner
	if authorized.pinnedFrom != nil {
		authorized = authorized.pinnedFrom
	}
	pinned := &root{
		given:      authorized.given,
		real:       authorized.real,
		directory:  current,
		pinnedFrom: authorized,
		base:       p.Rel(),
	}
	return &PinnedDir{
		owner: pinned,
		path:  Path{owner: pinned, relative: ".", real: p.real, given: p.given},
	}, nil
}

// Path returns the pinned directory as a Path. Every operation on it, and on
// a Path joined from it, goes through the pinned descriptor. A nil PinnedDir
// gives the zero Path, which reaches nothing.
func (d *PinnedDir) Path() Path {
	if d == nil {
		return Path{}
	}
	return d.path
}

// Close releases the pinned descriptor. Path() and every Path joined from it
// refuse KindForbidden afterwards; an operation already past that check
// finishes. Closing twice is harmless.
func (d *PinnedDir) Close() error {
	if d == nil || d.owner.revoked.Swap(true) {
		return nil
	}
	return d.owner.directory.Close()
}
