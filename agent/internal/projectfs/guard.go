// Package projectfs is the agent's only door to the filesystem.
//
// It holds the set of project roots the user has authorized and resolves every
// incoming path against them. A path that is not inside one of those roots is
// refused, and the refusal is a *PathError with KindForbidden, which a caller
// can tell apart from a file that is merely missing.
//
// Two decisions are the whole of this package, and both of them are about the
// difference between checking a path and using it.
//
// Resolution happens on the real path. Every segment is walked against the
// filesystem, symbolic links are followed, and ".." is applied to the parent
// that link resolution arrived at rather than to the text of the path. The
// containment check then compares real absolute paths segment by segment, so
// "/home/user/projectX" is not inside "/home/user/project" and neither is
// "/home/user/Project". See realPath and within in resolve.go.
//
// A ".." in the caller's path is honoured only when the directory it lands on
// is inside an authorized root. One that climbs out is refused as forbidden at
// that segment, even when the rest of the path would come back in, and before
// anything after it is looked up: "root/../x/../root/file" is refused without x
// ever being asked about, so the answer is the same whether x is a directory,
// a file or nothing. Symbolic link targets are not held to this. A link is
// judged by where it really leads, like any other, because its target was
// written by someone who could already write the root, not by the caller. See
// walk in resolve.go.
//
// Authorization is enforced again when the file is opened, by the kernel, and
// not by re-reading the path. Each authorized root is held open as an *os.Root
// (Go 1.24), which is a directory descriptor plus a resolver that walks each
// component relative to that descriptor and refuses any component that would
// leave it. Every operation in this package goes through the *os.Root of the
// path's own root with a path relative to it, so a symbolic link swapped in
// between the check and the open makes the open fail; it cannot make the open
// land somewhere else. That is why the check at resolve time is a courtesy to
// the caller -- it gives an answer and an error kind before any work starts --
// while the guarantee lives in the open itself. See Path in path.go.
//
// Nothing here runs a command. No path reaches a shell from this package or
// from anywhere else in the agent, and boundary_test.go holds the whole module
// to that.
//
// What this does not stop, written down because a guard whose edges are
// guessed at is worse than one whose edges are known:
//
//   - A hard link inside a root to a file outside it. The file is inside the
//     root, by one of its names, and no amount of path checking can see the
//     difference; creating one needs write access to the root already.
//   - A bind mount, or a /proc magic link, placed inside a root. os.Root says
//     so itself: it confines paths, not mounts. Both need privileges this
//     agent does not have and does not use.
//   - A differently cased spelling of an authorized root on a case-insensitive
//     filesystem. That is refused, which is the fail-closed direction and
//     costs usability rather than safety; within in resolve.go says why.
//   - Anything about what happens to a file after it is handed over. This
//     package decides which files may be reached, not what is done with them.
package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// root is one authorized project root: the spelling the user authorized, the
// real path it resolved to, and a descriptor for that directory held open for
// as long as the root is authorized.
//
// Holding the descriptor is not an optimization. It pins the directory the
// user authorized, so replacing that directory -- with a symbolic link, with
// another directory of the same name -- does not redirect anything: operations
// keep going to the directory that was authorized, in whatever place it has
// been moved to.
type root struct {
	// given is the path as it was authorized, kept for reporting.
	given string
	// real is the real, absolute, link-free path of this root.
	real string
	// directory is the open descriptor every operation under this root goes
	// through.
	directory *os.Root

	// revoked is set when the root stops being authorized, by Revoke or by
	// Close, before its descriptor is closed. Every Path holds the root it was
	// resolved against, so this is what makes a handle taken earlier stop
	// working: Path.usable refuses it, and the closed descriptor refuses
	// anything already past that check.
	revoked atomic.Bool

	// escapeOnce and escaped hold os.Root's own refusal error, asked for once
	// and compared against afterwards. See escapeSentinel.
	escapeOnce sync.Once
	escaped    error

	// pinnedFrom is set only on a root that stands for a directory held open
	// by Path.Pin (pin.go): it is the authorized root that directory is in,
	// whose revocation stops the pin too. base is the pinned directory's path
	// from that root. Both are zero on an authorized root.
	pinnedFrom *root
	base       string
}

// release ends this root's authorization: first the flag every Path checks,
// then the descriptor. os.Root counts the operations in flight on it, so one
// that started before the release finishes, and none can start after it.
func (r *root) release() error {
	r.revoked.Store(true)
	return r.directory.Close()
}

