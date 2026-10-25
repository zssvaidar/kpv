package projectfs

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// A path that is exactly an authorized root is accepted, and it is accepted as
// the root rather than as something inside it.
func TestAPathExactlyEqualToAnAuthorizedRootIsAccepted(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	resolved := allowed(t, f.guard, f.root, f.root)
	if !resolved.IsRoot() {
		t.Error("the root resolved to something that does not call itself the root")
	}
	if resolved.Rel() != "." {
		t.Errorf("the root is %q inside itself, want %q", resolved.Rel(), ".")
	}
	if resolved.Root() != f.root {
		t.Errorf("the root belongs to %q, want %q", resolved.Root(), f.root)
	}
	// Accepted means usable, not merely resolvable.
	entries, err := resolved.ReadDir()
	if err != nil {
		t.Fatalf("read the authorized root as a directory: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "inside" {
		t.Errorf("the root listed %v, want just inside", entries)
	}
}

// The spellings of a root and of a path inside it that name the same file.
// Every one of them has to land on the same real path, because any of them can
// arrive from a caller and they are the same file.
func TestEquivalentSpellingsOfAnAcceptedPathResolveToTheSameFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	file := f.in("inside", "ok.txt")

	for _, spelling := range []struct {
		name string
		path string
		want string
	}{
		{"the root itself", f.root, f.root},
		{"the root with a trailing separator", f.root + "/", f.root},
		{"the root with several trailing separators", f.root + "///", f.root},
		{"the root as a dot path", f.root + "/.", f.root},
		{"a file inside the root", file, file},
		{"doubled separators", f.root + "//inside//ok.txt", file},
		{"dot segments", f.root + "/./inside/./ok.txt", file},
		{"up and back down inside the root", f.root + "/inside/deep/../ok.txt", file},
		{"up and back down through the root itself", f.root + "/../project/inside/ok.txt", file},
	} {
		t.Run(spelling.name, func(t *testing.T) {
			t.Parallel()
			resolved := allowed(t, f.guard, spelling.path, spelling.want)
			if spelling.want == file {
				data, err := resolved.ReadFile()
				if err != nil {
					t.Fatalf("read %s: %v", spelling.path, err)
				}
				if string(data) != "ok" {
					t.Errorf("read %q from %s, want %q", data, spelling.path, "ok")
				}
			}
		})
	}
}

// A root authorized by a relative path, and a path inside it given relatively,
// are both held to the same rules. The decision is that a relative path is
// made absolute against the process's working directory and then resolved like
// any other; it is not a second kind of path with second rules.
func TestARelativeRootAndARelativePathAreResolvedAgainstTheWorkingDirectory(t *testing.T) {
	// t.Chdir makes this test non-parallel, which is the point: the working
	// directory is process-wide state.
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "project", "inside"), 0o755); err != nil {
		t.Fatalf("build the tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "project", "inside", "ok.txt"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "outside"), 0o755); err != nil {
		t.Fatalf("build the tree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "outside", "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	t.Chdir(base)

	guard, err := New("project")
	if err != nil {
		t.Fatalf("authorize the root by its relative name: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	if roots := guard.Roots(); len(roots) != 1 || roots[0] != filepath.Join(base, "project") {
		t.Fatalf("a relative root was recorded as %v, want the absolute %s", roots, filepath.Join(base, "project"))
	}

	allowed(t, guard, "project", filepath.Join(base, "project"))
	allowed(t, guard, "project/inside/ok.txt", filepath.Join(base, "project", "inside", "ok.txt"))
	allowed(t, guard, "./project/inside/ok.txt", filepath.Join(base, "project", "inside", "ok.txt"))

	refused(t, mustFail(guard.ReadFile("outside/secret.txt")), "a relative path to a file outside the root")
	refused(t, mustFail(guard.ReadFile("project/../outside/secret.txt")), "a relative path climbing out of the root")
	refused(t, mustFail(guard.ReadFile("../")), "a relative path above the working directory")
}

// mustFail is for the read-and-discard shape: the bytes are irrelevant, the
// error is the assertion.
func mustFail(_ []byte, err error) error { return err }

// An absolute path outside every authorized root is refused, whether it is
// there or not. A path that does not exist is still refused rather than
// reported missing, because answering "missing" about a path outside every
// root would be answering a question nobody is allowed to ask.
func TestAnAbsolutePathOutsideEveryRootIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, attempt := range []struct {
		name string
		path string
	}{
		{"a real file beside the root", f.at("outside", "secret.txt")},
		{"the directory beside the root", f.outside},
		{"the parent of the root", f.base},
		{"the filesystem root", "/"},
		{"a system file", "/etc/passwd"},
		{"a path that does not exist at all", f.at("nowhere", "at", "all")},
		{"a path that does not exist under a real directory", f.at("outside", "missing.txt")},
		{"a sibling whose name starts with the root's", f.at("projectevil", "loot.txt")},
		{"the sibling directory itself", f.at("projectevil")},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.guard.Resolve(attempt.path)
			refused(t, err, attempt.path)
			_, err = f.guard.ReadFile(attempt.path)
			refused(t, err, "reading "+attempt.path)
		})
	}
}

