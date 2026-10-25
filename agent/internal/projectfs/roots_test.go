// Roots (#63): which directory may become a project root, the store that
// keeps the set across restarts, and the check every stored root gets again
// at startup.

package projectfs

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// rootsFixture is a real tree:
//
//	base/
//	  home/            the home directory
//	    code/app/      a directory to authorize
//	    code/lib/
//	    notes.txt
//	    .config/kpv/   the agent's store directory
//	  outside/secret/  outside home
//	  outside/secret.txt
type rootsFixture struct {
	t                   *testing.T
	base, home, outside string
	store               string
	logs                *bytes.Buffer
	logMutex            *sync.Mutex
	guard               *Guard
	browser             *Browser
	roots               *Roots
}

func newRootsFixture(t *testing.T) *rootsFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	f := &rootsFixture{
		t: t, base: base,
		home:     filepath.Join(base, "home"),
		outside:  filepath.Join(base, "outside"),
		logMutex: &sync.Mutex{},
	}
	f.store = filepath.Join(f.home, ".config", "kpv", "roots.json")
	for _, directory := range []string{
		filepath.Join(f.home, "code", "app", "src"),
		filepath.Join(f.home, "code", "lib"),
		filepath.Join(f.outside, "secret"),
	} {
		mustMkdirAll(t, directory)
	}
	mustWriteFile(t, filepath.Join(f.home, "notes.txt"), "notes")
	mustWriteFile(t, filepath.Join(f.home, "code", "app", "src", "main.go"), "package main\n")
	mustWriteFile(t, filepath.Join(f.outside, "secret.txt"), "secret")
	f.open()
	return f
}

// open starts the roots over a fresh guard and browser, the way an agent
// starting up does, reading whatever the store holds.
func (f *rootsFixture) open() {
	f.t.Helper()
	f.openWithHome(f.home)
}

func (f *rootsFixture) openWithHome(home string) {
	f.t.Helper()
	guard, err := New()
	if err != nil {
		f.t.Fatalf("new guard: %v", err)
	}
	f.t.Cleanup(func() { _ = guard.Close() })
	browser, err := NewBrowser(guard, home)
	if err != nil {
		f.t.Fatalf("new browser: %v", err)
	}
	f.t.Cleanup(func() { _ = browser.Close() })
	f.logs = &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(lockedWriter{f.logMutex, f.logs}, nil))
	roots, err := OpenRoots(browser, f.store, logger)
	if err != nil {
		f.t.Fatalf("open the roots: %v", err)
	}
	f.guard, f.browser, f.roots = guard, browser, roots
}

type lockedWriter struct {
	mutex  *sync.Mutex
	writer *bytes.Buffer
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.writer.Write(p)
}

func (f *rootsFixture) log() string {
	f.logMutex.Lock()
	defer f.logMutex.Unlock()
	return f.logs.String()
}

func (f *rootsFixture) in(elements ...string) string {
	return filepath.Join(append([]string{f.home}, elements...)...)
}

func (f *rootsFixture) authorize(path string) string {
	f.t.Helper()
	realDir, err := f.roots.Authorize(path)
	if err != nil {
		f.t.Fatalf("authorize %s: %v", path, err)
	}
	return realDir
}

