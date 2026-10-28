package projectfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

// MaxListing is the most entries one listing returns. A directory with more
// than this is answered with a page of this many and Truncated set, never with
// everything it holds: reading a directory is bounded by this number, not by
// the size of the directory.
const MaxListing = 1000

// EntryKind is what a directory entry is, as the entry itself says without
// following it: a symbolic link is a symlink here, whatever it points at.
type EntryKind string

const (
	// EntryDirectory is a directory.
	EntryDirectory EntryKind = "directory"
	// EntryFile is a regular file.
	EntryFile EntryKind = "file"
	// EntrySymlink is a symbolic link. What it leads to is in LinkTarget.
	EntrySymlink EntryKind = "symlink"
	// EntryOther is anything else: a named pipe, a socket, a device.
	EntryOther EntryKind = "other"
	// EntryUnknown is an entry this process could not even describe, which
	// is always also Unreadable. It is listed with its name rather than left
	// out.
	EntryUnknown EntryKind = "unknown"
)

// LinkTarget is what a symbolic link leads to, judged by the same rules as
// any other path: through the guard, inside the browsable directories only.
type LinkTarget string

const (
	// LinkToDirectory leads to a directory that can be browsed.
	LinkToDirectory LinkTarget = "directory"
	// LinkToFile leads to a regular file inside the browsable directories.
	LinkToFile LinkTarget = "file"
	// LinkToOther leads to something inside the browsable directories that is
	// neither a directory nor a regular file.
	LinkToOther LinkTarget = "other"
	// LinkBroken leads nowhere: its target is inside the browsable
	// directories and does not exist, or the link is part of a cycle.
	LinkBroken LinkTarget = "broken"
	// LinkOutside leads out of the browsable directories. It is not
	// followed, so nothing is said about what, if anything, is there.
	LinkOutside LinkTarget = "outside"
	// LinkUnknown is a link whose target could not be looked at, because a
	// directory on the way cannot be read. The entry is Unreadable as well.
	LinkUnknown LinkTarget = "unknown"
)

// Entry is one entry of a listing.
type Entry struct {
	// Name is the entry's name in its directory.
	Name string
	// Path is the directory's real path joined with Name. For a symbolic
	// link it is the link, not where the link leads.
	Path string
	// Kind is what the entry is, without following it.
	Kind EntryKind
	// Unreadable says this process cannot read the entry: a directory it
	// cannot open, a file it cannot open, an entry it cannot describe at all,
	// or a link whose target is one of those.
	Unreadable bool
	// LinkTarget is what a symbolic link leads to. It is "" for anything that
	// is not a link.
	LinkTarget LinkTarget
}

// Evidence is one reason a directory looks like a project: a marker file or
// directory found in it, and what that marker usually means.
type Evidence struct {
	Marker    string
	Describes string
}

// Listing is one page of a directory.
//
// It holds strings and nothing else. In particular it holds no Path, so what a
// caller gets back from browsing is a description it can show, never a handle
// it could read or write through.
type Listing struct {
	// Path is the real path of the directory listed.
	Path string
	// Parent is the real path of the directory above it, when that is also
	// browsable, and "" when it is not: the top of a browsable directory has
	// no parent to go to.
	Parent string
	// Entries are the entries of the page, sorted by name.
	Entries []Entry
	// Truncated says the directory has more entries than the page holds.
	Truncated bool
	// Limit is the most entries the page could hold.
	Limit int
	// LooksLikeProject is the cheap heuristic: one or more project markers
	// were found directly in the directory. Real detection is #41.
	LooksLikeProject bool
	// Evidence is every marker that was found, in a fixed order.
	Evidence []Evidence
}