// escapeSentinel is the error os.Root returns when a path would leave the
// root. The value is unexported in os, so it is obtained from the root itself:
// every root refuses "..", and that refusal is the sentinel. Asking for it
// beats matching a message, because a message can be reworded.
func (r *root) escapeSentinel() error {
	r.escapeOnce.Do(func() {
		_, err := r.directory.Lstat("..")
		var fault *fs.PathError
		if errors.As(err, &fault) {
			r.escaped = fault.Err
		}
	})
	return r.escaped
}

// escapeMessage is the sentinel's wording, used only if asking the root for
// the sentinel itself ever stops working. Reading an escape as plain IO would
// mislabel a failure; it cannot turn one into a success, because the operation
// has already failed by the time anything is classified.
const escapeMessage = "path escapes from parent"

// isEscape reports whether err is this root refusing to let an operation leave
// it.
func (r *root) isEscape(err error) bool {
	if err == nil {
		return false
	}
	if sentinel := r.escapeSentinel(); sentinel != nil && errors.Is(err, sentinel) {
		return true
	}
	return strings.Contains(err.Error(), escapeMessage)
}

// Guard holds the authorized project roots and resolves paths against them.
//
// A Guard with no roots refuses every path, which is the right answer before
// the user has authorized anything. It is safe for concurrent use.
//
// Authorizing a root is a deliberate act by the user; the API that offers the
// user that choice is #7, and this package only holds the answer.
type Guard struct {
	mutex sync.RWMutex
	roots []*root
	// closed is set by Close, after which nothing resolves.
	closed bool
}

// New returns a Guard with each of roots authorized. A root that cannot be
// authorized fails the whole call and leaves nothing open.
func New(roots ...string) (*Guard, error) {
	guard := &Guard{}
	for _, candidate := range roots {
		if _, err := guard.Authorize(candidate); err != nil {
			_ = guard.Close()
			return nil, err
		}
	}
	return guard, nil
}

// Authorize adds a project root to the set this Guard allows, and returns the
// real path it was recorded as.
//
// The root must be an existing directory. It is resolved the same way every
// other path is, so authorizing a symbolic link authorizes the directory it
// points at, under that directory's own real name -- which is the only name
// containment can be checked against. A path that is already authorized, under
// any spelling that resolves to the same directory, is accepted again and
// changes nothing.
func (g *Guard) Authorize(candidate string) (string, error) {
	return g.authorize(candidate, nil)
}