func (f *rootsFixture) storedPaths() []string {
	f.t.Helper()
	raw, err := os.ReadFile(f.store)
	if err != nil {
		f.t.Fatalf("read the store: %v", err)
	}
	paths, ok := parseStore(raw)
	if !ok {
		f.t.Fatalf("the store does not parse: %s", raw)
	}
	return paths
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("make %s: %v", path, err)
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustSymlink(t *testing.T, target, at string) {
	t.Helper()
	if err := os.Symlink(target, at); err != nil {
		t.Fatalf("link %s -> %s: %v", at, target, err)
	}
}

// refusedAs checks a refusal's kind and that it carries nothing resolution
// found: no resolved path, no root, no location and no cause.
func refusedAs(t *testing.T, err error, kind ErrorKind, what string) *PathError {
	t.Helper()
	var fault *PathError
	if !errors.As(err, &fault) {
		t.Fatalf("%s: want a %s refusal, got %v", what, kind, err)
	}
	if fault.Kind != kind {
		t.Fatalf("%s: want %s, got %s (%v)", what, kind, fault.Kind, err)
	}
	if fault.Resolved != "" || fault.Location != "" || fault.Err != nil || (kind != KindOverlaps && fault.Root != "") {
		t.Fatalf("%s: the refusal carries what resolution found: %+v", what, fault)
	}
	return fault
}

func TestADirectoryStrictlyInsideHomeIsAuthorizedAndStored(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	if realDir := f.authorize(app); realDir != app {
		t.Fatalf("authorized %s, want %s", realDir, app)
	}
	if want := []RootStatus{{Path: app, Available: true}}; !reflect.DeepEqual(f.roots.List(), want) {
		t.Fatalf("list = %+v, want %+v", f.roots.List(), want)
	}
	if roots := f.guard.Roots(); len(roots) != 1 || roots[0] != app {
		t.Fatalf("the guard the browser shares holds %v", roots)
	}
	// The authorized guard reaches it at once.
	if err := f.guard.WriteFile(filepath.Join(app, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write inside the new root: %v", err)
	}
	if got := f.storedPaths(); !reflect.DeepEqual(got, []string{app}) {
		t.Fatalf("stored %v", got)
	}
}

func TestAuthorizingARootDoesNotMakeHomeOrAnythingElseWritable(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	f.authorize(f.in("code", "app"))
	for _, path := range []string{f.in("x"), f.in("code", "x"), f.in("code", "lib", "x"), filepath.Join(f.outside, "x")} {
		if err := f.guard.WriteFile(path, []byte("x"), 0o644); !IsForbidden(err) {
			t.Errorf("write %s = %v, want forbidden", path, err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the refused write created %s", path)
		}
	}
}

func TestTheHomeDirectoryAndEverythingAboveItIsTooBroad(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	mustMkdirAll(t, f.in("sub"))
	mustSymlink(t, f.home, f.in("self"))
	mustSymlink(t, f.home, filepath.Join(f.base, "home-link"))
	for _, path := range []string{
		f.home,
		f.home + "/",
		f.home + "/.",
		f.home + "//",
		f.in("sub") + "/..",
		f.in("self"),
		f.in("self") + "/",
		filepath.Join(f.base, "home-link"),
		f.base,
		f.base + "/",
		filepath.Dir(f.base),
		"/",
		f.home + "/..",
	} {
		_, err := f.roots.Authorize(path)
		fault := refusedAs(t, err, KindTooBroad, path)
		if fault.Message != "the home directory, the directories above it and / are too broad to be a project directory" {
			t.Errorf("%s: message %q", path, fault.Message)
		}
	}
	if len(f.roots.List()) != 0 || len(f.guard.Roots()) != 0 {
		t.Fatalf("a too-broad path was authorized: %v", f.roots.List())
	}
}

func TestSlashIsTooBroadWhenHomeIsSlash(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	f.openWithHome("/")
	for _, path := range []string{"/", "/.", "//"} {
		_, err := f.roots.Authorize(path)
		_ = refusedAs(t, err, KindTooBroad, path)
	}
}

// The criterion's uniformity: every outside shape is the same refusal, kind,
// message and all, and carries nothing resolution found.
func TestAPathOutsideHomeIsForbiddenWhateverIsThere(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	mustSymlink(t, "loop-b", filepath.Join(f.outside, "loop-a"))
	mustSymlink(t, "loop-a", filepath.Join(f.outside, "loop-b"))
	mustSymlink(t, filepath.Join(f.outside, "secret"), f.in("to-secret"))
	mustSymlink(t, filepath.Join(f.outside, "secret.txt"), f.in("to-secret-file"))
	mustSymlink(t, filepath.Join(f.outside, "missing"), f.in("to-missing"))
	locked := filepath.Join(f.outside, "locked")
	mustMkdirAll(t, filepath.Join(locked, "inner"))
	if os.Geteuid() != 0 {
		chmod(t, locked, 0o000)
	}
	shapes := []string{
		filepath.Join(f.outside, "secret"),
		filepath.Join(f.outside, "secret.txt"),
		filepath.Join(f.outside, "missing"),
		filepath.Join(f.outside, "missing", "deeper"),
		filepath.Join(f.outside, "secret.txt", "below"),
		locked,
		filepath.Join(locked, "inner"),
		filepath.Join(f.outside, "loop-a"),
		filepath.Join(f.outside, "loop-a", "x"),
		f.in("to-secret"),
		f.in("to-secret-file"),
		f.in("to-missing"),
		f.in("to-secret", "x"),
		f.home + "/../outside/secret",
		f.home + "/../outside/missing",
		"/etc",
		"/no-such-top-level-directory",
	}
	var first *PathError
	for _, shape := range shapes {
		_, err := f.roots.Authorize(shape)
		fault := refusedAs(t, err, KindForbidden, shape)
		if first == nil {
			first = fault
			continue
		}
		if fault.Message != first.Message || fault.Op != first.Op {
			t.Errorf("the refusal of %s differs from the first: %+v vs %+v", shape, fault, first)
		}
	}
	if len(f.roots.List()) != 0 {
		t.Fatalf("an outside path was authorized: %v", f.roots.List())
	}
}

func TestInsideHomeAMissingPathAFileAndAnUnreadableDirectoryAreRefused(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	mustSymlink(t, "cycle-b", f.in("cycle-a"))
	mustSymlink(t, "cycle-a", f.in("cycle-b"))
	cases := map[string]ErrorKind{
		f.in("missing"):                  KindNotFound,
		f.in("missing", "deeper"):        KindNotFound,
		f.in("cycle-a"):                  KindNotFound,
		f.in("notes.txt"):                KindNotDirectory,
		f.in("notes.txt", "below"):       KindNotDirectory,
		f.in("code", "app", "src", "x"):  KindNotFound,
		f.in("code", "app", "src") + "x": KindNotFound,
	}
	if os.Geteuid() != 0 {
		for name, mode := range map[string]os.FileMode{"none": 0o000, "search-only": 0o111, "read-only": 0o444} {
			directory := f.in("locked-" + name)
			mustMkdirAll(t, filepath.Join(directory, "inner"))
			chmod(t, directory, mode)
			cases[directory] = KindUnreadable
		}
		cases[f.in("locked-none", "inner")] = KindUnreadable
		cases[f.in("locked-read-only", "inner")] = KindUnreadable
	} else {
		t.Log("running as root, which reads every directory, so the unreadable cases are not checked here")
	}
	for path, kind := range cases {
		_, err := f.roots.Authorize(path)
		_ = refusedAs(t, err, kind, path)
	}
	if len(f.roots.List()) != 0 || len(f.guard.Roots()) != 0 {
		t.Fatalf("a refused path was authorized: %v", f.roots.List())
	}
	if _, err := os.Lstat(f.store); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refusal wrote the store: %v", err)
	}
}

func TestARequestPathMustBeAbsoluteAndUsable(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	for _, path := range []string{"", "code/app", "./code", f.home + "/x\x00y"} {
		_, err := f.roots.Authorize(path)
		_ = refusedAs(t, err, KindInvalid, path)
		_, err = f.roots.Revoke(path)
		_ = refusedAs(t, err, KindInvalid, path)
	}
}

func TestAuthorizingTheSameDirectoryAgainChangesNothing(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	mustSymlink(t, app, f.in("app-link"))
	f.authorize(app)
	before, err := os.ReadFile(f.store)
	if err != nil {
		t.Fatalf("read the store: %v", err)
	}
	info, err := os.Stat(f.store)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	for _, spelling := range []string{app, app + "/", app + "/.", app + "/src/..", f.in("app-link"), f.in("code") + "//app"} {
		if realDir := f.authorize(spelling); realDir != app {
			t.Fatalf("%s authorized %s, want %s", spelling, realDir, app)
		}
	}
	if len(f.roots.List()) != 1 || len(f.guard.Roots()) != 1 {
		t.Fatalf("a second entry appeared: %v / %v", f.roots.List(), f.guard.Roots())
	}
	after, err := os.ReadFile(f.store)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("the store changed: %s -> %s (%v)", before, after, err)
	}
	if again, err := os.Stat(f.store); err != nil || !os.SameFile(info, again) {
		t.Fatalf("the store was rewritten: %v", err)
	}
}

func TestConcurrentAuthorizationsOfOneDirectoryLeaveOneRoot(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	spellings := []string{app, app + "/", app + "/.", f.in("code") + "/./app"}
	var group sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 32)
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			realDir, err := f.roots.Authorize(spellings[index%len(spellings)])
			if err == nil && realDir != app {
				err = errors.New("authorized " + realDir)
			}
			errs <- err
		}()
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("a concurrent authorization failed: %v", err)
		}
	}
	if len(f.roots.List()) != 1 || len(f.guard.Roots()) != 1 || len(f.storedPaths()) != 1 {
		t.Fatalf("want one root, got %v / %v / %v", f.roots.List(), f.guard.Roots(), f.storedPaths())
	}
}

