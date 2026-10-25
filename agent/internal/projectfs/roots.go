package projectfs

import (
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// RootStatus is one stored project root as Roots reports it: plain strings
// and a boolean, never a handle.
type RootStatus struct {
	// Path is the real path the root was authorized as.
	Path string
	// Available says the root is authorized in the Guard right now.
	Available bool
	// Reason is why a stored root is not available, and "" when it is. It is
	// one of KindNotFound, KindNotDirectory, KindUnreadable, KindForbidden,
	// KindTooBroad, KindChanged, KindOverlaps and KindIO.
	Reason ErrorKind
}

// Roots is the user's set of authorized project roots (#63): which directories
// the Guard allows, kept in step with a file that survives a restart.
//
// It is the one way a root is authorized or revoked on the user's behalf, and
// it decides which directory may become one. A root is a directory strictly
// inside the home directory, readable and searchable by the agent, that
// neither contains nor is contained by another stored root, and that does not
// hold the agent's own store. Everything else is refused with a kind that says
// why, and a path outside the home directory is refused as KindForbidden
// whatever is there, because it is resolved through the Browser's home guard,
// which answers the same for every outside shape.
//
// The authorization itself is Guard.Authorize on the Guard the Browser
// shares, so a root authorized here is browsable at once, and revoking one is
// Guard.Revoke, which makes a Path taken earlier stop working.
//
// It exposes no operation that reads or writes a file under a root, and no
// Guard or Path: what it hands back is RootStatus and strings. It is safe for
// concurrent use, and one change is made at a time, store write included.
type Roots struct {
	mutex      sync.Mutex
	authorized *Guard
	home       *Guard
	store      *rootStore
	logger     *slog.Logger
	entries    []RootStatus
}

// OpenRoots returns the roots stored in storeFile, each checked again and
// authorized in the Browser's guard if it still passes.
//
// storeFile is the agent's own file, outside every project; "" keeps the set
// in memory only. A missing file is no roots. A file that cannot be read or
// does not parse is reported once to logger, by kind, and the agent starts with
// no roots; the file is moved aside to a named backup before a new one is ever
// written, so it is not silently overwritten. A stored root that no longer
// passes is listed as unavailable with the reason, not dropped.
//
// The only failure is a nil browser.
func OpenRoots(browser *Browser, storeFile string, logger *slog.Logger) (*Roots, error) {
	if browser == nil {
		return nil, &PathError{Op: "roots", Kind: KindInvalid, Message: "the roots need the browser whose guard they authorize into"}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	roots := &Roots{authorized: browser.authorized, home: browser.home, logger: logger}
	if storeFile == "" {
		return roots, nil
	}
	roots.store = newRootStore(storeFile)
	stored, problem := roots.store.load()
	if problem != "" {
		logger.Warn("the stored project roots could not be read, so none are authorized; the file is moved aside", "kind", string(problem))
		if err := roots.store.moveAside(); err != nil {
			logger.Warn("the unreadable store of project roots could not be moved aside yet, so it is not written until it can be", "kind", string(KindOf(err)))
		}
	}
	for _, path := range stored {
		status := roots.recheck(path)
		if !status.Available {
			logger.Warn("a stored project root is not available", "kind", string(status.Reason))
		}
		roots.entries = append(roots.entries, status)
	}
	return roots, nil
}

// List returns every stored root, in the order they were authorized.
func (r *Roots) List() []RootStatus {
	r.mutex.Lock()
	defer r.mutex.Unlock()
	return r.listLocked()
}

func (r *Roots) listLocked() []RootStatus {
	return append([]RootStatus{}, r.entries...)
}

// Authorize makes the directory at path a project root and returns the real
// path it was authorized as. Authorizing a root that already is, under any
// spelling, returns it and changes nothing.
//
// The refusals are *PathError, and only the kind is meant for anyone outside
// the agent: KindInvalid, KindForbidden, KindTooBroad, KindNotFound,
// KindNotDirectory, KindUnreadable, KindOverlaps (OverlappedRoot names the
// stored root) and KindIO.
func (r *Roots) Authorize(path string) (string, error) {
	if err := requestPath("authorize", path); err != nil {
		return "", err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()

	checked, err := r.check("authorize", path)
	if err != nil {
		return "", err
	}
	realDir := checked.Real()
	existing := -1
	for index, entry := range r.entries {
		if entry.Path == realDir {
			existing = index
			continue
		}
		if _, inside := within(entry.Path, realDir); inside {
			return "", overlapping(path, entry.Path)
		}
		if _, contains := within(realDir, entry.Path); contains {
			return "", overlapping(path, entry.Path)
		}
	}
	if existing >= 0 && r.entries[existing].Available {
		return realDir, nil
	}
	if err := r.authorizeChecked(checked); err != nil {
		return "", err
	}
	if existing >= 0 {
		// A stored root that was unavailable at startup and passes now: the
		// store already has it, so only the status changes.
		r.entries[existing] = RootStatus{Path: realDir, Available: true}
		return realDir, nil
	}
	next := append(r.listLocked(), RootStatus{Path: realDir, Available: true})
	if err := r.save(next); err != nil {
		r.authorized.revokeExact(realDir)
		return "", err
	}
	r.entries = next
	return realDir, nil
}

// Revoke removes a stored root, available or not, and returns what is left.
//
// The root may be named as it is listed, with a trailing separator, or by any
// spelling inside the home directory that resolves to it, the link it was
// authorized through included. A path that is not a stored root is
// KindNotFound, the same answer whether or not the directory exists. Nothing
// on disk is touched except the store.
func (r *Roots) Revoke(path string) ([]RootStatus, error) {
	if err := requestPath("revoke", path); err != nil {
		return nil, err
	}
	r.mutex.Lock()
	defer r.mutex.Unlock()

	index := r.find(filepath.Clean(path))
	if index < 0 && r.home != nil {
		if resolved, err := r.home.resolve("revoke", path); err == nil {
			index = r.find(resolved.Real())
		}
	}
	if index < 0 {
		return nil, &PathError{Op: "revoke", Path: path, Kind: KindNotFound, Message: "this path is not an authorized project directory"}
	}
	revoked := r.entries[index]
	next := make([]RootStatus, 0, len(r.entries)-1)
	next = append(next, r.entries[:index]...)
	next = append(next, r.entries[index+1:]...)
	if err := r.save(next); err != nil {
		return nil, err
	}
	r.entries = next
	if revoked.Available {
		r.authorized.revokeExact(revoked.Path)
	}
	return r.listLocked(), nil
}

// OverlappedRoot is the stored root a KindOverlaps refusal is about, and ""
// for any other error. It is the one thing a refusal from Roots carries that
// is meant to be shown, and it is a root the user authorized.
func OverlappedRoot(err error) string {
	var fault *PathError
	if errors.As(err, &fault) && fault.Kind == KindOverlaps {
		return fault.Root
	}
	return ""
}

func overlapping(path, existing string) *PathError {
	return &PathError{
		Op: "authorize", Path: path, Root: existing, Kind: KindOverlaps,
		Message: "this directory is inside, or contains, a project directory that is already authorized",
	}
}

func (r *Roots) find(path string) int {
	for index, entry := range r.entries {
		if entry.Path == path {
			return index
		}
	}
	return -1
}

// save writes entries to the store. Its failure is a fresh io refusal: the
// store's own error names the agent's config directory, which is nobody
// else's business.
func (r *Roots) save(entries []RootStatus) error {
	if r.store == nil {
		return nil
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		paths = append(paths, entry.Path)
	}
	if err := r.store.save(paths); err != nil {
		r.logger.Warn("the store of project roots could not be written", "kind", string(KindOf(err)))
		return refusal("store", "", KindIO, "the project directories could not be stored")
	}
	return nil
}

// requestPath is the shape a root request's path must have before anything
// looks at the filesystem: usable, and absolute, because a relative path would
// be resolved against the agent's working directory.
func requestPath(op, path string) error {
	if err := usablePath(path); err != nil {
		var fault *PathError
		if errors.As(err, &fault) {
			fault.Op = op
		}
		return err
	}
	if !filepath.IsAbs(path) {
		return &PathError{Op: op, Path: path, Kind: KindInvalid, Message: "a project directory is named by an absolute path"}
	}
	return nil
}

// refusal is a fresh error carrying the kind and nothing that resolution
// found, so whatever a refusal is passed to, it has no outside path in it.
func refusal(op, path string, kind ErrorKind, message string) *PathError {
	return &PathError{Op: op, Path: path, Kind: kind, Message: message}
}

// checkedRoot is a directory check approved: resolved through the home guard,
// and what it was when it was looked at, so the open that follows can be held
// to being the same directory.
type checkedRoot struct {
	path Path
	info fs.FileInfo
}

// Real is the real path of the checked directory.
func (c checkedRoot) Real() string { return c.path.Real() }

// check decides whether the directory at path may be a project root, and
// returns it resolved through the home guard when it may.
func (r *Roots) check(op, path string) (checkedRoot, error) {
	forbidden := func() (checkedRoot, error) {
		return checkedRoot{}, refusal(op, path, KindForbidden, "only a directory inside the home directory can be a project directory")
	}
	tooBroad := func() (checkedRoot, error) {
		return checkedRoot{}, refusal(op, path, KindTooBroad, "the home directory, the directories above it and the filesystem root are too broad to be a project directory")
	}
	if r.home == nil {
		return forbidden()
	}
	resolved, err := r.home.resolve(op, path)
	if err != nil {
		switch KindOf(err) {
		case KindForbidden:
			// Outside the home directory. Whether it is above it is decided
			// from the text alone, so the answer depends on nothing outside.
			if r.coversHome(filepath.Clean(path)) {
				return tooBroad()
			}
			return forbidden()
		case KindUnreadable:
			return checkedRoot{}, refusal(op, path, KindUnreadable, "the agent is not permitted to read this directory")
		case KindNotDirectory:
			return checkedRoot{}, refusal(op, path, KindNotDirectory, "a project directory must be a directory")
		case KindNotFound, KindInvalid:
			// KindInvalid here is a link cycle, or a ".." back through a
			// component that is not there: no directory either way.
			return checkedRoot{}, refusal(op, path, KindNotFound, "this directory does not exist")
		default:
			return checkedRoot{}, refusal(op, path, KindIO, "this directory could not be checked")
		}
	}
	if resolved.IsRoot() {
		return tooBroad()
	}
	info, err := resolved.Stat()
	switch {
	case IsNotFound(err):
		return checkedRoot{}, refusal(op, path, KindNotFound, "this directory does not exist")
	case KindOf(err) == KindUnreadable:
		return checkedRoot{}, refusal(op, path, KindUnreadable, "the agent is not permitted to read this directory")
	case err != nil:
		return checkedRoot{}, refusal(op, path, KindIO, "this directory could not be checked")
	case !info.IsDir():
		return checkedRoot{}, refusal(op, path, KindNotDirectory, "a project directory must be a directory")
	}
	if !resolved.canSearch() {
		return checkedRoot{}, refusal(op, path, KindUnreadable, "the agent is not permitted to read this directory")
	}
	if r.store != nil && r.store.overlaps(resolved.Real()) {
		return checkedRoot{}, refusal(op, path, KindForbidden, "only a directory inside the home directory can be a project directory")
	}
	return checkedRoot{path: resolved, info: info}, nil
}

// coversHome reports whether the cleaned absolute path is the home directory
// or above it, by its real path or by the spelling it was given as.
func (r *Roots) coversHome(cleaned string) bool {
	r.home.mutex.RLock()
	defer r.home.mutex.RUnlock()
	for _, home := range r.home.roots {
		if _, ok := within(cleaned, home.real); ok {
			return true
		}
		if filepath.IsAbs(home.given) {
			if _, ok := within(cleaned, filepath.Clean(home.given)); ok {
				return true
			}
		}
	}
	return false
}

// authorizeChecked authorizes the directory check approved, and refuses it if
// the directory the guard opens is not that same directory: a component
// swapped for a link between the check and the open lands somewhere else, and
// that somewhere was never checked.
func (r *Roots) authorizeChecked(checked checkedRoot) error {
	given := checked.path.Given()
	_, err := r.authorized.authorize(checked.Real(), func(realDir string, opened *os.Root) error {
		changed := refusal("authorize", given, KindChanged, "this directory changed while it was being authorized")
		if realDir != checked.Real() {
			return changed
		}
		openedInfo, openErr := opened.Stat(".")
		if openErr != nil || !os.SameFile(openedInfo, checked.info) {
			return changed
		}
		return nil
	})
	if err != nil {
		if KindOf(err) == KindChanged {
			return err
		}
		return refusal("authorize", given, KindIO, "this directory could not be authorized")
	}
	return nil
}

// recheck is the startup check of one stored root: everything Authorize
// checks, plus that it still resolves to the real path it was stored as, and
// that it does not overlap a root stored before it.
func (r *Roots) recheck(stored string) RootStatus {
	unavailable := func(kind ErrorKind) RootStatus { return RootStatus{Path: stored, Reason: kind} }
	checked, err := r.check("authorize", stored)
	if err != nil {
		return unavailable(KindOf(err))
	}
	if checked.Real() != stored {
		return unavailable(KindChanged)
	}
	for _, entry := range r.entries {
		if _, inside := within(entry.Path, stored); inside {
			return unavailable(KindOverlaps)
		}
		if _, contains := within(stored, entry.Path); contains {
			return unavailable(KindOverlaps)
		}
	}
	if err := r.authorizeChecked(checked); err != nil {
		return unavailable(KindOf(err))
	}
	return RootStatus{Path: stored, Available: true}
}

// canSearch reports whether this process can both read the directory's
// entries and reach inside it, by doing both. A directory that can be listed
// but not searched, or searched but not listed, is not one a project can live
// in.
func (p Path) canSearch() bool {
	if p.usable("authorize") != nil {
		return false
	}
	directory, err := p.openToRead(true)
	if err != nil {
		return false
	}
	_, err = directory.Readdirnames(1)
	_ = directory.Close()
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	_, err = p.owner.directory.Lstat(p.relative + string(filepath.Separator) + ".")
	return err == nil
}