// authorize is Authorize with one more check, made after the directory has
// been opened and before it is added: verify is handed the real path and the
// open descriptor, and a non-nil answer refuses the root and closes it again.
// Roots uses it to confirm that the directory opened is the one it checked,
// so a component swapped for a link between its check and this open cannot
// authorize somewhere else.
func (g *Guard) authorize(candidate string, verify func(realDir string, opened *os.Root) error) (string, error) {
	fail := func(kind ErrorKind, message string, err error) (string, error) {
		return "", &PathError{Op: "authorize", Path: candidate, Kind: kind, Message: message, Err: err}
	}
	if err := usablePath(candidate); err != nil {
		var fault *PathError
		if errors.As(err, &fault) {
			fault.Op = "authorize"
			return "", fault
		}
		return "", err
	}
	absolutePath, err := absolute(candidate)
	if err != nil {
		return fail(KindIO, "the working directory could not be read, so a relative root cannot be made absolute", err)
	}
	resolved, missing, err := realPath(absolutePath)
	if err != nil {
		kind, message := classifyResolution(err)
		return "", &PathError{
			Op: "authorize", Path: candidate, Resolved: resolved,
			Kind: kind, Message: message, Err: err,
		}
	}
	if len(missing) > 0 {
		return "", &PathError{
			Op: "authorize", Path: candidate, Resolved: resolved, Kind: KindNotFound,
			Location: filepath.Join(missing...),
			Message:  "the directory to authorize does not exist",
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fail(KindIO, "the directory to authorize could not be read", err)
	}
	if !info.IsDir() {
		return "", &PathError{
			Op: "authorize", Path: candidate, Resolved: resolved, Kind: KindNotDirectory,
			Message: "a project root must be a directory",
		}
	}

	g.mutex.Lock()
	defer g.mutex.Unlock()
	if g.closed {
		return "", &PathError{Op: "authorize", Path: candidate, Kind: KindInvalid, Message: "this guard is closed"}
	}
	for _, existing := range g.roots {
		if existing.real == resolved {
			return existing.real, nil
		}
	}
	directory, err := os.OpenRoot(resolved)
	if err != nil {
		return fail(KindIO, "the directory to authorize could not be opened", err)
	}
	if verify != nil {
		if err := verify(resolved, directory); err != nil {
			_ = directory.Close()
			return "", err
		}
	}
	g.roots = append(g.roots, &root{given: candidate, real: resolved, directory: directory})
	return resolved, nil
}

// Revoke removes an authorized root and returns the real path it was
// authorized as.
//
// The root may be named by its real path, with or without a trailing
// separator, or by any spelling that resolves to it, the link it was
// authorized through included. A nested root inside or around it is a root of
// its own and is left alone.
//
// Revoking is the same as Close for that one root: nothing resolves into it
// any more, and a Path taken before the revoke stops working, because the
// root it holds is marked revoked and its descriptor closed. An operation that
// was already running when Revoke was called may finish; nothing that starts
// after Revoke returns reaches the directory. Nothing on disk is touched.
//
// A path that is not an authorized root is KindNotFound.
func (g *Guard) Revoke(candidate string) (string, error) {
	if err := usablePath(candidate); err != nil {
		var fault *PathError
		if errors.As(err, &fault) {
			fault.Op = "revoke"
		}
		return "", err
	}
	notAuthorized := &PathError{
		Op: "revoke", Path: candidate, Kind: KindNotFound,
		Message: "this path is not an authorized project directory",
	}
	// The exact real path first, which needs nothing from the filesystem
	// and so still works for a root whose directory has since gone.
	if filepath.IsAbs(candidate) {
		if realDir, ok := g.revokeExact(filepath.Clean(candidate)); ok {
			return realDir, nil
		}
	}
	absolutePath, err := absolute(candidate)
	if err != nil {
		return "", notAuthorized
	}
	resolved, missing, err := realPath(absolutePath)
	if err != nil || len(missing) > 0 {
		return "", notAuthorized
	}
	if realDir, ok := g.revokeExact(resolved); ok {
		return realDir, nil
	}
	return "", notAuthorized
}

// revokeExact revokes the root whose real path is exactly real.
func (g *Guard) revokeExact(realDir string) (string, bool) {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	for index, authorized := range g.roots {
		if authorized.real != realDir {
			continue
		}
		g.roots = append(g.roots[:index:index], g.roots[index+1:]...)
		_ = authorized.release()
		return authorized.real, true
	}
	return "", false
}

// heldInfo describes the directory the root whose real path is exactly realDir
// holds open: the directory that was authorized, wherever it is now and
// whatever is now at realDir. It is false when there is no such root, or the
// descriptor cannot be described.
func (g *Guard) heldInfo(realDir string) (fs.FileInfo, bool) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	for _, authorized := range g.roots {
		if authorized.real == realDir {
			info, err := authorized.directory.Stat(".")
			return info, err == nil
		}
	}
	return nil, false
}

// Roots returns the real paths of the authorized roots, in the order they were
// authorized.
func (g *Guard) Roots() []string {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	out := make([]string, 0, len(g.roots))
	for _, authorized := range g.roots {
		out = append(out, authorized.real)
	}
	return out
}

// Close releases the descriptors the authorized roots hold and refuses
// everything afterwards. A Guard that is never closed leaks one descriptor per
// root, which is why a test closes it.
func (g *Guard) Close() error {
	g.mutex.Lock()
	defer g.mutex.Unlock()
	g.closed = true
	var first error
	for _, authorized := range g.roots {
		if err := authorized.release(); err != nil && first == nil {
			first = err
		}
	}
	g.roots = nil
	return first
}

// usablePath rejects the two path shapes this package will not reason about at
// all, before anything touches the filesystem.
func usablePath(candidate string) error {
	if candidate == "" {
		return &PathError{Path: candidate, Kind: KindInvalid, Message: "no path was given"}
	}
	if strings.ContainsRune(candidate, 0) {
		// A NUL truncates the path at the system call, so the path the checks
		// read and the path the kernel opens would not be the same path.
		return &PathError{Path: candidate, Kind: KindInvalid, Message: "a path cannot contain a NUL byte"}
	}
	return nil
}

// classifyResolution turns a failure of the real-path walk into a kind and a
// message. It never produces KindForbidden: whether a path is allowed is
// decided by the containment check, not by what the filesystem said.
func classifyResolution(err error) (ErrorKind, string) {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return KindUnreadable, "a directory on the way to this path cannot be read by this process"
	case errors.Is(err, errNotDirectory):
		return KindNotDirectory, "a component of this path is not a directory"
	case errors.Is(err, errTooManyLinks):
		return KindInvalid, "this path has too many symbolic links on it"
	case errors.Is(err, fs.ErrNotExist):
		return KindNotFound, "this path does not exist"
	default:
		return KindIO, "this path could not be resolved"
	}
}