func TestANestedRootIsRefusedAsOverlapsAndNamesTheExistingOne(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	f.authorize(app)

	_, err := f.roots.Authorize(filepath.Join(app, "src"))
	_ = refusedAs(t, err, KindOverlaps, "inside")
	if OverlappedRoot(err) != app {
		t.Fatalf("the overlap names %q, want %s", OverlappedRoot(err), app)
	}
	_, err = f.roots.Authorize(f.in("code"))
	_ = refusedAs(t, err, KindOverlaps, "containing")
	if OverlappedRoot(err) != app {
		t.Fatalf("the overlap names %q, want %s", OverlappedRoot(err), app)
	}
	if OverlappedRoot(errors.New("other")) != "" || OverlappedRoot(nil) != "" {
		t.Fatal("OverlappedRoot names a root for an error that is not an overlap")
	}
	// A sibling whose name starts the same is not an overlap.
	mustMkdirAll(t, f.in("code", "app-two"))
	f.authorize(f.in("code", "app-two"))

	// Revoke the outer one, and the one that overlapped it can be authorized.
	if _, err := f.roots.Revoke(app); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.authorize(filepath.Join(app, "src"))
	if _, err := f.roots.Revoke(filepath.Join(app, "src")); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.roots.Revoke(f.in("code", "app-two")); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.authorize(f.in("code"))
}

