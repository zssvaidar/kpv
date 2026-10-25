package projectfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every operation the package exposes refuses a path outside the root. Not one
// representative operation: all of them, named one by one, because a guard
// with one unguarded door is not a guard and the door is always the one nobody
// listed.
func TestEveryOperationRefusesAPathOutsideTheRoot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	outside := f.at("outside", "secret.txt")
	outsideDir := f.outside
	inside := f.in("inside", "ok.txt")

	guardOperations := map[string]func(path string) error{
		"Guard.Resolve":         func(p string) error { _, err := f.guard.Resolve(p); return err },
		"Guard.Open":            func(p string) error { _, err := f.guard.Open(p); return err },
		"Guard.ReadFile":        func(p string) error { _, err := f.guard.ReadFile(p); return err },
		"Guard.WriteFile":       func(p string) error { return f.guard.WriteFile(p, []byte("planted"), 0o644) },
		"Guard.WriteAtomically": func(p string) error { return f.guard.WriteAtomically(p, 0o644, writePlanted) },
		"Guard.Stat":            func(p string) error { _, err := f.guard.Stat(p); return err },
		"Guard.ReadDir":         func(p string) error { _, err := f.guard.ReadDir(p); return err },
		"Guard.Remove":          func(p string) error { return f.guard.Remove(p) },
		"Guard.Rename from":     func(p string) error { return f.guard.Rename(p, inside) },
		"Guard.Rename to":       func(p string) error { return f.guard.Rename(inside, p) },
	}
	for name, operation := range guardOperations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			refused(t, operation(outside), name+" on a file outside the root")
			refused(t, operation(outsideDir), name+" on a directory outside the root")
			refused(t, operation(f.at("outside", "not-there")), name+" on a missing path outside the root")
			refused(t, operation(f.root+"/../outside/secret.txt"), name+" on a traversal out of the root")
		})
	}

	// The same for the handle's own operations, reached through a handle that
	// was resolved legitimately and then had an escaping link planted under
	// it. Every one of them has to be re-checked.
	t.Run("Path operations through a planted link", func(t *testing.T) {
		t.Parallel()
		local := newFixture(t)
		local.mkdirAll("project", "work")
		local.mkdirAll("outside", "work")
		local.write(filepath.Join("outside", "work", "secret.txt"), "secret")
		handle, err := local.guard.Resolve(local.in("work", "secret.txt"))
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		directory, err := local.guard.Resolve(local.in("work"))
		if err != nil {
			t.Fatalf("resolve the directory: %v", err)
		}
		// work becomes a link pointing outside.
		if err := os.Remove(local.in("work")); err != nil {
			t.Fatalf("remove: %v", err)
		}
		if err := os.Symlink(local.at("outside", "work"), local.in("work")); err != nil {
			t.Fatalf("link: %v", err)
		}

		for name, operation := range map[string]func() error{
			"Path.Open":            func() error { _, err := handle.Open(); return err },
			"Path.OpenFile":        func() error { _, err := handle.OpenFile(os.O_RDWR, 0o644); return err },
			"Path.Create":          func() error { _, err := handle.Create(); return err },
			"Path.ReadFile":        func() error { _, err := handle.ReadFile(); return err },
			"Path.WriteFile":       func() error { return handle.WriteFile([]byte("planted"), 0o644) },
			"Path.Stat":            func() error { _, err := handle.Stat(); return err },
			"Path.Lstat":           func() error { _, err := handle.Lstat(); return err },
			"Path.Readlink":        func() error { _, err := handle.Readlink(); return err },
			"Path.ReadDir":         func() error { _, err := directory.ReadDir(); return err },
			"Path.Mkdir":           func() error { return handle.Mkdir(0o755) },
			"Path.MkdirAll":        func() error { return handle.MkdirAll(0o755) },
			"Path.Remove":          func() error { return handle.Remove() },
			"Path.RemoveAll":       func() error { return handle.RemoveAll() },
			"Path.WriteAtomically": func() error { return handle.WriteAtomically(0o644, writePlanted) },
		} {
			err := operation()
			if err == nil {
				t.Errorf("%s reached through the planted link", name)
				continue
			}
			if IsNotFound(err) {
				t.Errorf("%s was reported as a missing file: %v", name, err)
			}
		}

		// Nothing out there was read, written, or removed.
		if data, err := os.ReadFile(local.at("outside", "work", "secret.txt")); err != nil || string(data) != "secret" {
			t.Errorf("the file outside reads %q (%v), want %q untouched", data, err, "secret")
		}
		entries, err := os.ReadDir(local.at("outside", "work"))
		if err != nil {
			t.Fatalf("list outside: %v", err)
		}
		if len(entries) != 1 || entries[0].Name() != "secret.txt" {
			t.Errorf("the directory outside holds %v, want only secret.txt", entries)
		}
	})
}