// Resolve checks path against the authorized roots and returns a handle to it.
//
// The path may name something that does not exist yet, because a file that is
// about to be created still has to be inside a root. What Resolve guarantees
// is that the real path, after every symbolic link on it has been followed, is
// an authorized root or inside one.
//
// Resolve is not the authorization. It is an answer the caller can act on --
// which root the path belongs to, and a specific error kind when it belongs to
// none -- and it is checked again, by the kernel, every time the returned
// handle is used. Nothing that happens between the two can widen what the
// handle reaches.
func (g *Guard) Resolve(path string) (Path, error) {
	return g.resolve("resolve", path)
}

func (g *Guard) resolve(op, given string) (Path, error) {
	if err := usablePath(given); err != nil {
		var fault *PathError
		if errors.As(err, &fault) {
			fault.Op = op
		}
		return Path{}, err
	}
	absolutePath, err := absolute(given)
	if err != nil {
		return Path{}, &PathError{
			Op: op, Path: given, Kind: KindIO,
			Message: "the working directory could not be read, so a relative path cannot be made absolute", Err: err,
		}
	}

	resolved, missing, resolveErr := walk(absolutePath, func(candidate string) bool {
		owner, _ := g.locate(candidate)
		return owner != nil
	})
	if errors.Is(resolveErr, errClimbsOut) {
		return Path{}, g.climbedOut(op, given)
	}
	base, tail, climbed := reduceTail(resolved, missing)
	candidate := base
	if len(tail) > 0 {
		candidate = filepath.Join(append([]string{base}, tail...)...)
	}

	owner, relative := g.locate(candidate)
	if owner == nil {
		// Also refused when the walk failed: the part of the path that did
		// resolve is outside every root, so there is nothing to report about
		// it beyond the refusal.
		if resolveErr != nil {
			if inside, _ := g.locate(resolved); inside == nil {
				return Path{}, g.forbidden(op, given, resolved)
			}
			kind, message := classifyResolution(resolveErr)
			return Path{}, &PathError{
				Op: op, Path: given, Resolved: resolved,
				Kind: kind, Message: message, Err: resolveErr,
			}
		}
		return Path{}, g.forbidden(op, given, candidate)
	}
	if resolveErr != nil {
		kind, message := classifyResolution(resolveErr)
		return Path{}, &PathError{
			Op: op, Path: given, Resolved: resolved, Root: owner.real,
			Kind: kind, Message: message, Err: resolveErr,
		}
	}
	if climbed {
		return Path{}, &PathError{
			Op: op, Path: given, Resolved: candidate, Root: owner.real, Kind: KindInvalid,
			Message: "this path goes back up through a component that does not exist",
		}
	}
	return Path{owner: owner, relative: relative, real: candidate, given: given}, nil
}

// notInsideMessage is the wording of the plain refusal.
const notInsideMessage = "this path is not inside any authorized project directory"

// forbidden is the one refusal this package exists for, worded the same way
// whatever the path was, and carrying no root: a path that belongs to no root
// has nothing to be reported against.
func (g *Guard) forbidden(op, given, resolved string) *PathError {
	g.mutex.RLock()
	closed := g.closed
	count := len(g.roots)
	g.mutex.RUnlock()
	message := notInsideMessage
	switch {
	case closed:
		message = "this guard is closed, so no path is authorized"
	case count == 0:
		message = "no project directory has been authorized yet"
	}
	return &PathError{
		Op: op, Path: given, Resolved: resolved, Kind: KindForbidden, Message: message,
	}
}

// climbedOut is the refusal for a path with a ".." that climbs out of the
// authorized roots. It is decided before anything past that ".." is looked up,
// and it carries nothing but the caller's own path: no resolved path, no root
// and no cause, so the same refusal comes back whether what the path names
// outside the roots is a directory, a file or nothing at all.
func (g *Guard) climbedOut(op, given string) *PathError {
	refusal := g.forbidden(op, given, "")
	if refusal.Message == notInsideMessage {
		refusal.Message = `this path has a ".." that climbs out of the authorized project directories`
	}
	return refusal
}

// locate finds the authorized root a real path belongs to, and the path from
// that root to it. The most specific root wins, so nesting one authorized root
// inside another is unambiguous rather than order-dependent.
func (g *Guard) locate(candidate string) (*root, string) {
	g.mutex.RLock()
	defer g.mutex.RUnlock()
	if g.closed {
		return nil, ""
	}
	var best *root
	var bestRelative string
	bestDepth := -1
	for _, authorized := range g.roots {
		relative, ok := within(authorized.real, candidate)
		if !ok {
			continue
		}
		if depth := len(segments(authorized.real)); depth > bestDepth {
			best, bestRelative, bestDepth = authorized, relative, depth
		}
	}
	return best, bestRelative
}