// Containment is segment by segment, so a root's name being a prefix of a
// sibling's name is not containment. This is the bypass that reads as correct
// when it is written with strings.HasPrefix.
func TestASiblingWhoseNameStartsWithTheRootsIsNotInsideIt(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// The sibling really is there and really is readable without the guard,
	// so the refusal is the guard's and not the filesystem's.
	if _, err := os.ReadFile(f.at("projectevil", "loot.txt")); err != nil {
		t.Fatalf("the sibling this test is about is not readable: %v", err)
	}
	_, err := f.guard.ReadFile(f.at("projectevil", "loot.txt"))
	refused(t, err, "a file in the sibling directory projectevil")

	// And the comparison itself, directly, so the rule is pinned rather than
	// inferred from one tree.
	for _, pair := range []struct {
		root, candidate string
		wantInside      bool
		wantRelative    string
	}{
		{"/home/user/project", "/home/user/project", true, "."},
		{"/home/user/project", "/home/user/project/a/b", true, "a/b"},
		{"/home/user/project", "/home/user/projectevil", false, ""},
		{"/home/user/project", "/home/user/projectevil/loot", false, ""},
		{"/home/user/project", "/home/user/proj", false, ""},
		{"/home/user/project", "/home/user", false, ""},
		{"/home/user/project", "/home", false, ""},
		{"/home/user/project", "/", false, ""},
		{"/", "/anything/at/all", true, "anything/at/all"},
		{"/", "/", true, "."},
	} {
		relative, inside := within(pair.root, pair.candidate)
		if inside != pair.wantInside {
			t.Errorf("within(%q, %q) = %v, want %v", pair.root, pair.candidate, inside, pair.wantInside)
		}
		if inside && relative != pair.wantRelative {
			t.Errorf("within(%q, %q) = %q, want %q", pair.root, pair.candidate, relative, pair.wantRelative)
		}
	}
}

// A guard with nothing authorized refuses everything. That is the state the
// agent starts in, before the user has picked a project.
func TestAGuardWithNoRootsRefusesEverything(t *testing.T) {
	t.Parallel()
	guard := &Guard{}
	t.Cleanup(func() { _ = guard.Close() })

	if roots := guard.Roots(); len(roots) != 0 {
		t.Errorf("a fresh guard already has roots: %v", roots)
	}
	for _, path := range []string{"/", "/etc/passwd", "/tmp", os.TempDir(), "relative/path"} {
		_, err := guard.Resolve(path)
		refused(t, err, path+" against a guard with no roots")
	}
}