// projectMarkers are the names whose presence makes a directory look like a
// project. They are looked for directly in the listed directory and nowhere
// else, and only their presence is checked: nothing is opened or parsed, which
// is what keeps this a heuristic and leaves real detection to #41 and manifest
// parsing to #22.
var projectMarkers = []Evidence{
	{".git", "a Git repository"},
	{"go.mod", "a Go module"},
	{"package.json", "a Node.js package"},
	{"pyproject.toml", "a Python project"},
	{"setup.py", "a Python package"},
	{"requirements.txt", "Python requirements"},
	{"Cargo.toml", "a Rust crate"},
	{"pom.xml", "a Maven project"},
	{"build.gradle", "a Gradle project"},
	{"build.gradle.kts", "a Gradle project"},
	{"Gemfile", "a Ruby project"},
	{"composer.json", "a PHP project"},
}

// Browser is a read-only view of the directories a user may browse: the
// authorized project roots, plus their home directory.
//
// The home directory is browsable without being authorized, and that is the
// whole reason this type exists rather than the HTTP API holding a Guard. The
// home directory is held by a Guard of its own that never leaves this value,
// and a Browser has no operation that writes, opens a file for the caller, or
// hands out a Path. So the home allowance widens what can be looked at and
// nothing else: every write in the agent still goes through the authorized
// Guard, which does not have the home directory in it unless the user
// authorizes it there.
//
// Every path is resolved by a Guard, so everything #6 holds a path to holds
// here: real-path resolution, the climb rule, symbolic links judged by where
// they lead, and every read performed through a root's descriptor. A link out
// of the browsable directories is never followed to enumerate what it leads
// to.
type Browser struct {
	authorized *Guard
	// home is the browse-only allowance. It is unexported and never returned,
	// which is what keeps it browse-only.
	home *Guard
	// roots is the user's set of roots built over this Browser, if any. A
	// browse that lands in one of them has it checked first (#67).
	roots atomic.Pointer[Roots]
}

// NewBrowser returns a Browser over the roots authorized holds, plus home.
// authorized is shared rather than copied, so a root authorized later is
// browsable at once. home is the user's home directory, or "" for none; it
// must be an existing directory.
func NewBrowser(authorized *Guard, home string) (*Browser, error) {
	if authorized == nil {
		return nil, &PathError{Op: "browse", Kind: KindInvalid, Message: "a browser needs the guard that holds the authorized roots"}
	}
	browser := &Browser{authorized: authorized}
	if home != "" {
		guard, err := New(home)
		if err != nil {
			return nil, err
		}
		browser.home = guard
	}
	return browser, nil
}

// Home is the real path of the home directory being browsed, or "" when there
// is none.
func (b *Browser) Home() string {
	if b == nil || b.home == nil {
		return ""
	}
	roots := b.home.Roots()
	if len(roots) == 0 {
		return ""
	}
	return roots[0]
}

// Close releases the home directory's descriptor. The authorized Guard is not
// closed: it was handed in, and it is not this value's to close.
func (b *Browser) Close() error {
	if b == nil || b.home == nil {
		return nil
	}
	return b.home.Close()
}

// resolve resolves path against the authorized roots first and the home
// directory second. A path either one accepts is browsable; a path both refuse
// is refused with the same forbidden answer either would give.
//
// A path that lands in a stored root has that root checked first, so a root
// that is gone, or is now another directory, is released and the path is
// resolved again without it: what is at that path now is browsed through the
// home allowance, read-only, and never through the descriptor of the
// directory that was authorized. confirmed holds the roots already checked by
// the same listing, so one listing checks each root once.
func (b *Browser) resolve(path string, confirmed map[string]bool) (Path, error) {
	resolved, err := b.authorized.resolve("browse", path)
	if held := landedIn(resolved, err); held != "" && !b.confirm(held, confirmed) {
		resolved, err = b.authorized.resolve("browse", path)
	}
	if err == nil || !IsForbidden(err) || b.home == nil {
		return resolved, err
	}
	return b.home.resolve("browse", path)
}

