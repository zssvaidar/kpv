// Guard.Revoke (#63): a revoked root reaches nothing, through a new path or
// through a handle taken before the revoke.

package projectfs

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// revokeTree is base/project with a file in it, authorized, plus base/other,
// a second root, so a revoke can be seen to touch only the one it names.
func revokeTree(t *testing.T) (base, project, other string, guard *Guard) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	project = filepath.Join(base, "project")
	other = filepath.Join(base, "other")
	for _, directory := range []string{filepath.Join(project, "src"), other} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("make %s: %v", directory, err)
		}
	}
	if err := os.WriteFile(filepath.Join(project, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	guard, err = New(project, other)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	t.Cleanup(func() { _ = guard.Close() })
	return base, project, other, guard
}

func TestARevokedRootIsNoLongerAuthorizedAndNothingOnDiskIsTouched(t *testing.T) {
	t.Parallel()
	_, project, other, guard := revokeTree(t)
	realDir, err := guard.Revoke(project)
	if err != nil || realDir != project {
		t.Fatalf("revoke = %q, %v", realDir, err)
	}
	if roots := guard.Roots(); len(roots) != 1 || roots[0] != other {
		t.Fatalf("after the revoke the roots are %v, want only %s", roots, other)
	}
	for _, path := range []string{project, filepath.Join(project, "src", "main.go"), filepath.Join(project, "new.txt")} {
		if _, err := guard.Resolve(path); !IsForbidden(err) {
			t.Errorf("resolve %s after the revoke = %v, want forbidden", path, err)
		}
	}
	created := filepath.Join(project, "src", "created.txt")
	if err := guard.WriteFile(created, []byte("x"), 0o644); !IsForbidden(err) {
		t.Fatalf("a write after the revoke = %v, want forbidden", err)
	}
	if _, err := os.Lstat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the refused write created %s: %v", created, err)
	}
	// Revoking deletes nothing.
	if data, err := os.ReadFile(filepath.Join(project, "src", "main.go")); err != nil || string(data) != "package main\n" {
		t.Fatalf("the revoked root's file is %q, %v", data, err)
	}
	// The other root is untouched.
	if err := guard.WriteFile(filepath.Join(other, "still.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("the other root stopped working: %v", err)
	}
}

func TestAPathTakenBeforeARevokeStopsWorkingLikeAfterClose(t *testing.T) {
	t.Parallel()
	for name, end := range map[string]func(*Guard, string) error{
		"revoke": func(guard *Guard, project string) error { _, err := guard.Revoke(project); return err },
		"close":  func(guard *Guard, _ string) error { return guard.Close() },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, project, _, guard := revokeTree(t)
			file, err := guard.Resolve(filepath.Join(project, "src", "main.go"))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			directory, err := guard.Resolve(filepath.Join(project, "src"))
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if _, err := file.ReadFile(); err != nil {
				t.Fatalf("read before: %v", err)
			}
			if err := end(guard, project); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			operations := map[string]error{}
			_, operations["read"] = file.ReadFile()
			_, operations["stat"] = file.Stat()
			_, operations["open"] = file.Open()
			operations["write"] = file.WriteFile([]byte("x"), 0o644)
			_, operations["readdir"] = directory.ReadDir()
			operations["mkdir"] = directory.Mkdir(0o755)
			_, operations["join"] = directory.Join("x")
			operations["remove"] = file.Remove()
			for operation, err := range operations {
				if !IsForbidden(err) {
					t.Errorf("%s through a handle taken before the %s = %v, want forbidden", operation, name, err)
				}
			}
			if data, err := os.ReadFile(filepath.Join(project, "src", "main.go")); err != nil || string(data) != "package main\n" {
				t.Fatalf("the file was changed through a handle after the %s: %q, %v", name, data, err)
			}
		})
	}
}