// Closing a guard withdraws every root, and nothing resolves afterwards. The
// descriptors are what the enforcement is made of, so letting a path through
// after they are gone would be letting it through unenforced.
func TestAClosedGuardRefusesEverything(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	file := f.in("inside", "ok.txt")

	resolved := allowed(t, f.guard, file, file)
	if err := f.guard.Close(); err != nil {
		t.Fatalf("close the guard: %v", err)
	}

	_, err := f.guard.Resolve(file)
	refused(t, err, "a path inside a root after the guard was closed")
	if roots := f.guard.Roots(); len(roots) != 0 {
		t.Errorf("a closed guard still has roots: %v", roots)
	}
	// A handle taken before the close is not a way around it either.
	_, err = resolved.ReadFile()
	refused(t, err, "a handle resolved before the guard was closed")
	if _, err := f.guard.Authorize(f.root); err == nil {
		t.Error("a closed guard authorized a new root")
	}
}

// The zero Path is not a permission slip. It is the only Path value that can
// be built outside this package, so it has to reach nothing.
func TestTheZeroPathReachesNothing(t *testing.T) {
	t.Parallel()
	var empty Path

	if empty.Root() != "" || empty.Real() != "" || empty.IsRoot() {
		t.Errorf("the zero path claims a root: root=%q real=%q isRoot=%v", empty.Root(), empty.Real(), empty.IsRoot())
	}
	if _, err := empty.ReadFile(); !IsForbidden(err) {
		t.Errorf("the zero path read a file, or failed with %v", err)
	}
	if _, err := empty.Open(); !IsForbidden(err) {
		t.Errorf("the zero path opened a file, or failed with %v", err)
	}
	if _, err := empty.Stat(); !IsForbidden(err) {
		t.Errorf("the zero path stat'ed a file, or failed with %v", err)
	}
	if _, err := empty.ReadDir(); !IsForbidden(err) {
		t.Errorf("the zero path listed a directory, or failed with %v", err)
	}
	if err := empty.WriteFile([]byte("x"), 0o644); !IsForbidden(err) {
		t.Errorf("the zero path wrote a file, or failed with %v", err)
	}
	if err := empty.Remove(); !IsForbidden(err) {
		t.Errorf("the zero path removed a file, or failed with %v", err)
	}
	if _, err := empty.Join("x"); !IsForbidden(err) {
		t.Errorf("the zero path joined a name, or failed with %v", err)
	}
	if err := empty.WriteAtomically(0o644, func(io.Writer) error { return nil }); !IsForbidden(err) {
		t.Errorf("the zero path wrote atomically, or failed with %v", err)
	}
}