func writePlanted(w io.Writer) error {
	_, err := w.Write([]byte("planted"))
	return err
}

// The operations work, on a path inside the root. A guard that refused
// everything would pass every test above and be useless.
func TestTheOperationsWorkInsideTheRoot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	if data, err := f.guard.ReadFile(f.in("inside", "ok.txt")); err != nil || string(data) != "ok" {
		t.Fatalf("read inside the root: %q (%v)", data, err)
	}
	file, err := f.guard.Open(f.in("inside", "ok.txt"))
	if err != nil {
		t.Fatalf("open inside the root: %v", err)
	}
	contents, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil || string(contents) != "ok" {
		t.Errorf("the opened file read %q (%v), want %q", contents, err, "ok")
	}

	if err := f.guard.WriteFile(f.in("inside", "written.txt"), []byte("written"), 0o640); err != nil {
		t.Fatalf("write inside the root: %v", err)
	}
	info, err := os.Stat(f.in("inside", "written.txt"))
	if err != nil {
		t.Fatalf("stat the written file: %v", err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("the written file has mode %v, want %v", info.Mode().Perm(), fs.FileMode(0o640))
	}

	stat, err := f.guard.Stat(f.in("inside"))
	if err != nil || !stat.IsDir() {
		t.Errorf("stat the directory inside: %v (%v)", stat, err)
	}

	// Directories, created and removed.
	created, err := f.guard.Resolve(f.in("new", "deeper"))
	if err != nil {
		t.Fatalf("resolve a path that is not there yet: %v", err)
	}
	if err := created.MkdirAll(0o755); err != nil {
		t.Fatalf("create it: %v", err)
	}
	if stat, err := created.Stat(); err != nil || !stat.IsDir() {
		t.Errorf("the created directory: %v (%v)", stat, err)
	}
	if err := created.Remove(); err != nil {
		t.Fatalf("remove it: %v", err)
	}
	parent, err := f.guard.Resolve(f.in("new"))
	if err != nil {
		t.Fatalf("resolve the parent: %v", err)
	}
	if err := parent.RemoveAll(); err != nil {
		t.Fatalf("remove the parent: %v", err)
	}

	// Renaming, inside the root.
	if err := f.guard.Rename(f.in("inside", "written.txt"), f.in("inside", "moved.txt")); err != nil {
		t.Fatalf("rename inside the root: %v", err)
	}
	if _, err := os.Stat(f.in("inside", "written.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the renamed file is still at its old name")
	}
	if data, err := os.ReadFile(f.in("inside", "moved.txt")); err != nil || string(data) != "written" {
		t.Errorf("the renamed file reads %q (%v), want %q", data, err, "written")
	}
}

// Reading a directory gives every entry, sorted, with a link reported as a
// link rather than as what it points at. This is what #7 and #48 need from
// here, and the one thing they must not be able to do by accident is follow a
// link out of the root while enumerating.
func TestReadingADirectoryReportsEntriesWithoutFollowingLinks(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mkdirAll("project", "listing", "sub")
	f.write(filepath.Join("project", "listing", "b.txt"), "b")
	f.write(filepath.Join("project", "listing", "a.txt"), "a")
	f.symlink("../../outside", filepath.Join("project", "listing", "z-escape"))
	f.symlink("a.txt", filepath.Join("project", "listing", "c-alias"))

	entries, err := f.guard.ReadDir(f.in("listing"))
	if err != nil {
		t.Fatalf("read the directory: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got, want := strings.Join(names, ","), "a.txt,b.txt,c-alias,sub,z-escape"; got != want {
		t.Errorf("the directory listed %q, want %q", got, want)
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "z-escape", "c-alias":
			if entry.Type()&fs.ModeSymlink == 0 {
				t.Errorf("%s is a link and was listed as %v", entry.Name(), entry.Type())
			}
		case "sub":
			if !entry.IsDir() {
				t.Errorf("sub is a directory and was listed as %v", entry.Type())
			}
		}
	}

	// Walking into the escaping entry is refused, which is what stops an
	// enumeration leaving the root.
	listing, err := f.guard.Resolve(f.in("listing"))
	if err != nil {
		t.Fatalf("resolve the directory: %v", err)
	}
	escape, err := listing.Join("z-escape")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := escape.ReadDir(); err == nil {
		t.Error("the escaping entry was enumerated")
	} else {
		refused(t, err, "enumerating through the escaping entry")
	}
	// And the entry that stays inside is followed, so the listing is usable.
	alias, err := listing.Join("c-alias")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if data, err := alias.ReadFile(); err != nil || string(data) != "a" {
		t.Errorf("the alias inside the root read %q (%v), want %q", data, err, "a")
	}
}

