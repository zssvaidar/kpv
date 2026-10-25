package projectfs

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// browseFixture is a real tree for browsing:
//
//	base/
//	  home/                 the user's home directory, browsable, not authorized
//	    code/app/           a project inside home, with .git and go.mod
//	    notes.txt
//	  project/              an authorized root, outside home
//	    src/
//	  outside/secret/       neither authorized nor home
//	  outside/secret.txt
type browseFixture struct {
	t       *testing.T
	base    string
	home    string
	project string
	outside string
	guard   *Guard
	browser *Browser
}

func newBrowseFixture(t *testing.T) *browseFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	f := &browseFixture{
		t: t, base: base,
		home:    filepath.Join(base, "home"),
		project: filepath.Join(base, "project"),
		outside: filepath.Join(base, "outside"),
	}
	for _, directory := range []string{
		filepath.Join(f.home, "code", "app", ".git"),
		filepath.Join(f.project, "src"),
		filepath.Join(f.outside, "secret"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("make %s: %v", directory, err)
		}
	}
	f.write(filepath.Join(f.home, "notes.txt"))
	f.write(filepath.Join(f.home, "code", "app", "go.mod"))
	f.write(filepath.Join(f.outside, "secret.txt"))

	guard, err := New(f.project)
	if err != nil {
		t.Fatalf("authorize the project: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	browser, err := NewBrowser(guard, f.home)
	if err != nil {
		t.Fatalf("build the browser: %v", err)
	}
	t.Cleanup(func() { _ = browser.Close() })
	f.guard, f.browser = guard, browser
	return f
}

func (f *browseFixture) write(path string) {
	f.t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", path, err)
	}
}

func (f *browseFixture) link(target, at string) {
	f.t.Helper()
	if err := os.Symlink(target, at); err != nil {
		f.t.Fatalf("link %s -> %s: %v", at, target, err)
	}
}

func (f *browseFixture) list(path string) Listing {
	f.t.Helper()
	listing, err := f.browser.List(path, MaxListing)
	if err != nil {
		f.t.Fatalf("list %s: %v", path, err)
	}
	return listing
}

func entryNamed(t *testing.T, listing Listing, name string) Entry {
	t.Helper()
	for _, entry := range listing.Entries {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("%s has no entry %q: %+v", listing.Path, name, listing.Entries)
	return Entry{}
}

// skipIfPermissionsAreIgnored skips a test that needs a permission to be
// refused, when the process is one the kernel refuses nothing.
func skipIfPermissionsAreIgnored(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which every permission check lets through")
	}
}

// chmod sets a mode for the rest of the test and puts a usable one back
// afterwards, so t.TempDir can clean up.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

func TestAListingHasTheSubdirectoriesAndTellsFilesFromDirectories(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	listing := f.list(f.home)

	if listing.Path != f.home {
		t.Fatalf("listed %s, want %s", listing.Path, f.home)
	}
	if got := len(listing.Entries); got != 2 {
		t.Fatalf("want 2 entries, got %+v", listing.Entries)
	}
	code := entryNamed(t, listing, "code")
	if code.Kind != EntryDirectory || code.Unreadable || code.LinkTarget != "" || code.Path != filepath.Join(f.home, "code") {
		t.Errorf("code is %+v, want a readable directory", code)
	}
	notes := entryNamed(t, listing, "notes.txt")
	if notes.Kind != EntryFile || notes.Unreadable {
		t.Errorf("notes.txt is %+v, want a readable file", notes)
	}
	if listing.Truncated || listing.Limit != MaxListing {
		t.Errorf("a two-entry listing is truncated=%v limit=%d", listing.Truncated, listing.Limit)
	}
}

func TestEntriesAreSortedByName(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	for _, name := range []string{"zeta", "Alpha", "beta", "10", "9"} {
		f.write(filepath.Join(f.project, name))
	}
	listing := f.list(f.project)
	names := make([]string, 0, len(listing.Entries))
	for _, entry := range listing.Entries {
		names = append(names, entry.Name)
	}
	if !sort.StringsAreSorted(names) {
		t.Fatalf("entries are not sorted: %v", names)
	}
}

