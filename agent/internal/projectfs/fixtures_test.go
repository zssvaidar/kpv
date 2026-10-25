package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// fixture is a real directory tree with a real authorized root in it, a real
// directory outside that root, and real symbolic links between the two.
//
// Everything here is built on disk rather than faked. A guard that is wrong
// about symbolic links is wrong about them in the filesystem, and a test that
// does not use one cannot find out.
type fixture struct {
	t *testing.T
	// base is the temporary directory, resolved: it holds root and outside.
	base string
	// root is the authorized project directory.
	root string
	// outside is a directory beside the root that nothing may reach.
	outside string
	// guard has root authorized and nothing else.
	guard *Guard
}

// newFixture builds the tree every test in this package starts from:
//
//	base/
//	  project/            the authorized root
//	    inside/ok.txt     an ordinary file to read
//	    inside/deep/
//	  outside/secret.txt  a file no path may reach
//	  projectevil/loot.txt  a sibling whose name starts with the root's
//	  Project/loot.txt      a sibling that differs from the root only in case
func newFixture(t *testing.T) *fixture {
	t.Helper()
	// The temporary directory may itself be reached through a symbolic link
	// (/tmp is one on some systems), and every path this package reports is a
	// real path, so the expectations have to be real paths too.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	f := &fixture{t: t, base: base, root: filepath.Join(base, "project"), outside: filepath.Join(base, "outside")}

	f.mkdirAll("project", "inside", "deep")
	f.write(filepath.Join("project", "inside", "ok.txt"), "ok")
	f.mkdirAll("outside")
	f.write(filepath.Join("outside", "secret.txt"), "secret")
	f.mkdirAll("projectevil")
	f.write(filepath.Join("projectevil", "loot.txt"), "loot")
	f.mkdirAll("Project")
	f.write(filepath.Join("Project", "loot.txt"), "loot")

	guard, err := New(f.root)
	if err != nil {
		t.Fatalf("authorize %s: %v", f.root, err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	f.guard = guard
	return f
}

// at builds an absolute path inside the fixture's temporary directory.
func (f *fixture) at(elements ...string) string {
	return filepath.Join(append([]string{f.base}, elements...)...)
}

// in builds an absolute path inside the authorized root.
func (f *fixture) in(elements ...string) string {
	return filepath.Join(append([]string{f.root}, elements...)...)
}

func (f *fixture) mkdirAll(elements ...string) {
	f.t.Helper()
	if err := os.MkdirAll(f.at(elements...), 0o755); err != nil {
		f.t.Fatalf("create %v: %v", elements, err)
	}
}

func (f *fixture) write(relative, content string) {
	f.t.Helper()
	if err := os.WriteFile(f.at(relative), []byte(content), 0o644); err != nil {
		f.t.Fatalf("write %s: %v", relative, err)
	}
}

// symlink creates a link at relative pointing at target, exactly as written.
func (f *fixture) symlink(target, relative string) {
	f.t.Helper()
	if err := os.Symlink(target, f.at(relative)); err != nil {
		f.t.Fatalf("link %s -> %s: %v", relative, target, err)
	}
}

// replaceSymlink swaps the link at relative for one pointing somewhere else,
// which is the move a validate-then-open design loses to.
func (f *fixture) replaceSymlink(target, relative string) {
	f.t.Helper()
	if err := os.Remove(f.at(relative)); err != nil {
		f.t.Fatalf("remove %s: %v", relative, err)
	}
	f.symlink(target, relative)
}

// refused asserts that err is this package refusing a path, and that nothing
// can mistake the refusal for a file that is simply not there.
func refused(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was allowed, and it must be refused", what)
	}
	var fault *PathError
	if !errors.As(err, &fault) {
		t.Fatalf("%s failed with %T (%v), and a refusal has to be a *PathError", what, err, err)
	}
	if fault.Kind != KindForbidden {
		t.Errorf("%s was refused as %q; a path outside an authorized root is %q", what, fault.Kind, KindForbidden)
	}
	if !IsForbidden(err) {
		t.Errorf("%s: IsForbidden says no", what)
	}
	if IsNotFound(err) || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s was refused as a missing file (%v); a caller must be able to tell the two apart", what, err)
	}
	if fault.Message == "" {
		t.Errorf("%s was refused with no message", what)
	}
}

// allowed asserts that a path resolves, and that it resolves to what it should.
func allowed(t *testing.T, guard *Guard, path, wantReal string) Path {
	t.Helper()
	resolved, err := guard.Resolve(path)
	if err != nil {
		t.Fatalf("%s was refused (%v), and it is inside the authorized root", path, err)
	}
	if resolved.Real() != wantReal {
		t.Errorf("%s resolved to %s, want %s", path, resolved.Real(), wantReal)
	}
	return resolved
}
