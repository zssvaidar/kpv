package projectfs

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pinOf(t *testing.T, p Path) *PinnedDir {
	t.Helper()
	pinned, err := p.Pin()
	if err != nil {
		t.Fatalf("pin %s: %v", p, err)
	}
	t.Cleanup(func() { _ = pinned.Close() })
	return pinned
}

// pinRefused checks that err is a *PathError of kind from Pin, and returns it.
func pinRefused(t *testing.T, err error, kind ErrorKind, what string) *PathError {
	t.Helper()
	var fault *PathError
	if !errors.As(err, &fault) || fault.Kind != kind {
		t.Fatalf("%s: want a %s refusal, got %v", what, kind, err)
	}
	if fault.Message == "" {
		t.Fatalf("%s: a refusal with no message", what)
	}
	return fault
}

func writeThrough(p Path, content string) error {
	return p.WriteAtomically(0o644, func(w io.Writer) error {
		_, err := io.WriteString(w, content)
		return err
	})
}

func fileContent(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// listing is every name under dir with its content or link target, so a test
// can show a directory is byte-for-byte what it was.
func listing(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		entry := path + " " + info.Mode().String()
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			entry += " -> " + target
		case info.Mode().IsRegular():
			data, _ := os.ReadFile(path)
			entry += " " + string(data)
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return strings.Join(out, "\n")
}

func TestAPinnedPathAnswersWhatItWouldWithoutThePin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	given := f.in("inside", "deep")
	resolved := allowed(t, f.guard, given, given)
	pinned := pinOf(t, resolved)
	dir := pinned.Path()
	if dir.Real() != resolved.Real() || dir.Given() != resolved.Given() || dir.Root() != f.root || dir.Rel() != resolved.Rel() || dir.IsRoot() {
		t.Fatalf("pinned %q %q %q %q %v, want %q %q %q %q false",
			dir.Real(), dir.Given(), dir.Root(), dir.Rel(), dir.IsRoot(), resolved.Real(), resolved.Given(), f.root, resolved.Rel())
	}
	joined, err := dir.Join("x.txt")
	if err != nil {
		t.Fatal(err)
	}
	if joined.Real() != f.in("inside", "deep", "x.txt") || joined.Root() != f.root || joined.Rel() != filepath.Join("inside", "deep", "x.txt") || joined.IsRoot() {
		t.Fatalf("joined %q %q %q %v", joined.Real(), joined.Root(), joined.Rel(), joined.IsRoot())
	}

	root := pinOf(t, allowed(t, f.guard, f.root, f.root))
	if !root.Path().IsRoot() || root.Path().Real() != f.root || root.Path().Rel() != "." {
		t.Fatalf("the pinned root: IsRoot %v Real %q Rel %q", root.Path().IsRoot(), root.Path().Real(), root.Path().Rel())
	}
	if err := writeThrough(root.Path(), "x"); KindOf(err) != KindNotDirectory {
		t.Fatalf("writing over a pinned root = %v", err)
	}
	if err := writeThrough(dir, "x"); KindOf(err) != KindNotDirectory {
		t.Fatalf("writing over a pinned directory = %v", err)
	}
}

// The case Pin exists for: the directory renamed away and a link to an
// in-root sibling put where it was. A write through the pin lands in the
// original directory, and the sibling is untouched.
func TestAPinnedDirectoryReplacedByALinkKeepsReceivingTheWrites(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mkdirAll("project", "sibling")
	f.write(filepath.Join("project", "sibling", "keep.txt"), "sibling's")
	pinned := pinOf(t, allowed(t, f.guard, f.in("inside"), f.in("inside")))
	sibling := listing(t, f.in("sibling"))

	if err := os.Rename(f.in("inside"), f.in("inside-moved")); err != nil {
		t.Fatal(err)
	}
	f.symlink("sibling", filepath.Join("project", "inside"))

	file, err := pinned.Path().Join("ok.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeThrough(file, "through the pin"); err != nil {
		t.Fatalf("write through the pin: %v", err)
	}
	if got := fileContent(t, f.in("inside-moved", "ok.txt")); got != "through the pin" {
		t.Fatalf("the original directory holds %q", got)
	}
	if after := listing(t, f.in("sibling")); after != sibling {
		t.Fatalf("the sibling changed:\n%s\nwas\n%s", after, sibling)
	}
	data, err := file.ReadFile()
	if err != nil || string(data) != "through the pin" {
		t.Fatalf("read through the pin: %q %v", data, err)
	}
}