func TestSymlinksAreMarkedAndJudgedByWhereTheyLead(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	f.link(filepath.Join(f.home, "code"), filepath.Join(f.home, "to-code"))
	f.link("notes.txt", filepath.Join(f.home, "to-notes"))
	f.link(filepath.Join(f.outside, "secret"), filepath.Join(f.home, "to-outside-directory"))
	f.link(filepath.Join(f.outside, "nothing-here"), filepath.Join(f.home, "to-outside-missing"))
	f.link("missing", filepath.Join(f.home, "dangling"))
	f.link("loop-b", filepath.Join(f.home, "loop-a"))
	f.link("loop-a", filepath.Join(f.home, "loop-b"))
	f.link(filepath.Join(f.project, "src"), filepath.Join(f.home, "to-project"))

	listing := f.list(f.home)
	for name, want := range map[string]LinkTarget{
		"to-code":              LinkToDirectory,
		"to-notes":             LinkToFile,
		"to-outside-directory": LinkOutside,
		"to-outside-missing":   LinkOutside,
		"dangling":             LinkBroken,
		"loop-a":               LinkBroken,
		"loop-b":               LinkBroken,
		"to-project":           LinkToDirectory,
	} {
		entry := entryNamed(t, listing, name)
		if entry.Kind != EntrySymlink {
			t.Errorf("%s is %s, want symlink: a link is reported as a link", name, entry.Kind)
		}
		if entry.LinkTarget != want {
			t.Errorf("%s leads to %q, want %q", name, entry.LinkTarget, want)
		}
		if entry.Path != filepath.Join(f.home, name) {
			t.Errorf("%s has path %s, want the link's own path", name, entry.Path)
		}
	}
	// An outside link says nothing about what is out there: a directory and
	// nothing at all are the same answer.
	if a, b := entryNamed(t, listing, "to-outside-directory"), entryNamed(t, listing, "to-outside-missing"); a.Unreadable != b.Unreadable || a.LinkTarget != b.LinkTarget || a.Kind != b.Kind {
		t.Errorf("two outside links differ: %+v and %+v", a, b)
	}
}

// The constraint: a symbolic link out of the browsable directories is never
// followed to enumerate what it leads to.
func TestALinkOutOfTheBrowsableDirectoriesIsNeverListed(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	f.link(filepath.Join(f.outside, "secret"), filepath.Join(f.home, "out"))
	f.link(f.outside, filepath.Join(f.project, "out"))
	f.link("..", filepath.Join(f.home, "up"))

	for _, path := range []string{
		filepath.Join(f.home, "out"),
		filepath.Join(f.project, "out"),
		filepath.Join(f.project, "out", "secret"),
		filepath.Join(f.home, "up"),
		filepath.Join(f.home, "up", "outside"),
	} {
		_, err := f.browser.List(path, MaxListing)
		if !IsForbidden(err) {
			t.Errorf("listing %s gave %v, want a forbidden refusal", path, err)
		}
	}
}

func TestTheHomeDirectoryIsBrowsableWithoutBeingAuthorized(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	for _, root := range f.guard.Roots() {
		if root == f.home {
			t.Fatal("the fixture authorized the home directory, so this test proves nothing")
		}
	}
	if f.browser.Home() != f.home {
		t.Fatalf("the browser's home is %q, want %q", f.browser.Home(), f.home)
	}

	f.list(f.home)
	f.list(filepath.Join(f.home, "code", "app"))
	f.list(f.project)
	f.list(filepath.Join(f.project, "src"))

	for _, path := range []string{f.base, f.outside, filepath.Join(f.outside, "secret"), "/"} {
		if _, err := f.browser.List(path, MaxListing); !IsForbidden(err) {
			t.Errorf("listing %s gave %v, want forbidden: only the roots and home are browsable", path, err)
		}
	}
}