func TestAuthorizingThroughALinkAuthorizesItsRealTarget(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	mustSymlink(t, app, f.in("link"))
	mustSymlink(t, app, filepath.Join(f.outside, "into-home"))
	if realDir := f.authorize(f.in("link")); realDir != app {
		t.Fatalf("authorized %s, want the real %s", realDir, app)
	}
	if list := f.roots.List(); list[0].Path != app {
		t.Fatalf("listed %v", list)
	}
	if got := f.storedPaths(); !reflect.DeepEqual(got, []string{app}) {
		t.Fatalf("stored %v", got)
	}
	// A link outside home that leads into home is judged by where it leads.
	if realDir := f.authorize(filepath.Join(f.outside, "into-home")); realDir != app {
		t.Fatalf("authorized %s", realDir)
	}
	// Re-pointing the link afterwards changes nothing the root reaches.
	if err := os.Remove(f.in("link")); err != nil {
		t.Fatalf("remove the link: %v", err)
	}
	mustSymlink(t, f.in("code", "lib"), f.in("link"))
	if list := f.roots.List(); len(list) != 1 || list[0].Path != app {
		t.Fatalf("listed %v after the link moved", list)
	}
	if data, err := f.guard.ReadFile(filepath.Join(app, "src", "main.go")); err != nil || string(data) != "package main\n" {
		t.Fatalf("read through the root after the link moved: %q, %v", data, err)
	}
	if _, err := f.guard.Resolve(f.in("code", "lib")); !IsForbidden(err) {
		t.Fatalf("the link's new target became authorized: %v", err)
	}
}