// The atomic write: it replaces the file, and a failure part-way through
// leaves the previous file exactly as it was with no temporary file behind.
func TestTheAtomicWriteReplacesOrLeavesAlone(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(filepath.Join("project", "inside", "state.txt"), "previous")
	if err := os.Chmod(f.in("inside", "state.txt"), 0o600); err != nil {
		t.Fatalf("set the mode: %v", err)
	}
	target := f.in("inside", "state.txt")

	t.Run("it replaces the file and keeps its mode", func(t *testing.T) {
		if err := f.guard.WriteAtomically(target, 0o644, func(w io.Writer) error {
			_, err := w.Write([]byte("next"))
			return err
		}); err != nil {
			t.Fatalf("write: %v", err)
		}
		if data, err := os.ReadFile(target); err != nil || string(data) != "next" {
			t.Fatalf("the file reads %q (%v), want %q", data, err, "next")
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("the file has mode %v, want the %v it had", info.Mode().Perm(), fs.FileMode(0o600))
		}
		assertNoLeftovers(t, f.in("inside"))
	})

	t.Run("a failure part-way leaves the previous file", func(t *testing.T) {
		stop := errors.New("stopped on purpose")
		err := f.guard.WriteAtomically(target, 0o644, func(w io.Writer) error {
			// Real bytes, really flushed to the temporary file, and then a
			// failure: the destination must not have moved.
			if _, writeErr := w.Write([]byte("half of something")); writeErr != nil {
				return writeErr
			}
			if file, ok := w.(*os.File); ok {
				if syncErr := file.Sync(); syncErr != nil {
					return syncErr
				}
				if _, statErr := os.Stat(file.Name()); statErr != nil {
					t.Errorf("the temporary file is not on disk: %v", statErr)
				}
			}
			return stop
		})
		if !errors.Is(err, stop) {
			t.Fatalf("the failure was reported as %v, want the one the writer returned", err)
		}
		if data, readErr := os.ReadFile(target); readErr != nil || string(data) != "next" {
			t.Errorf("the previous file reads %q (%v), want %q", data, readErr, "next")
		}
		assertNoLeftovers(t, f.in("inside"))
	})

	t.Run("a new file is created with the mode it was given", func(t *testing.T) {
		fresh := f.in("inside", "fresh.txt")
		if err := f.guard.WriteAtomically(fresh, 0o600, func(w io.Writer) error {
			_, err := w.Write([]byte("fresh"))
			return err
		}); err != nil {
			t.Fatalf("write: %v", err)
		}
		info, err := os.Stat(fresh)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Errorf("the new file has mode %v, want %v", info.Mode().Perm(), fs.FileMode(0o600))
		}
		assertNoLeftovers(t, f.in("inside"))
	})

	t.Run("the root itself is not a file to write", func(t *testing.T) {
		if err := f.guard.WriteAtomically(f.root, 0o644, writePlanted); err == nil {
			t.Error("the authorized root was written over")
		}
	})
}

// assertNoLeftovers fails if an atomic write left a temporary file behind.
func assertNoLeftovers(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("list %s: %v", directory, err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".tmp-") {
			t.Errorf("a temporary file was left behind: %s", entry.Name())
		}
	}
}

// A directory this process cannot read is reported as unreadable rather than
// as a refusal or as missing, which is what #7 needs in order to list an entry
// it cannot open instead of dropping it.
func TestAnUnreadableDirectoryInsideTheRootIsReportedAsUnreadable(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a directory with no permissions, so there is nothing to measure")
	}
	f := newFixture(t)
	f.mkdirAll("project", "closed", "deeper")
	f.write(filepath.Join("project", "closed", "deeper", "file.txt"), "hidden")
	if err := os.Chmod(f.in("closed"), 0o000); err != nil {
		t.Fatalf("close the directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.in("closed"), 0o755) })

	_, err := f.guard.Resolve(f.in("closed", "deeper", "file.txt"))
	if KindOf(err) != KindUnreadable {
		t.Errorf("a path under an unreadable directory gave %v, want %q", err, KindUnreadable)
	}
	if IsNotFound(err) {
		t.Errorf("an unreadable directory was reported as missing: %v", err)
	}
}

// A path whose unreadable part is outside every root is a refusal, not a
// report about what is out there. Failing to resolve is not a reason to say
// more than "no".
func TestAnUnreadablePathOutsideEveryRootIsStillARefusal(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("running as root, which can read a directory with no permissions, so there is nothing to measure")
	}
	f := newFixture(t)
	f.mkdirAll("outside", "closed", "deeper")
	if err := os.Chmod(f.at("outside", "closed"), 0o000); err != nil {
		t.Fatalf("close the directory: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(f.at("outside", "closed"), 0o755) })

	_, err := f.guard.Resolve(f.at("outside", "closed", "deeper", "file.txt"))
	refused(t, err, "an unreadable path outside every root")
}