// confirm reports whether the authorized root realDir is still the directory
// that was authorized, asking the Roots built over this Browser once per
// listing.
func (b *Browser) confirm(realDir string, confirmed map[string]bool) bool {
	roots := b.roots.Load()
	if roots == nil || confirmed[realDir] {
		return true
	}
	if !roots.confirm(realDir) {
		return false
	}
	confirmed[realDir] = true
	return true
}

// landedIn is the real path of the authorized root a resolution landed in,
// whether it succeeded or failed inside that root, and "" when it landed in
// none.
func landedIn(resolved Path, err error) string {
	if err == nil {
		if resolved.owner != nil {
			return resolved.owner.real
		}
		return ""
	}
	var fault *PathError
	if errors.As(err, &fault) {
		return fault.Root
	}
	return ""
}

// List reads one page of the directory at path: at most limit entries, which
// must be between 1 and MaxListing.
//
// path must be absolute. A relative path would be resolved against the
// agent's working directory, which is meaningless to anyone browsing.
//
// The work is bounded by limit, whatever the size of the directory: at most
// limit+1 names are read, the extra one only to find out whether there are
// more. When there are, the page is the first limit names the filesystem
// returned, sorted by name. Directory order is not name order, so a truncated
// page is a sample of the directory and not its alphabetical beginning;
// reading the whole directory to sort it is the unbounded work this refuses.
//
// The failures are *PathError, and their kinds are the answer: KindForbidden
// for a path outside every browsable directory whether or not it exists,
// KindNotFound for one inside that does not, KindUnreadable for one this
// process may not read, KindNotDirectory and KindInvalid. The other fields of
// a forbidden error can describe where resolution stopped outside the
// browsable directories, so a caller answering someone else passes on the
// kind and nothing more.
func (b *Browser) List(path string, limit int) (Listing, error) {
	invalid := func(message string) (Listing, error) {
		return Listing{}, &PathError{Op: "browse", Path: path, Kind: KindInvalid, Message: message}
	}
	if limit < 1 || limit > MaxListing {
		return invalid("a listing holds between 1 and 1000 entries")
	}
	if err := usablePath(path); err != nil {
		return Listing{}, err
	}
	if !filepath.IsAbs(path) {
		return invalid("a directory to browse is named by an absolute path")
	}

	confirmed := map[string]bool{}
	directory, err := b.resolve(path, confirmed)
	if err != nil {
		return Listing{}, err
	}
	info, err := directory.Lstat()
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, &PathError{Op: "browse", Path: path, Kind: KindNotDirectory, Message: "this path is not a directory"}
	}

	names, truncated, err := directory.readNames(limit)
	if err != nil {
		return Listing{}, err
	}
	sort.Strings(names)

	listing := Listing{
		Path:      directory.Real(),
		Entries:   make([]Entry, 0, len(names)),
		Truncated: truncated,
		Limit:     limit,
		Evidence:  []Evidence{},
	}
	for _, name := range names {
		if entry, ok := b.describe(directory, name, confirmed); ok {
			listing.Entries = append(listing.Entries, entry)
		}
	}
	for _, marker := range projectMarkers {
		candidate, err := directory.Join(marker.Marker)
		if err != nil {
			continue
		}
		if _, err := candidate.Lstat(); err == nil {
			listing.Evidence = append(listing.Evidence, marker)
		}
	}
	listing.LooksLikeProject = len(listing.Evidence) > 0
	if above := filepath.Dir(listing.Path); above != listing.Path {
		if parent, err := b.resolve(above, confirmed); err == nil {
			listing.Parent = parent.Real()
		}
	}
	return listing, nil
}

