package projectfs

import (
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