// Authorizing a root: what is accepted, what is not, and what happens twice.
func TestAuthorizingARoot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	t.Run("a directory that does not exist is not a root", func(t *testing.T) {
		t.Parallel()
		guard := &Guard{}
		t.Cleanup(func() { _ = guard.Close() })
		_, err := guard.Authorize(f.at("nowhere"))
		if !IsNotFound(err) {
			t.Errorf("authorizing a missing directory gave %v, want a not-found refusal", err)
		}
	})

	t.Run("a file is not a root", func(t *testing.T) {
		t.Parallel()
		guard := &Guard{}
		t.Cleanup(func() { _ = guard.Close() })
		_, err := guard.Authorize(f.in("inside", "ok.txt"))
		if KindOf(err) != KindNotDirectory {
			t.Errorf("authorizing a file gave %v, want %q", err, KindNotDirectory)
		}
	})

	t.Run("an empty path is not a root", func(t *testing.T) {
		t.Parallel()
		guard := &Guard{}
		t.Cleanup(func() { _ = guard.Close() })
		if _, err := guard.Authorize(""); KindOf(err) != KindInvalid {
			t.Errorf("authorizing an empty path gave %v, want %q", err, KindInvalid)
		}
		if _, err := guard.Authorize("/tmp/with\x00nul"); KindOf(err) != KindInvalid {
			t.Errorf("authorizing a path with a NUL gave %v, want %q", err, KindInvalid)
		}
	})

	t.Run("a root given twice is one root", func(t *testing.T) {
		t.Parallel()
		guard, err := New(f.root, f.root+"/", f.root+"/./")
		if err != nil {
			t.Fatalf("authorize the same root three ways: %v", err)
		}
		t.Cleanup(func() { _ = guard.Close() })
		if roots := guard.Roots(); len(roots) != 1 || roots[0] != f.root {
			t.Errorf("three spellings of one root gave %v, want just %s", roots, f.root)
		}
	})

	t.Run("a link to a directory authorizes the directory", func(t *testing.T) {
		t.Parallel()
		f.symlink(f.root, "link-to-project")
		guard, err := New(f.at("link-to-project"))
		if err != nil {
			t.Fatalf("authorize a link to the project: %v", err)
		}
		t.Cleanup(func() { _ = guard.Close() })
		if roots := guard.Roots(); len(roots) != 1 || roots[0] != f.root {
			t.Errorf("a link was recorded as %v, want the real %s", roots, f.root)
		}
		// Reached through the link, which resolves into the root: allowed,
		// and reported under the root's own real name.
		allowed(t, guard, f.at("link-to-project", "inside", "ok.txt"), f.in("inside", "ok.txt"))
	})

	t.Run("one bad root fails the whole call and opens nothing", func(t *testing.T) {
		t.Parallel()
		guard, err := New(f.root, f.at("nowhere"))
		if err == nil {
			t.Fatal("a missing root was accepted")
		}
		if guard != nil {
			t.Error("a failed New returned a guard")
		}
	})
}

// Nested roots are not order-dependent: the most specific root owns the path.
func TestTheMostSpecificRootOwnsAPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	inner := f.in("inside")

	for _, order := range [][]string{{f.root, inner}, {inner, f.root}} {
		guard, err := New(order...)
		if err != nil {
			t.Fatalf("authorize %v: %v", order, err)
		}
		file := f.in("inside", "ok.txt")
		resolved, err := guard.Resolve(file)
		if err != nil {
			t.Fatalf("resolve %s with roots %v: %v", file, order, err)
		}
		if resolved.Root() != inner {
			t.Errorf("with roots %v, %s was owned by %s, want the more specific %s", order, file, resolved.Root(), inner)
		}
		if resolved.Rel() != "ok.txt" {
			t.Errorf("with roots %v, %s is %q inside its root, want %q", order, file, resolved.Rel(), "ok.txt")
		}
		if err := guard.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
}

// Two roots, and each keeps to itself.
func TestTwoRootsDoNotLendEachOtherAuthority(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	second := f.at("second")
	if err := os.MkdirAll(filepath.Join(second, "sub"), 0o755); err != nil {
		t.Fatalf("build the second root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(second, "sub", "two.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("write in the second root: %v", err)
	}
	guard, err := New(f.root, second)
	if err != nil {
		t.Fatalf("authorize two roots: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })

	allowed(t, guard, f.in("inside", "ok.txt"), f.in("inside", "ok.txt"))
	allowed(t, guard, filepath.Join(second, "sub", "two.txt"), filepath.Join(second, "sub", "two.txt"))
	refused(t, mustFail(guard.ReadFile(f.at("outside", "secret.txt"))), "a file outside both roots")

	// A rename across two roots is not something to do quietly.
	from, err := guard.Resolve(f.in("inside", "ok.txt"))
	if err != nil {
		t.Fatalf("resolve the source: %v", err)
	}
	to, err := guard.Resolve(filepath.Join(second, "moved.txt"))
	if err != nil {
		t.Fatalf("resolve the destination: %v", err)
	}
	if err := from.RenameTo(to); !IsForbidden(err) {
		t.Errorf("a rename across roots gave %v, want a refusal", err)
	}
	if _, err := os.Stat(filepath.Join(second, "moved.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the refused rename happened anyway")
	}
}