// describe looks at one entry without following it, and reports whether it is
// still there. An entry that vanished between reading the directory and
// looking at it is the only kind left out of a listing: there is nothing left
// to show. One that cannot be looked at is listed, by name, as unreadable.
func (b *Browser) describe(directory Path, name string, confirmed map[string]bool) (Entry, bool) {
	entry := Entry{Name: name, Path: filepath.Join(directory.Real(), name)}
	child, err := directory.Join(name)
	if err != nil {
		entry.Kind, entry.Unreadable = EntryUnknown, true
		return entry, true
	}
	info, err := child.Lstat()
	switch {
	case IsNotFound(err):
		return Entry{}, false
	case err != nil:
		entry.Kind, entry.Unreadable = EntryUnknown, true
		return entry, true
	}
	switch mode := info.Mode(); {
	case mode.IsDir():
		entry.Kind = EntryDirectory
		entry.Unreadable = !child.canRead(true)
	case mode.IsRegular():
		entry.Kind = EntryFile
		entry.Unreadable = !child.canRead(false)
	case mode&fs.ModeSymlink != 0:
		entry.Kind = EntrySymlink
		entry.LinkTarget, entry.Unreadable = b.follow(entry.Path, confirmed)
	default:
		// A pipe, a socket or a device is described and never opened:
		// opening one can block or have an effect, and listing a directory
		// must do neither.
		entry.Kind = EntryOther
	}
	return entry, true
}

// follow says what the symbolic link at path leads to, by resolving it the way
// any browsed path is resolved. A link that leads out of the browsable
// directories is reported as outside and nothing past that is looked at.
func (b *Browser) follow(path string, confirmed map[string]bool) (LinkTarget, bool) {
	target, err := b.resolve(path, confirmed)
	switch KindOf(err) {
	case "":
	case KindForbidden:
		return LinkOutside, false
	case KindNotFound, KindInvalid, KindNotDirectory:
		return LinkBroken, false
	default:
		return LinkUnknown, true
	}
	info, err := target.Lstat()
	switch {
	case IsNotFound(err):
		return LinkBroken, false
	case IsForbidden(err):
		return LinkOutside, false
	case err != nil:
		return LinkUnknown, true
	}
	switch mode := info.Mode(); {
	case mode.IsDir():
		return LinkToDirectory, !target.canRead(true)
	case mode.IsRegular():
		return LinkToFile, !target.canRead(false)
	default:
		return LinkToOther, false
	}
}

// readNames reads at most limit names from the directory, plus one more to
// find out whether there are more than that. Names are read without being
// described, so an entry that cannot be described still has a name to be
// listed under.
func (p Path) readNames(limit int) ([]string, bool, error) {
	if err := p.usable("browse"); err != nil {
		return nil, false, err
	}
	directory, err := p.openToRead(true)
	if err != nil {
		return nil, false, p.fail("browse", err)
	}
	defer func() { _ = directory.Close() }()
	names, err := directory.Readdirnames(limit + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, p.fail("browse", err)
	}
	if len(names) > limit {
		return names[:limit], true, nil
	}
	return names, false, nil
}

// canRead reports whether this process can open the entry for reading, by
// opening it and closing it again. It is how "unreadable" is decided, rather
// than by reading permission bits, because the bits do not know about the
// process's groups, its capabilities or an access control list, and the open
// does.
func (p Path) canRead(directory bool) bool {
	if p.usable("browse") != nil {
		return false
	}
	if !directory && !probesFiles {
		return true
	}
	opened, err := p.openToRead(directory)
	if err != nil {
		return false
	}
	_ = opened.Close()
	return true
}

// openToRead opens the path for reading through its root's descriptor in a way
// that cannot block, whatever has been put under its name since it was looked
// at.
//
// Opening a named pipe for reading blocks until something opens it for
// writing, so opening an entry that was a file a moment ago could hang a
// listing for as long as whoever swapped it in likes. O_NONBLOCK makes that
// open return at once, and it changes nothing for a file or a directory. Where
// there is no such flag, a directory is opened as "name/.", which the root
// walks by opening name as a directory and fails on anything else.
func (p Path) openToRead(directory bool) (*os.File, error) {
	if openNonblock != 0 || !directory {
		return p.owner.directory.OpenFile(p.relative, os.O_RDONLY|openNonblock, 0)
	}
	return p.owner.directory.Open(p.relative + string(filepath.Separator) + ".")
}