// The home allowance is for looking. Every write path in the agent goes
// through the authorized Guard, and that guard does not have home in it.
func TestTheHomeAllowanceWidensNothingThatWrites(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	target := filepath.Join(f.home, "written.txt")
	if err := f.guard.WriteFile(target, []byte("x"), 0o644); !IsForbidden(err) {
		t.Fatalf("writing into home through the authorized guard gave %v, want forbidden", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("a file was created in home: %v", err)
	}
	if _, err := f.guard.Resolve(f.home); !IsForbidden(err) {
		t.Fatalf("home resolves through the authorized guard: %v", err)
	}

	// And a Browser can do nothing but look: its whole exported surface is
	// these three methods, and what List returns holds no Path.
	var methods []string
	browserType := reflect.TypeOf(&Browser{})
	for index := 0; index < browserType.NumMethod(); index++ {
		methods = append(methods, browserType.Method(index).Name)
	}
	if want := []string{"Close", "Home", "List"}; !reflect.DeepEqual(methods, want) {
		t.Fatalf("Browser has methods %v, want only %v; anything more is a way to reach the home directory other than looking", methods, want)
	}
	assertHoldsNoHandle(t, reflect.TypeOf(Listing{}))
}

// assertHoldsNoHandle fails if a type holds, anywhere inside it, a Path, a
// Guard or anything else that is not plain data.
func assertHoldsNoHandle(t *testing.T, typ reflect.Type) {
	t.Helper()
	switch typ.Kind() {
	case reflect.String, reflect.Bool, reflect.Int:
	case reflect.Slice:
		assertHoldsNoHandle(t, typ.Elem())
	case reflect.Struct:
		if typ == reflect.TypeOf(Path{}) || typ == reflect.TypeOf(Guard{}) {
			t.Fatalf("a listing holds a %s", typ)
		}
		for index := 0; index < typ.NumField(); index++ {
			assertHoldsNoHandle(t, typ.Field(index).Type)
		}
	default:
		t.Fatalf("a listing holds a %s, which is not plain data", typ)
	}
}

func TestARootAuthorizedLaterIsBrowsableAtOnce(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	if _, err := f.browser.List(f.outside, MaxListing); !IsForbidden(err) {
		t.Fatalf("outside was browsable before it was authorized: %v", err)
	}
	if _, err := f.guard.Authorize(f.outside); err != nil {
		t.Fatalf("authorize outside: %v", err)
	}
	f.list(f.outside)
}

func TestNoHomeMeansOnlyTheAuthorizedRoots(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	browser, err := NewBrowser(f.guard, "")
	if err != nil {
		t.Fatalf("build a browser with no home: %v", err)
	}
	if browser.Home() != "" {
		t.Fatalf("a browser with no home reports %q", browser.Home())
	}
	if _, err := browser.List(f.home, MaxListing); !IsForbidden(err) {
		t.Fatalf("home was browsable with no home given: %v", err)
	}
	if _, err := browser.List(f.project, MaxListing); err != nil {
		t.Fatalf("the authorized root was not browsable: %v", err)
	}
	if _, err := NewBrowser(nil, f.home); KindOf(err) != KindInvalid {
		t.Fatalf("a browser with no guard was built: %v", err)
	}
	if _, err := NewBrowser(f.guard, filepath.Join(f.base, "no-such-home")); !IsNotFound(err) {
		t.Fatalf("a missing home gave %v, want notFound", err)
	}
}

func TestAMissingDirectoryIsNotFoundAndAnOutsideOneIsForbiddenWhetherOrNotItExists(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	for _, path := range []string{
		filepath.Join(f.home, "no-such-directory"),
		filepath.Join(f.home, "code", "no-such", "deeper"),
		filepath.Join(f.project, "no-such-directory"),
	} {
		if _, err := f.browser.List(path, MaxListing); !IsNotFound(err) {
			t.Errorf("listing %s gave %v, want notFound", path, err)
		}
	}
	for _, path := range []string{
		filepath.Join(f.outside, "secret"),
		filepath.Join(f.outside, "secret.txt"),
		filepath.Join(f.outside, "no-such-directory"),
		filepath.Join(f.outside, "secret.txt", "below-a-file"),
		filepath.Join(f.home, "..", "outside", "secret"),
		filepath.Join(f.home) + "/../outside/no-such-directory",
		f.home + "/../home/code",
	} {
		_, err := f.browser.List(path, MaxListing)
		if !IsForbidden(err) {
			t.Errorf("listing %s gave %v, want forbidden", path, err)
		}
		if IsNotFound(err) {
			t.Errorf("listing %s reads as missing, which says something about outside", path)
		}
	}
}

func TestAPathThatIsNotADirectoryOrNotAbsoluteOrBadlyLimitedIsRefused(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	if _, err := f.browser.List(filepath.Join(f.home, "notes.txt"), MaxListing); KindOf(err) != KindNotDirectory {
		t.Errorf("listing a file gave %v, want notDirectory", err)
	}
	for _, path := range []string{"", "home", "./home", "../home", "with\x00nul"} {
		if _, err := f.browser.List(path, MaxListing); KindOf(err) != KindInvalid {
			t.Errorf("listing %q gave %v, want invalid", path, err)
		}
	}
	for _, limit := range []int{-1, 0, MaxListing + 1} {
		if _, err := f.browser.List(f.home, limit); KindOf(err) != KindInvalid {
			t.Errorf("a limit of %d gave %v, want invalid", limit, err)
		}
	}
}

func TestAProjectIsReportedWithItsEvidence(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	app := f.list(filepath.Join(f.home, "code", "app"))
	if !app.LooksLikeProject {
		t.Fatalf("a directory with .git and go.mod does not look like a project: %+v", app)
	}
	want := []Evidence{{".git", "a Git repository"}, {"go.mod", "a Go module"}}
	if !reflect.DeepEqual(app.Evidence, want) {
		t.Fatalf("evidence is %+v, want %+v", app.Evidence, want)
	}

	code := f.list(filepath.Join(f.home, "code"))
	if code.LooksLikeProject || code.Evidence == nil || len(code.Evidence) != 0 {
		t.Fatalf("a directory with no marker looks like a project, or its evidence is not an empty list: %+v", code)
	}

	// Every marker counts, and a marker is noticed even when the listing is
	// truncated before reaching it.
	every := filepath.Join(f.project, "every")
	if err := os.Mkdir(every, 0o755); err != nil {
		t.Fatalf("make %s: %v", every, err)
	}
	for _, marker := range projectMarkers {
		f.write(filepath.Join(every, marker.Marker))
	}
	listing, err := f.browser.List(every, 1)
	if err != nil {
		t.Fatalf("list %s: %v", every, err)
	}
	if !listing.Truncated || !reflect.DeepEqual(listing.Evidence, projectMarkers) {
		t.Fatalf("with every marker present and a page of one, got truncated=%v evidence=%+v", listing.Truncated, listing.Evidence)
	}
}

func TestTheParentIsGivenOnlyWhenItIsBrowsable(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	if home := f.list(f.home); home.Parent != "" {
		t.Errorf("the home directory has the parent %q, which is not browsable", home.Parent)
	}
	if root := f.list(f.project); root.Parent != "" {
		t.Errorf("the authorized root has the parent %q, which is not browsable", root.Parent)
	}
	if code := f.list(filepath.Join(f.home, "code")); code.Parent != f.home {
		t.Errorf("home/code has the parent %q, want %q", code.Parent, f.home)
	}
}

// A very large directory: a capped page, the truncation marked, and the work
// bounded by the page rather than by the directory.
func TestAVeryLargeDirectoryReturnsACappedPage(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	large := filepath.Join(f.project, "large")
	if err := os.Mkdir(large, 0o755); err != nil {
		t.Fatalf("make %s: %v", large, err)
	}
	const total = 20000
	for index := 0; index < total; index++ {
		name := filepath.Join(large, fmt.Sprintf("entry-%05d", index))
		if index%10 == 0 {
			if err := os.Mkdir(name, 0o755); err != nil {
				t.Fatalf("make %s: %v", name, err)
			}
			continue
		}
		if err := os.WriteFile(name, nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	listing := f.list(large)
	if len(listing.Entries) != MaxListing || !listing.Truncated || listing.Limit != MaxListing {
		t.Fatalf("a directory of %d gave %d entries, truncated=%v, limit=%d; want a page of %d, truncated", total, len(listing.Entries), listing.Truncated, listing.Limit, MaxListing)
	}
	seen := map[string]bool{}
	for _, entry := range listing.Entries {
		if seen[entry.Name] {
			t.Fatalf("%s is in the page twice", entry.Name)
		}
		seen[entry.Name] = true
	}

	small, err := f.browser.List(large, 25)
	if err != nil {
		t.Fatalf("list with a limit of 25: %v", err)
	}
	if len(small.Entries) != 25 || !small.Truncated || small.Limit != 25 {
		t.Fatalf("a limit of 25 gave %d entries, truncated=%v, limit=%d", len(small.Entries), small.Truncated, small.Limit)
	}
}

// The edge of the cap: exactly a page is not truncated, one more is.
func TestTruncationIsMarkedExactlyWhenThereIsMore(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	directory := filepath.Join(f.project, "five")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatalf("make %s: %v", directory, err)
	}
	for index := 0; index < 5; index++ {
		f.write(filepath.Join(directory, fmt.Sprintf("f%d", index)))
	}
	for limit, want := range map[int]bool{4: true, 5: false, 6: false} {
		listing, err := f.browser.List(directory, limit)
		if err != nil {
			t.Fatalf("list with a limit of %d: %v", limit, err)
		}
		if listing.Truncated != want {
			t.Errorf("five entries with a limit of %d: truncated=%v, want %v", limit, listing.Truncated, want)
		}
		if wantLen := min(limit, 5); len(listing.Entries) != wantLen {
			t.Errorf("five entries with a limit of %d: %d entries, want %d", limit, len(listing.Entries), wantLen)
		}
	}
}

// An entry the process cannot read is listed, by name, as unreadable.
func TestAnUnreadableEntryIsListedAndMarked(t *testing.T) {
	t.Parallel()
	skipIfPermissionsAreIgnored(t)
	f := newBrowseFixture(t)
	locked := filepath.Join(f.home, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatalf("make %s: %v", locked, err)
	}
	private := filepath.Join(f.home, "private.txt")
	f.write(private)
	f.link("locked", filepath.Join(f.home, "to-locked"))
	chmod(t, locked, 0o000)
	chmod(t, private, 0o000)

	listing := f.list(f.home)
	for name, kind := range map[string]EntryKind{"locked": EntryDirectory, "private.txt": EntryFile, "to-locked": EntrySymlink} {
		entry := entryNamed(t, listing, name)
		if !entry.Unreadable || entry.Kind != kind {
			t.Errorf("%s is %+v, want an unreadable %s", name, entry, kind)
		}
	}
	if entry := entryNamed(t, listing, "notes.txt"); entry.Unreadable {
		t.Errorf("a readable file is marked unreadable: %+v", entry)
	}
}

// A directory whose names can be read but whose entries cannot be looked at
// (read permission without search permission): every entry is still listed,
// by name, marked unreadable.
func TestEntriesThatCannotBeDescribedAreListedByName(t *testing.T) {
	t.Parallel()
	skipIfPermissionsAreIgnored(t)
	f := newBrowseFixture(t)
	opaque := filepath.Join(f.home, "opaque")
	if err := os.MkdirAll(filepath.Join(opaque, "inner"), 0o755); err != nil {
		t.Fatalf("make %s: %v", opaque, err)
	}
	f.write(filepath.Join(opaque, "file.txt"))
	chmod(t, opaque, 0o444)

	listing := f.list(opaque)
	if len(listing.Entries) != 2 {
		t.Fatalf("want both entries listed, got %+v", listing.Entries)
	}
	for _, name := range []string{"file.txt", "inner"} {
		entry := entryNamed(t, listing, name)
		if !entry.Unreadable || entry.Kind != EntryUnknown {
			t.Errorf("%s is %+v, want unknown and unreadable", name, entry)
		}
	}
}

func TestAnUnreadableDirectoryIsReportedAsUnreadable(t *testing.T) {
	t.Parallel()
	skipIfPermissionsAreIgnored(t)
	f := newBrowseFixture(t)
	locked := filepath.Join(f.home, "locked")
	if err := os.MkdirAll(filepath.Join(locked, "inside"), 0o755); err != nil {
		t.Fatalf("make %s: %v", locked, err)
	}
	chmod(t, locked, 0o000)

	for _, path := range []string{locked, filepath.Join(locked, "inside")} {
		_, err := f.browser.List(path, MaxListing)
		if KindOf(err) != KindUnreadable {
			t.Errorf("listing %s gave %v, want unreadable", path, err)
		}
		if IsForbidden(err) || IsNotFound(err) {
			t.Errorf("listing %s reads as forbidden or missing: %v", path, err)
		}
	}
}
