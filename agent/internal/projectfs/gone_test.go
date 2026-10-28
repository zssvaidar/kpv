// A stored root that is gone at the time it is asked about (#67): listed as
// unavailable with the reason, released from the guard at once, browsed as
// whatever is at its path now, and authorized again only by being asked to.

package projectfs

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// goneFixture is a rootsFixture with three roots authorized, app in the
// middle, so the order of the list can be seen not to change.
type goneFixture struct {
	*rootsFixture
	docs, app, music string
}

func newGoneFixture(t *testing.T) *goneFixture {
	t.Helper()
	f := &goneFixture{rootsFixture: newRootsFixture(t)}
	f.docs, f.app, f.music = f.in("docs"), f.in("code", "app"), f.in("music")
	mustMkdirAll(t, f.docs)
	mustMkdirAll(t, f.music)
	for _, root := range []string{f.docs, f.app, f.music} {
		f.authorize(root)
	}
	return f
}

// restoreMode puts a mode back once the test is done, so the temporary
// directory can be removed.
func restoreMode(t *testing.T, path string) {
	t.Helper()
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}

// warnings is the number of times the log says a root is no longer available.
func (f *rootsFixture) warnings() int {
	return strings.Count(f.log(), "a project root is no longer available")
}

// storeState is the store's bytes and the file they are in, to see it was
// neither changed nor rewritten.
type storeState struct {
	raw  []byte
	info os.FileInfo
}

func (f *rootsFixture) storeState() storeState {
	f.t.Helper()
	raw, err := os.ReadFile(f.store)
	if err != nil {
		f.t.Fatalf("read the store: %v", err)
	}
	info, err := os.Stat(f.store)
	if err != nil {
		f.t.Fatalf("stat the store: %v", err)
	}
	return storeState{raw, info}
}

func (f *rootsFixture) assertStoreUnchanged(before storeState) {
	f.t.Helper()
	after := f.storeState()
	if string(after.raw) != string(before.raw) || !os.SameFile(after.info, before.info) {
		f.t.Fatalf("the store changed: %s, then %s", before.raw, after.raw)
	}
}

// assertConsistent checks that the list, the guard and the store agree: one
// entry per root, every available root in the guard and nothing else there,
// and the store holding the listed paths in order.
func (f *rootsFixture) assertConsistent() {
	f.t.Helper()
	list := f.roots.List()
	listed, held := []string{}, []string{}
	seen := map[string]bool{}
	for _, entry := range list {
		if seen[entry.Path] {
			f.t.Fatalf("%s is listed twice: %+v", entry.Path, list)
		}
		seen[entry.Path] = true
		listed = append(listed, entry.Path)
		if entry.Available {
			held = append(held, entry.Path)
		}
	}
	guarded := f.guard.Roots()
	for _, path := range held {
		if !contains(guarded, path) {
			f.t.Fatalf("%s is listed available but the guard does not hold it: %v", path, guarded)
		}
	}
	if len(guarded) != len(held) {
		f.t.Fatalf("the guard holds %v, the list makes %v available", guarded, held)
	}
	if stored := f.storedPaths(); !reflect.DeepEqual(stored, listed) {
		f.t.Fatalf("stored %v, listed %v", stored, listed)
	}
}

func contains(paths []string, path string) bool {
	for _, candidate := range paths {
		if candidate == path {
			return true
		}
	}
	return false
}