func TestADirectoryHoldingTheStoreCannotBeARoot(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	mustMkdirAll(t, filepath.Dir(f.store))
	for _, path := range []string{f.in(".config"), filepath.Dir(f.store)} {
		_, err := f.roots.Authorize(path)
		_ = refusedAs(t, err, KindForbidden, path)
	}
	inside := filepath.Join(filepath.Dir(f.store), "inside")
	mustMkdirAll(t, inside)
	_, err := f.roots.Authorize(inside)
	_ = refusedAs(t, err, KindForbidden, inside)
}

func TestRevokingRemovesTheRootAndDeletesNothing(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	lib := f.in("code", "lib")
	f.authorize(app)
	f.authorize(lib)
	handle, err := f.guard.Resolve(filepath.Join(app, "src", "main.go"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	remaining, err := f.roots.Revoke(app)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if want := []RootStatus{{Path: lib, Available: true}}; !reflect.DeepEqual(remaining, want) || !reflect.DeepEqual(f.roots.List(), want) {
		t.Fatalf("remaining %v, list %v", remaining, f.roots.List())
	}
	if got := f.storedPaths(); !reflect.DeepEqual(got, []string{lib}) {
		t.Fatalf("stored %v", got)
	}
	created := filepath.Join(app, "src", "created.txt")
	if err := f.guard.WriteFile(created, []byte("x"), 0o644); !IsForbidden(err) {
		t.Fatalf("a write after the revoke = %v", err)
	}
	if _, err := os.Lstat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the refused write created a file")
	}
	if _, err := handle.ReadFile(); !IsForbidden(err) {
		t.Fatalf("a handle taken before the revoke still reads: %v", err)
	}
	if err := handle.WriteFile([]byte("x"), 0o644); !IsForbidden(err) {
		t.Fatalf("a handle taken before the revoke still writes: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(app, "src", "main.go")); err != nil || string(data) != "package main\n" {
		t.Fatalf("the revoke touched the files: %q, %v", data, err)
	}
	// Still browsable read-only, because it is under home.
	if listing, err := f.browser.List(app, MaxListing); err != nil || listing.Path != app {
		t.Fatalf("the revoked root cannot be browsed: %+v, %v", listing, err)
	}
}

func TestRevokingWhatIsNotARootIsTheSameAnswerWhetherOrNotItExists(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	f.authorize(f.in("code", "app"))
	var first *PathError
	for _, path := range []string{
		f.in("code", "lib"),
		f.in("missing"),
		f.in("code", "app", "src"),
		f.in("notes.txt"),
		filepath.Join(f.outside, "secret"),
		filepath.Join(f.outside, "missing"),
		f.home,
		"/",
	} {
		_, err := f.roots.Revoke(path)
		fault := refusedAs(t, err, KindNotFound, path)
		if first == nil {
			first = fault
		} else if fault.Message != first.Message {
			t.Errorf("revoking %s answered %q, want %q", path, fault.Message, first.Message)
		}
	}
	if len(f.roots.List()) != 1 {
		t.Fatalf("a refused revoke changed the roots")
	}
}

func TestARootCanBeRevokedByAnotherSpelling(t *testing.T) {
	t.Parallel()
	for name, spelling := range map[string]func(f *rootsFixture) string{
		"a trailing slash":         func(f *rootsFixture) string { return f.in("code", "app") + "/" },
		"the link it was given by": func(f *rootsFixture) string { return f.in("link") },
		"a dot segment":            func(f *rootsFixture) string { return f.in("code", ".", "app") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRootsFixture(t)
			mustSymlink(t, f.in("code", "app"), f.in("link"))
			f.authorize(f.in("link"))
			if remaining, err := f.roots.Revoke(spelling(f)); err != nil || len(remaining) != 0 {
				t.Fatalf("revoke = %v, %v", remaining, err)
			}
			if len(f.guard.Roots()) != 0 {
				t.Fatalf("the guard still holds %v", f.guard.Roots())
			}
		})
	}
}

func TestTheStoreIsWrittenAtomicallyWithItsOwnerOnly(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.authorize(f.in("code", "app"))
	info, err := os.Stat(f.store)
	if err != nil {
		t.Fatalf("stat the store: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the store has mode %o, want 600", mode)
	}
	if directory, err := os.Stat(filepath.Dir(f.store)); err != nil || directory.Mode().Perm() != 0o700 {
		t.Fatalf("the store's directory: %v, %v", directory, err)
	}
	raw, err := os.ReadFile(f.store)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("the store is not JSON: %v", err)
	}
	keys := make([]string, 0, len(document))
	for key := range document {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !reflect.DeepEqual(keys, []string{"roots", "version"}) {
		t.Fatalf("the store holds %v, want only roots and version", keys)
	}
	if !strings.Contains(string(raw), app) {
		t.Fatalf("the store does not hold the root: %s", raw)
	}
	// Nothing temporary is left beside it.
	entries, err := os.ReadDir(filepath.Dir(f.store))
	if err != nil || len(entries) != 1 {
		t.Fatalf("the store's directory holds %v (%v)", entries, err)
	}
}

func TestTheStoredRootsComeBackAfterARestart(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.authorize(f.in("code", "app"))
	lib := f.authorize(f.in("code", "lib"))
	f.open()
	want := []RootStatus{{Path: app, Available: true}, {Path: lib, Available: true}}
	if !reflect.DeepEqual(f.roots.List(), want) {
		t.Fatalf("after a restart the roots are %+v, want %+v", f.roots.List(), want)
	}
	if roots := f.guard.Roots(); !reflect.DeepEqual(roots, []string{app, lib}) {
		t.Fatalf("after a restart the guard holds %v", roots)
	}
	if _, err := f.guard.ReadFile(filepath.Join(app, "src", "main.go")); err != nil {
		t.Fatalf("a restored root cannot be read through: %v", err)
	}
	if f.log() != "" {
		t.Fatalf("a clean restart logged: %s", f.log())
	}
}

func TestAMissingStoreIsNoRootsAndWritesNothing(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	if len(f.roots.List()) != 0 || f.log() != "" {
		t.Fatalf("no store gave %v, logged %q", f.roots.List(), f.log())
	}
	if _, err := os.Lstat(filepath.Dir(f.store)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("starting with no store made its directory: %v", err)
	}
}

// Each stored root is checked again at startup, and one that no longer passes
// is listed with the reason rather than dropped, and can be revoked.
func TestAtStartupAStoredRootThatNoLongerPassesIsListedUnavailable(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	gone := f.in("gone")
	file := f.in("file-now")
	swapped := f.in("swap", "inner")
	locked := f.in("locked")
	app := f.in("code", "app")
	for _, directory := range []string{gone, file, swapped, locked} {
		mustMkdirAll(t, directory)
	}
	stored := []string{app, gone, file, swapped, locked}
	for _, path := range stored {
		f.authorize(path)
	}
	// What a hand edit, or an older agent, could leave behind.
	extra := []string{filepath.Join(f.outside, "secret"), f.home, filepath.Join(app, "src")}
	writeStore(t, f.store, append(append([]string{}, stored...), extra...))

	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustWriteFile(t, file, "now a file")
	if err := os.Rename(f.in("swap"), f.in("swap-real")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	mustSymlink(t, f.in("swap-real"), f.in("swap"))
	unreadable := os.Geteuid() != 0
	if unreadable {
		chmod(t, locked, 0o000)
	}

	f.open()
	want := []RootStatus{
		{Path: app, Available: true},
		{Path: gone, Reason: KindNotFound},
		{Path: file, Reason: KindNotDirectory},
		{Path: swapped, Reason: KindChanged},
		{Path: locked, Reason: KindUnreadable},
		{Path: extra[0], Reason: KindForbidden},
		{Path: extra[1], Reason: KindTooBroad},
		{Path: extra[2], Reason: KindOverlaps},
	}
	if !unreadable {
		want[4] = RootStatus{Path: locked, Available: true}
	}
	if got := f.roots.List(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after the restart:\n got %+v\nwant %+v", got, want)
	}
	wantGuard := []string{app}
	if !unreadable {
		wantGuard = append(wantGuard, locked)
	}
	if roots := f.guard.Roots(); !reflect.DeepEqual(roots, wantGuard) {
		t.Fatalf("the guard holds %v, want %v", roots, wantGuard)
	}
	// The swapped root reaches neither where it used to be nor where it
	// leads now.
	if _, err := f.guard.Resolve(f.in("swap-real", "inner")); !IsForbidden(err) {
		t.Fatalf("the swapped root's new target is authorized: %v", err)
	}
	// The log carries kinds, never a path.
	for _, path := range append(append([]string{}, stored...), extra...) {
		if strings.Contains(f.log(), path) {
			t.Fatalf("the log names %s:\n%s", path, f.log())
		}
	}
	if !strings.Contains(f.log(), "kind=changed") || !strings.Contains(f.log(), "kind=forbidden") {
		t.Fatalf("the log does not carry the kinds:\n%s", f.log())
	}

	// Revoking an unavailable root removes it from the store.
	for _, path := range []string{gone, swapped, extra[0], extra[1], extra[2]} {
		if _, err := f.roots.Revoke(path); err != nil {
			t.Fatalf("revoke the unavailable %s: %v", path, err)
		}
	}
	for _, path := range f.storedPaths() {
		if path == gone || path == swapped || path == extra[0] {
			t.Fatalf("revoked %s is still stored: %v", path, f.storedPaths())
		}
	}
	// A root that was unavailable and passes again can be authorized again,
	// without a second entry.
	if err := os.Remove(file); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustMkdirAll(t, file)
	f.authorize(file)
	count := 0
	for _, entry := range f.roots.List() {
		if entry.Path == file {
			count++
			if !entry.Available {
				t.Fatalf("re-authorized %s is still unavailable", file)
			}
		}
	}
	if count != 1 {
		t.Fatalf("%s is listed %d times", file, count)
	}
}

func writeStore(t *testing.T, path string, roots []string) {
	t.Helper()
	body, err := json.Marshal(storeDocument{Version: storeVersion, Roots: roots})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the store: %v", err)
	}
}

func TestACorruptStoreIsMovedAsideAndTheAgentStartsWithNoRoots(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"not json":         "{not json",
		"empty":            "",
		"a wrong version":  `{"version":2,"roots":[]}`,
		"no roots":         `{"version":1}`,
		"an unknown field": `{"version":1,"roots":[],"token":"x"}`,
		"a relative root":  `{"version":1,"roots":["code/app"]}`,
		"an unclean root":  `{"version":1,"roots":["/home/../etc"]}`,
		"a root not text":  `{"version":1,"roots":[1]}`,
		"trailing data":    `{"version":1,"roots":[]} {}`,
		"an array":         `[]`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newRootsFixture(t)
			mustMkdirAll(t, filepath.Dir(f.store))
			if err := os.WriteFile(f.store, []byte(content), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			f.open()
			if len(f.roots.List()) != 0 {
				t.Fatalf("a corrupt store gave %v", f.roots.List())
			}
			if count := strings.Count(f.log(), "level=WARN"); count != 1 || !strings.Contains(f.log(), "kind=corrupt") {
				t.Fatalf("want one warning with the kind, got:\n%s", f.log())
			}
			if strings.Contains(f.log(), f.base) {
				t.Fatalf("the log names a path:\n%s", f.log())
			}
			backups, err := filepath.Glob(f.store + ".corrupt-*")
			if err != nil || len(backups) != 1 {
				t.Fatalf("want one backup, got %v (%v)", backups, err)
			}
			if saved, err := os.ReadFile(backups[0]); err != nil || string(saved) != content {
				t.Fatalf("the backup holds %q, %v", saved, err)
			}
			// A new store is written beside the backup, which is kept.
			f.authorize(f.in("code", "app"))
			if saved, err := os.ReadFile(backups[0]); err != nil || string(saved) != content {
				t.Fatalf("writing the new store changed the backup: %q, %v", saved, err)
			}
			if got := f.storedPaths(); len(got) != 1 {
				t.Fatalf("the new store holds %v", got)
			}
			// A second corrupt store gets a backup of its own, never over
			// the first.
			if err := os.WriteFile(f.store, []byte(content+"again"), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			f.open()
			if backups, _ := filepath.Glob(f.store + ".corrupt-*"); len(backups) != 2 {
				t.Fatalf("want two backups, got %v", backups)
			}
		})
	}
}