func TestARootCanBeRevokedByAnySpellingThatResolvesToIt(t *testing.T) {
	t.Parallel()
	for name, spelling := range map[string]func(base, project string) string{
		"its real path":      func(_, project string) string { return project },
		"a trailing slash":   func(_, project string) string { return project + "/" },
		"a dot segment":      func(_, project string) string { return project + "/." },
		"the link it was by": func(base, _ string) string { return filepath.Join(base, "link") },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			base, project, other, guard := revokeTree(t)
			if err := os.Symlink(project, filepath.Join(base, "link")); err != nil {
				t.Fatalf("link: %v", err)
			}
			realDir, err := guard.Revoke(spelling(base, project))
			if err != nil || realDir != project {
				t.Fatalf("revoke = %q, %v", realDir, err)
			}
			if roots := guard.Roots(); len(roots) != 1 || roots[0] != other {
				t.Fatalf("roots are %v", roots)
			}
		})
	}
}

func TestRevokingWhatIsNotARootIsNotFound(t *testing.T) {
	t.Parallel()
	base, project, _, guard := revokeTree(t)
	for _, path := range []string{
		filepath.Join(project, "src"),
		base,
		filepath.Join(base, "missing"),
		"/",
		"relative",
	} {
		if _, err := guard.Revoke(path); KindOf(err) != KindNotFound {
			t.Errorf("revoke %s = %v, want notFound", path, err)
		}
	}
	if _, err := guard.Revoke(""); KindOf(err) != KindInvalid {
		t.Errorf("revoke of nothing = %v, want invalid", err)
	}
	if len(guard.Roots()) != 2 {
		t.Fatalf("a refused revoke changed the roots: %v", guard.Roots())
	}
	// Twice is not found the second time.
	if _, err := guard.Revoke(project); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := guard.Revoke(project); KindOf(err) != KindNotFound {
		t.Fatalf("a second revoke = %v, want notFound", err)
	}
}

// A root whose directory has gone is still revocable by its real path, which
// needs nothing from the filesystem.
func TestARootWhoseDirectoryHasGoneCanStillBeRevoked(t *testing.T) {
	t.Parallel()
	base, _, _, guard := revokeTree(t)
	gone := filepath.Join(base, "gone")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := guard.Authorize(gone); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if realDir, err := guard.Revoke(gone); err != nil || realDir != gone {
		t.Fatalf("revoke = %q, %v", realDir, err)
	}
}

func TestRevokingTheOuterOfTwoNestedRootsLeavesTheInnerOne(t *testing.T) {
	t.Parallel()
	_, project, _, guard := revokeTree(t)
	inner := filepath.Join(project, "src")
	if _, err := guard.Authorize(inner); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if _, err := guard.Revoke(project); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := guard.ReadFile(filepath.Join(inner, "main.go")); err != nil {
		t.Fatalf("the inner root stopped working: %v", err)
	}
	if _, err := guard.Resolve(project); !IsForbidden(err) {
		t.Fatalf("the outer root still resolves: %v", err)
	}
}

// Readers hammer a handle while the root is revoked. Any read that starts
// after Revoke has returned must fail; nothing may race (-race).
func TestARevokeRacingReadersCutsThemAllOff(t *testing.T) {
	t.Parallel()
	_, project, _, guard := revokeTree(t)
	file, err := guard.Resolve(filepath.Join(project, "src", "main.go"))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	var revoked atomic.Bool
	var succeededBefore, leaked atomic.Int64
	var group sync.WaitGroup
	start := make(chan struct{})
	for reader := 0; reader < 8; reader++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			for {
				after := revoked.Load()
				_, readErr := file.ReadFile()
				_, resolveErr := guard.Resolve(filepath.Join(project, "src", "main.go"))
				if after {
					if readErr == nil || resolveErr == nil {
						leaked.Add(1)
					}
					return
				}
				if readErr == nil {
					succeededBefore.Add(1)
				}
			}
		}()
	}
	close(start)
	// Revoke only once some reads have gone through, so the test is not
	// passing because nothing ever ran before the revoke.
	for succeededBefore.Load() < 16 {
		if _, err := file.Stat(); err != nil {
			t.Fatalf("stat before the revoke: %v", err)
		}
	}
	if _, err := guard.Revoke(project); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	revoked.Store(true)
	group.Wait()
	if leaked.Load() != 0 {
		t.Fatalf("%d operations that started after the revoke returned still reached the root", leaked.Load())
	}
}
