package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// PrivateFile is a file of the agent's own beside the roots store, in the
// same directory (os.UserConfigDir()/kpv, handed out by OpenPrivateFile), for state that must never be
// inside a project: the connection store (#24). Because it shares that
// directory, the rule that a directory holding the store cannot be a root
// covers it too, so it is outside every authorized root by construction.
//
// It hands back bytes, never a path or a handle. A file that cannot be read
// is moved aside to <name>.corrupt-<UTC time> before anything is written over
// it, and while it cannot be, Write writes nothing and fails. It is safe for
// concurrent use; its owner decides what the bytes mean.
type PrivateFile struct {
	mutex sync.Mutex
	file  *privateFile
}

// OpenPrivateFile returns the file called name beside roots' store, or nil
// when the roots have no store, in which case its owner keeps its state in
// memory for as long as the process runs, as the roots do. name is one plain
// file name, not the roots store's own; anything else is KindInvalid.
//
// It is a function and not a method of Roots so that holding a Roots, as
// internal/httpapi does, gives no way to a file that can be written.
func OpenPrivateFile(r *Roots, name string) (*PrivateFile, error) {
	if r == nil {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "a private file needs the roots it sits beside"}
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") || strings.HasPrefix(name, ".") {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "a private file is one plain name"}
	}
	if r.store == nil {
		return nil, nil
	}
	if name == filepath.Base(r.store.file) {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "the roots store is not handed out"}
	}
	file := &privateFile{file: filepath.Join(filepath.Dir(r.store.file), name), real: r.store.real}
	return &PrivateFile{file: file}, nil
}

// Read returns the file's bytes, and whether there is a file. The problem is
// "" when it was read or is missing, and otherwise "unreadable" or
// "corrupt" (over 1 MiB); the file is then due to be moved aside, which
// SetAside does at once and Write does first.
func (p *PrivateFile) Read() ([]byte, bool, ErrorKind) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	return p.file.read()
}

// SetAside marks a file whose bytes its owner could not use as corrupt and
// moves it aside now. Its error is KindIO when it could not be moved; it is
// then tried again by the next Write, which writes nothing until it is.
func (p *PrivateFile) SetAside() error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	p.file.pendingBackup = true
	return p.file.moveAside()
}

// Write replaces the file with body, atomically, as 0600 in a 0700
// directory whatever the umask. Its error is KindIO and names no path.
func (p *PrivateFile) Write(body []byte) error {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if err := p.file.write(body); err != nil {
		return &PathError{Op: "store", Kind: KindIO, Message: "the private file could not be written"}
	}
	return nil
}

// MaxPrivateTreeFile is the most bytes a file of a PrivateTree holds; a
// larger one is corrupt and is moved aside rather than read.
const MaxPrivateTreeFile = 64 << 20

// PrivateTree is a directory of the agent's own beside the roots store
// (os.UserConfigDir()/kpv/<name>/, handed out by OpenPrivateTree), holding
// flat files for state that must never be inside a project: the working
// copies and snapshots of internal/workcopy (#43). Being inside the store's
// directory is what keeps it outside every root, by the same rule as
// PrivateFile: a directory holding the store cannot be authorized.
//
// The tree is made 0700, and so is the store directory above it, on every
// write whatever the umask; its files are written atomically as 0600. Each
// file is a PrivateFile with the same read, move-aside to
// <name>.corrupt-<UTC time> and atomic write, and at most MaxPrivateTreeFile
// bytes. It hands back names and bytes, never a path or a handle. Its owner
// serializes what it does with the files.
type PrivateTree struct {
	mutex sync.Mutex
	dir   string
	real  string
}

// OpenPrivateTree returns the directory called name beside roots' store, or
// nil when the roots have no store. name is one plain name, not the roots
// store's own; anything else is KindInvalid. Nothing is made until the first
// write. It is a function and not a method of Roots for the reason
// OpenPrivateFile gives.
func OpenPrivateTree(r *Roots, name string) (*PrivateTree, error) {
	if r == nil {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "a private tree needs the roots it sits beside"}
	}
	if !plainPrivateName(name) {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "a private tree is one plain name"}
	}
	if r.store == nil {
		return nil, nil
	}
	if name == filepath.Base(r.store.file) {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "the roots store is not handed out"}
	}
	return &PrivateTree{dir: filepath.Join(filepath.Dir(r.store.file), name), real: filepath.Join(r.store.real, name)}, nil
}

// plainPrivateName is one file name that is not hidden: no separator, no
// NUL, not "." or "..".
func plainPrivateName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\x00") && !strings.HasPrefix(name, ".")
}

// File returns the file called name in the tree. name is one plain name that
// is not a moved-aside one (holding ".corrupt-"); anything else is
// KindInvalid.
func (t *PrivateTree) File(name string) (*PrivateFile, error) {
	if !plainPrivateName(name) || strings.Contains(name, corruptMarker) {
		return nil, &PathError{Op: "store", Kind: KindInvalid, Message: "a private file is one plain name"}
	}
	file := &privateFile{file: filepath.Join(t.dir, name), real: t.real, limit: MaxPrivateTreeFile, inTree: true}
	return &PrivateFile{file: file}, nil
}

// corruptMarker is in the name a file is moved aside to.
const corruptMarker = ".corrupt-"

// Names is the name of every file in the tree, sorted, leaving out hidden
// names (the temporary files of a write) and files moved aside. A tree not
// made yet has none. Its error is KindIO and names no path.
func (t *PrivateTree) Names() ([]string, error) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	entries, err := os.ReadDir(t.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, &PathError{Op: "store", Kind: KindIO, Message: "the private tree could not be listed"}
	}
	names := []string{}
	for _, entry := range entries {
		name := entry.Name()
		if !plainPrivateName(name) || strings.Contains(name, corruptMarker) || !entry.Type().IsRegular() {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// Remove removes the file called name from the tree; one that is not there
// is no error. Its error is KindInvalid for a name File refuses, KindIO
// otherwise, and names no path.
func (t *PrivateTree) Remove(name string) error {
	if !plainPrivateName(name) || strings.Contains(name, corruptMarker) {
		return &PathError{Op: "store", Kind: KindInvalid, Message: "a private file is one plain name"}
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if err := os.Remove(filepath.Join(t.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &PathError{Op: "store", Kind: KindIO, Message: "the private file could not be removed"}
	}
	return nil
}