func TestRenamingAnAncestorOfAPinnedDirectoryDoesNotRedirectIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	pinned := pinOf(t, allowed(t, f.guard, f.in("inside", "deep"), f.in("inside", "deep")))
	if err := os.Rename(f.in("inside"), f.in("elsewhere")); err != nil {
		t.Fatal(err)
	}
	f.mkdirAll("project", "inside", "deep")

	file, err := pinned.Path().Join("x.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeThrough(file, "x"); err != nil {
		t.Fatal(err)
	}
	if got := fileContent(t, f.in("elsewhere", "deep", "x.txt")); got != "x" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Lstat(f.in("inside", "deep", "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("the new directory at the old name received the write: %v", err)
	}
}

// Pin follows no link, even one that stays inside the root.
func TestALinkOnThePathBeingPinnedIsRefusedChanged(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	through := allowed(t, f.guard, f.in("inside", "deep"), f.in("inside", "deep"))
	if err := os.Rename(f.in("inside"), f.in("inside-moved")); err != nil {
		t.Fatal(err)
	}
	f.symlink("inside-moved", filepath.Join("project", "inside"))
	_, err := through.Pin()
	if fault := pinRefused(t, err, KindChanged, "a link component inside the root"); fault.Location != "inside" {
		t.Fatalf("location %q", fault.Location)
	}

	// The last component, and a link out of the root.
	last := allowed(t, f.guard, f.in("inside-moved", "deep"), f.in("inside-moved", "deep"))
	if err := os.Rename(f.in("inside-moved", "deep"), f.in("inside-moved", "deep-moved")); err != nil {
		t.Fatal(err)
	}
	f.symlink(f.outside, filepath.Join("project", "inside-moved", "deep"))
	_, err = last.Pin()
	_ = pinRefused(t, err, KindChanged, "a link out of the root as the last component")
}

func TestPinRefusesWhatIsNotADirectoryOrNotThere(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	file := allowed(t, f.guard, f.in("inside", "ok.txt"), f.in("inside", "ok.txt"))
	_, err := file.Pin()
	_ = pinRefused(t, err, KindNotDirectory, "a file")

	missing := allowed(t, f.guard, f.in("inside", "nothing", "below"), f.in("inside", "nothing", "below"))
	_, err = missing.Pin()
	if fault := pinRefused(t, err, KindNotFound, "a missing directory"); fault.Location != filepath.Join("inside", "nothing") {
		t.Fatalf("location %q", fault.Location)
	}

	_, err = Path{}.Pin()
	refused(t, err, "the zero Path")
}

func TestARevokedOrClosedRootOrAClosedPinRefusesForbidden(t *testing.T) {
	t.Parallel()
	for name, end := range map[string]func(f *fixture, pinned *PinnedDir){
		"revoked root": func(f *fixture, _ *PinnedDir) {
			if _, err := f.guard.Revoke(f.root); err != nil {
				f.t.Fatal(err)
			}
		},
		"closed guard": func(f *fixture, _ *PinnedDir) { _ = f.guard.Close() },
		"closed pin":   func(_ *fixture, pinned *PinnedDir) { _ = pinned.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			resolved := allowed(t, f.guard, f.in("inside"), f.in("inside"))
			pinned := pinOf(t, resolved)
			joined, err := pinned.Path().Join("ok.txt")
			if err != nil {
				t.Fatal(err)
			}
			end(f, pinned)

			_, err = joined.ReadFile()
			refused(t, err, "a read through the pin")
			refused(t, writeThrough(joined, "x"), "a write through the pin")
			_, err = pinned.Path().Join("ok.txt")
			refused(t, err, "a join from the pin")
			_, err = pinned.Path().Pin()
			refused(t, err, "a pin from the pin")
			if name != "closed pin" {
				_, err = resolved.Pin()
				refused(t, err, "a pin of a revoked Path")
			}
			if got := fileContent(t, f.in("inside", "ok.txt")); got != "ok" {
				t.Fatalf("the file changed: %q", got)
			}
		})
	}
}

// A link under the pinned directory may not lead out of it, not even to
// elsewhere in the authorized root.
func TestALinkUnderAPinnedDirectoryThatLeavesItIsForbidden(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(filepath.Join("project", "top.txt"), "top")
	f.symlink("../top.txt", filepath.Join("project", "inside", "up.txt"))
	f.symlink(filepath.Join(f.root, "top.txt"), filepath.Join("project", "inside", "absolute.txt"))
	f.symlink("ok.txt", filepath.Join("project", "inside", "within.txt"))
	pinned := pinOf(t, allowed(t, f.guard, f.in("inside"), f.in("inside")))

	for _, name := range []string{"up.txt", "absolute.txt"} {
		link, err := pinned.Path().Join(name)
		if err != nil {
			t.Fatal(err)
		}
		_, err = link.ReadFile()
		refused(t, err, name)
		if err := link.WriteFile([]byte("x"), 0o644); err == nil {
			t.Fatalf("%s: written through", name)
		}
	}
	if got := fileContent(t, f.in("top.txt")); got != "top" {
		t.Fatalf("top.txt changed: %q", got)
	}
	within, err := pinned.Path().Join("within.txt")
	if err != nil {
		t.Fatal(err)
	}
	if data, err := within.ReadFile(); err != nil || string(data) != "ok" {
		t.Fatalf("a link that stays inside the pin: %q %v", data, err)
	}
}