func TestARootThatIsGoneIsListedUnavailableWithItsReason(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		reason ErrorKind
		change func(f *goneFixture)
	}{
		{"removed", KindNotFound, func(f *goneFixture) {
			if err := os.RemoveAll(f.app); err != nil {
				f.t.Fatalf("remove: %v", err)
			}
		}},
		{"replaced with a file", KindNotDirectory, func(f *goneFixture) {
			if err := os.RemoveAll(f.app); err != nil {
				f.t.Fatalf("remove: %v", err)
			}
			mustWriteFile(f.t, f.app, "a file")
		}},
		{"made unreadable", KindUnreadable, func(f *goneFixture) {
			restoreMode(f.t, f.app)
			if err := os.Chmod(f.app, 0o000); err != nil {
				f.t.Fatalf("chmod: %v", err)
			}
		}},
		{"moved away", KindNotFound, func(f *goneFixture) {
			if err := os.Rename(f.app, f.in("code", "app-old")); err != nil {
				f.t.Fatalf("move: %v", err)
			}
		}},
		{"removed and made again", KindChanged, func(f *goneFixture) {
			if err := os.RemoveAll(f.app); err != nil {
				f.t.Fatalf("remove: %v", err)
			}
			mustMkdirAll(f.t, filepath.Join(f.app, "src"))
		}},
		{"a component replaced by a link to another directory", KindChanged, func(f *goneFixture) {
			if err := os.Rename(f.in("code"), f.in("code-old")); err != nil {
				f.t.Fatalf("move: %v", err)
			}
			mustMkdirAll(f.t, f.in("elsewhere", "app", "src"))
			mustSymlink(f.t, f.in("elsewhere"), f.in("code"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			f := newGoneFixture(t)
			if test.reason == KindUnreadable && os.Geteuid() == 0 {
				t.Skip("root reads a mode 000 directory")
			}
			before := f.storeState()
			taken, err := f.guard.Resolve(filepath.Join(f.app, "src"))
			if err != nil {
				t.Fatalf("resolve inside the root: %v", err)
			}

			test.change(f)

			want := []RootStatus{
				{Path: f.docs, Available: true},
				{Path: f.app, Reason: test.reason},
				{Path: f.music, Available: true},
			}
			if got := f.roots.List(); !reflect.DeepEqual(got, want) {
				t.Fatalf("list = %+v, want %+v", got, want)
			}
			// Released from the guard: nothing is written, and a Path taken
			// before stops working.
			if got := f.guard.Roots(); !reflect.DeepEqual(got, []string{f.docs, f.music}) {
				t.Fatalf("the guard still holds %v", got)
			}
			created := filepath.Join(f.app, "src", "created.txt")
			refused(t, f.guard.WriteFile(created, []byte("x"), 0o644), "write under a root that is gone")
			if _, err := os.Lstat(created); err == nil {
				t.Fatalf("the refused write created %s", created)
			}
			if _, err := taken.Stat(); !IsForbidden(err) {
				t.Fatalf("a Path taken before still works: %v", err)
			}
			// The store keeps the entry, untouched.
			f.assertStoreUnchanged(before)
			f.assertConsistent()
			// One warning, by kind, when the state changed; none again.
			_ = f.roots.List()
			_, _ = f.browser.List(f.app, MaxListing)
			if got := f.warnings(); got != 1 {
				t.Fatalf("%d warnings, want 1:\n%s", got, f.log())
			}
			if !strings.Contains(f.log(), "kind="+string(test.reason)) {
				t.Fatalf("the warning does not carry the kind:\n%s", f.log())
			}
			if strings.Contains(f.log(), f.base) {
				t.Fatalf("the log carries a path:\n%s", f.log())
			}
			if test.name == "moved away" {
				for _, entry := range f.roots.List() {
					if strings.Contains(entry.Path, "app-old") {
						t.Fatalf("the moved directory is listed: %+v", entry)
					}
				}
			}
		})
	}
}

func TestARootThatIsStillTheSameDirectoryStaysAvailable(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	before := f.storeState()
	// Its contents change.
	mustWriteFile(t, filepath.Join(f.app, "added.txt"), "added")
	if err := os.RemoveAll(filepath.Join(f.app, "src")); err != nil {
		t.Fatalf("remove src: %v", err)
	}
	mustMkdirAll(t, filepath.Join(f.app, "lib"))
	// Its mode changes, and it can still be read and searched.
	restoreMode(t, f.app)
	for _, mode := range []os.FileMode{0o700, 0o500, 0o555} {
		if err := os.Chmod(f.app, mode); err != nil {
			t.Fatalf("chmod %o: %v", mode, err)
		}
		want := []RootStatus{{Path: f.docs, Available: true}, {Path: f.app, Available: true}, {Path: f.music, Available: true}}
		if got := f.roots.List(); !reflect.DeepEqual(got, want) {
			t.Fatalf("mode %o: list = %+v", mode, got)
		}
		listing, err := f.browser.List(f.app, MaxListing)
		if err != nil || len(listing.Entries) != 2 {
			t.Fatalf("mode %o: browsing the root gave %+v, %v", mode, listing, err)
		}
	}
	if got := f.guard.Roots(); !reflect.DeepEqual(got, []string{f.docs, f.app, f.music}) {
		t.Fatalf("the guard holds %v", got)
	}
	if again := f.authorize(f.app); again != f.app {
		t.Fatalf("authorizing it again answered %s", again)
	}
	if f.warnings() != 0 {
		t.Fatalf("a root that is still there was warned about:\n%s", f.log())
	}
	f.assertStoreUnchanged(before)
}

func TestBrowsingARecreatedRootListsWhatIsThereNowWithNoListFirst(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	if err := os.RemoveAll(f.app); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustMkdirAll(t, f.app)
	mustWriteFile(t, filepath.Join(f.app, "new"), "")

	// No List first: the browse itself checks the root it lands in.
	listing, err := f.browser.List(f.app, MaxListing)
	if err != nil {
		t.Fatalf("browse the recreated root: %v", err)
	}
	if listing.Path != f.app || len(listing.Entries) != 1 || listing.Entries[0].Name != "new" {
		t.Fatalf("browsing gave %+v", listing)
	}
	// Through the home allowance, read-only: the guard does not hold it.
	if contains(f.guard.Roots(), f.app) {
		t.Fatalf("the guard still holds the old root")
	}
	refused(t, f.guard.WriteFile(filepath.Join(f.app, "x"), []byte("x"), 0o644), "write into the recreated directory")
	if want := (RootStatus{Path: f.app, Reason: KindChanged}); f.roots.List()[1] != want {
		t.Fatalf("listed %+v", f.roots.List()[1])
	}
	if f.warnings() != 1 {
		t.Fatalf("want one warning:\n%s", f.log())
	}
}

func TestBrowsingAMovedRootNeverListsWhereItWentTo(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	if err := os.Rename(f.app, f.in("code", "app-old")); err != nil {
		t.Fatalf("move: %v", err)
	}
	// Below the old root, with nothing there now: not found, never the
	// contents of the moved directory.
	if _, err := f.browser.List(filepath.Join(f.app, "src"), MaxListing); !IsNotFound(err) {
		t.Fatalf("browsing below a moved root: %v", err)
	}
	mustMkdirAll(t, f.app)
	listing, err := f.browser.List(f.app, MaxListing)
	if err != nil {
		t.Fatalf("browse the new directory: %v", err)
	}
	if len(listing.Entries) != 0 {
		t.Fatalf("browsing the new, empty directory listed %+v", listing.Entries)
	}
}

func TestARootThatIsGoneIsNotAuthorizedAgainByLookingAtIt(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	if err := os.RemoveAll(f.app); err != nil {
		t.Fatalf("remove: %v", err)
	}
	_ = f.roots.List()
	mustMkdirAll(t, f.app)
	for range 2 {
		if _, err := f.browser.List(f.app, MaxListing); err != nil {
			t.Fatalf("browse: %v", err)
		}
		if got := f.roots.List()[1]; got.Available || got.Reason != KindNotFound {
			t.Fatalf("looking at it changed it: %+v", got)
		}
	}
	if contains(f.guard.Roots(), f.app) {
		t.Fatalf("looking at it authorized it")
	}
	// The next startup's check is unchanged, and passes it.
	f.open()
	if got := f.roots.List()[1]; got != (RootStatus{Path: f.app, Available: true}) {
		t.Fatalf("after a restart: %+v", got)
	}
}

func TestAnUnavailableRootIsAuthorizedAgainInItsPlace(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		reason ErrorKind
		gone   func(f *goneFixture)
		back   func(f *goneFixture)
	}{
		{"removed, then made again", KindNotFound, func(f *goneFixture) {
			if err := os.RemoveAll(f.app); err != nil {
				f.t.Fatalf("remove: %v", err)
			}
		}, func(f *goneFixture) { mustMkdirAll(f.t, f.app) }},
		{"made unreadable, then readable", KindUnreadable, func(f *goneFixture) {
			restoreMode(f.t, f.app)
			if err := os.Chmod(f.app, 0o000); err != nil {
				f.t.Fatalf("chmod: %v", err)
			}
		}, func(f *goneFixture) {
			if err := os.Chmod(f.app, 0o755); err != nil {
				f.t.Fatalf("chmod: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if test.reason == KindUnreadable && os.Geteuid() == 0 {
				t.Skip("root reads a mode 000 directory")
			}
			f := newGoneFixture(t)
			before := f.storeState()
			test.gone(f)
			if got := f.roots.List()[1]; got.Reason != test.reason {
				t.Fatalf("listed %+v, want %s", got, test.reason)
			}
			test.back(f)
			if got := f.roots.List()[1]; got.Available {
				t.Fatalf("it came back by itself: %+v", got)
			}
			if realDir := f.authorize(f.app + "/"); realDir != f.app {
				t.Fatalf("authorized %s", realDir)
			}
			want := []RootStatus{{Path: f.docs, Available: true}, {Path: f.app, Available: true}, {Path: f.music, Available: true}}
			if got := f.roots.List(); !reflect.DeepEqual(got, want) {
				t.Fatalf("list = %+v", got)
			}
			f.assertStoreUnchanged(before)
			f.assertConsistent()
			// It is the directory there now that is held.
			if err := f.guard.WriteFile(filepath.Join(f.app, "created.txt"), []byte("x"), 0o644); err != nil {
				t.Fatalf("write into the authorized directory: %v", err)
			}
			if _, err := os.Stat(filepath.Join(f.app, "created.txt")); err != nil {
				t.Fatalf("the write did not land in the directory there now: %v", err)
			}
		})
	}
}

func TestAuthorizingARootStillListedAvailableChecksItFirst(t *testing.T) {
	t.Parallel()
	t.Run("gone, and nothing there", func(t *testing.T) {
		t.Parallel()
		f := newGoneFixture(t)
		if err := os.RemoveAll(f.app); err != nil {
			t.Fatalf("remove: %v", err)
		}
		_, err := f.roots.Authorize(f.app)
		_ = refusedAs(t, err, KindNotFound, "authorize a root that is gone")
		if got := f.roots.List()[1]; got != (RootStatus{Path: f.app, Reason: KindNotFound}) {
			t.Fatalf("listed %+v", got)
		}
		if contains(f.guard.Roots(), f.app) {
			t.Fatalf("the guard still holds it")
		}
		f.assertConsistent()
	})
	t.Run("gone, and another directory there", func(t *testing.T) {
		t.Parallel()
		f := newGoneFixture(t)
		before := f.storeState()
		if err := os.RemoveAll(f.app); err != nil {
			t.Fatalf("remove: %v", err)
		}
		mustMkdirAll(t, f.app)
		if realDir := f.authorize(f.app); realDir != f.app {
			t.Fatalf("authorized %s", realDir)
		}
		held, ok := f.guard.heldInfo(f.app)
		now, err := os.Stat(f.app)
		if !ok || err != nil || !os.SameFile(held, now) {
			t.Fatalf("the guard does not hold the directory there now")
		}
		f.assertStoreUnchanged(before)
		f.assertConsistent()
		if f.warnings() != 1 {
			t.Fatalf("want one warning:\n%s", f.log())
		}
	})
}

func TestAnUnavailableRootIsRevokedByItsListedPath(t *testing.T) {
	t.Parallel()
	for _, recreate := range []bool{false, true} {
		f := newGoneFixture(t)
		if err := os.RemoveAll(f.app); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if recreate {
			mustMkdirAll(t, f.app)
		}
		_ = f.roots.List()
		remaining, err := f.roots.Revoke(f.app)
		if err != nil {
			t.Fatalf("recreated %v: revoke: %v", recreate, err)
		}
		want := []RootStatus{{Path: f.docs, Available: true}, {Path: f.music, Available: true}}
		if !reflect.DeepEqual(remaining, want) {
			t.Fatalf("recreated %v: remaining %+v", recreate, remaining)
		}
		if got := f.storedPaths(); !reflect.DeepEqual(got, []string{f.docs, f.music}) {
			t.Fatalf("recreated %v: stored %v", recreate, got)
		}
		f.assertConsistent()
	}
}

func TestARootThatBecameUnavailableStillBlocksAnOverlap(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	if err := os.RemoveAll(f.app); err != nil {
		t.Fatalf("remove: %v", err)
	}
	mustMkdirAll(t, filepath.Join(f.app, "src"))
	if got := f.roots.List()[1]; got.Reason != KindChanged {
		t.Fatalf("listed %+v", got)
	}
	_, err := f.roots.Authorize(filepath.Join(f.app, "src"))
	if fault := refusedAs(t, err, KindOverlaps, "authorize inside an unavailable root"); fault.Root != f.app {
		t.Fatalf("the overlap names %s", fault.Root)
	}
	_, err = f.roots.Authorize(f.in("code"))
	_ = refusedAs(t, err, KindOverlaps, "authorize around an unavailable root")
}

// Lists, authorizations, revokes and browses race a root being removed and
// made again. Whatever order they land in, the list, the guard and the store
// agree afterwards.
func TestConcurrentRequestsWhileARootIsRemovedStayConsistent(t *testing.T) {
	t.Parallel()
	f := newGoneFixture(t)
	const rounds = 40
	var wait sync.WaitGroup
	wait.Add(1)
	go func() {
		defer wait.Done()
		for range rounds {
			_ = os.RemoveAll(f.app)
			_ = os.MkdirAll(filepath.Join(f.app, "src"), 0o755)
		}
	}()
	for worker := range 4 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for round := range rounds {
				switch (worker + round) % 4 {
				case 0:
					_ = f.roots.List()
				case 1:
					_, _ = f.roots.Authorize(f.app)
				case 2:
					_, _ = f.browser.List(filepath.Join(f.app, "src"), MaxListing)
				case 3:
					if round%8 == 3 {
						_, _ = f.roots.Revoke(f.app)
					} else {
						_, _ = f.browser.List(f.app, MaxListing)
					}
				}
			}
		}()
	}
	wait.Wait()
	f.assertConsistent()
	mustMkdirAll(t, f.app)
	if _, err := f.roots.Authorize(f.app); err != nil {
		t.Fatalf("authorize after the race: %v", err)
	}
	f.assertConsistent()
	if strings.Contains(f.log(), f.base) {
		t.Fatalf("the log carries a path:\n%s", f.log())
	}
}