func TestAnUnreadableStoreIsReportedAndNeverOverwritten(t *testing.T) {
	t.Parallel()
	skipIfPermissionsAreIgnored(t)
	f := newRootsFixture(t)
	app := f.authorize(f.in("code", "app"))
	chmod(t, f.store, 0o000)
	f.open()
	if len(f.roots.List()) != 0 || !strings.Contains(f.log(), "kind=unreadable") {
		t.Fatalf("an unreadable store gave %v, logged:\n%s", f.roots.List(), f.log())
	}
	backups, _ := filepath.Glob(f.store + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("want one backup, got %v", backups)
	}
	if err := os.Chmod(backups[0], 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if saved, err := os.ReadFile(backups[0]); err != nil || !strings.Contains(string(saved), app) {
		t.Fatalf("the backup holds %q, %v", saved, err)
	}
}

// The store cannot be moved aside when its directory cannot be written, and
// then nothing is written over it either: the authorization fails as io and
// changes nothing.
func TestAStoreThatCannotBeMovedAsideIsNotOverwritten(t *testing.T) {
	t.Parallel()
	skipIfPermissionsAreIgnored(t)
	f := newRootsFixture(t)
	mustMkdirAll(t, filepath.Dir(f.store))
	if err := os.WriteFile(f.store, []byte("{corrupt"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	chmod(t, filepath.Dir(f.store), 0o500)
	f.open()
	_, err := f.roots.Authorize(f.in("code", "app"))
	_ = refusedAs(t, err, KindIO, "authorize over an unmovable store")
	if len(f.roots.List()) != 0 || len(f.guard.Roots()) != 0 {
		t.Fatalf("a failed store write left a root: %v", f.roots.List())
	}
	if saved, err := os.ReadFile(f.store); err != nil || string(saved) != "{corrupt" {
		t.Fatalf("the corrupt store was overwritten: %q, %v", saved, err)
	}
}

// The directory the guard opens has to be the one that was checked. A
// directory swapped in between the two is refused, not authorized.
func TestADirectorySwappedBetweenTheCheckAndTheOpenIsRefused(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	app := f.in("code", "app")
	checked, err := f.roots.check("authorize", app)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := os.Rename(app, f.in("code", "app-old")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	mustMkdirAll(t, app)
	if err := f.roots.authorizeChecked(checked); KindOf(err) != KindChanged {
		t.Fatalf("authorizing a swapped directory = %v, want changed", err)
	}
	if len(f.guard.Roots()) != 0 {
		t.Fatalf("the swapped directory was authorized: %v", f.guard.Roots())
	}
}

func TestNoHomeMeansNothingCanBeAuthorized(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	f.openWithHome("")
	_, err := f.roots.Authorize(f.in("code", "app"))
	_ = refusedAs(t, err, KindForbidden, "no home")
}

func TestTheRootsHandOutNoHandle(t *testing.T) {
	t.Parallel()
	typ := reflect.TypeOf(&Roots{})
	var methods []string
	for index := 0; index < typ.NumMethod(); index++ {
		methods = append(methods, typ.Method(index).Name)
	}
	if want := []string{"Authorize", "List", "Revoke"}; !reflect.DeepEqual(methods, want) {
		t.Fatalf("Roots has the methods %v, want exactly %v", methods, want)
	}
	assertHoldsNoHandle(t, reflect.TypeOf(RootStatus{}))
}