func TestARenameCannotCrossPins(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mkdirAll("project", "other")
	inside := pinOf(t, allowed(t, f.guard, f.in("inside"), f.in("inside")))
	other := pinOf(t, allowed(t, f.guard, f.in("other"), f.in("other")))
	from, _ := inside.Path().Join("ok.txt")
	to, _ := other.Path().Join("ok.txt")
	refused(t, from.RenameTo(to), "a rename between two pins")
	unpinned := allowed(t, f.guard, f.in("moved.txt"), f.in("moved.txt"))
	refused(t, from.RenameTo(unpinned), "a rename from a pin to its root")
	refused(t, unpinned.RenameTo(from), "a rename from a root to a pin")

	inPin, _ := inside.Path().Join("renamed.txt")
	if err := from.RenameTo(inPin); err != nil {
		t.Fatalf("a rename inside one pin: %v", err)
	}
	if got := fileContent(t, f.in("inside", "renamed.txt")); got != "ok" {
		t.Fatalf("got %q", got)
	}
}

// Pin on a Path joined from a pin walks from that pin, so it reaches the
// pinned directory even after its name has been taken over.
func TestPinningFromAPinWalksFromThePin(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outer := pinOf(t, allowed(t, f.guard, f.in("inside"), f.in("inside")))
	if err := os.Rename(f.in("inside"), f.in("inside-moved")); err != nil {
		t.Fatal(err)
	}
	f.mkdirAll("project", "decoy", "deep")
	f.symlink("decoy", filepath.Join("project", "inside"))

	deep, err := outer.Path().Join("deep")
	if err != nil {
		t.Fatal(err)
	}
	inner := pinOf(t, deep)
	if inner.Path().Real() != f.in("inside", "deep") || inner.Path().Rel() != filepath.Join("inside", "deep") || inner.Path().Root() != f.root {
		t.Fatalf("inner %q %q %q", inner.Path().Real(), inner.Path().Rel(), inner.Path().Root())
	}
	file, _ := inner.Path().Join("x.txt")
	if err := writeThrough(file, "x"); err != nil {
		t.Fatal(err)
	}
	if got := fileContent(t, f.in("inside-moved", "deep", "x.txt")); got != "x" {
		t.Fatalf("got %q", got)
	}
	if _, err := os.Lstat(f.in("decoy", "deep", "x.txt")); !os.IsNotExist(err) {
		t.Fatalf("the decoy received the write: %v", err)
	}
	// The outer pin closing does not stop the inner one, which holds its
	// own descriptor; the root being revoked does.
	_ = outer.Close()
	if _, err := file.ReadFile(); err != nil {
		t.Fatalf("the inner pin after the outer closed: %v", err)
	}
	if _, err := f.guard.Revoke(f.root); err != nil {
		t.Fatal(err)
	}
	_, err = file.ReadFile()
	refused(t, err, "the inner pin after the root was revoked")
}

// The moment between a component's Lstat and its open: a directory swapped
// in there, for a link or for another directory, is refused changed.
func TestADirectorySwappedBetweenTheLstatAndTheOpenIsRefusedChanged(t *testing.T) {
	t.Parallel()
	for name, swap := range map[string]func(f *fixture){
		"for a link inside the root": func(f *fixture) {
			f.mkdirAll("project", "decoy", "deep")
			f.symlink("decoy", filepath.Join("project", "inside"))
		},
		"for a link out of the root": func(f *fixture) {
			f.symlink(f.outside, filepath.Join("project", "inside"))
		},
		"for another directory": func(f *fixture) {
			f.mkdirAll("project", "inside", "deep")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			resolved := allowed(t, f.guard, f.in("inside", "deep"), f.in("inside", "deep"))
			swapped := false
			_, err := resolved.pin(func(component string) {
				if component != "inside" || swapped {
					return
				}
				swapped = true
				if err := os.Rename(f.in("inside"), f.in("inside-moved")); err != nil {
					t.Fatal(err)
				}
				swap(f)
			})
			if !swapped {
				t.Fatal("the hook never ran")
			}
			if fault := pinRefused(t, err, KindChanged, "a swap between the Lstat and the open"); fault.Location != "inside" {
				t.Fatalf("location %q", fault.Location)
			}
		})
	}
}
