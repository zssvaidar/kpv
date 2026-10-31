// PrivateFile (#24): a file of the agent's own beside the roots store, which
// no authorized root may hold.

package projectfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPrivateFileIsBesideTheRootsStoreAndNoRootMayHoldIt(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	file, err := OpenPrivateFile(f.roots, "connections.json")
	if err != nil || file == nil {
		t.Fatalf("PrivateFile = %v, %v", file, err)
	}
	if err := file.Write([]byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	stored := filepath.Join(filepath.Dir(f.store), "connections.json")
	if raw, err := os.ReadFile(stored); err != nil || string(raw) != "{}\n" {
		t.Fatalf("the private file is not beside the roots store: %q %v", raw, err)
	}
	for _, holder := range []string{filepath.Dir(stored), filepath.Dir(filepath.Dir(stored))} {
		_, err := f.roots.Authorize(holder)
		_ = refusedAs(t, err, KindForbidden, "a directory holding the connection store")
	}
}

func TestAPrivateFileIsWrittenOwnerOnlyInAnOwnerOnlyDirectory(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	// The directory already exists with a looser mode, as a umask or an
	// earlier version could leave it.
	mustMkdirAll(t, filepath.Dir(f.store))
	if err := os.Chmod(filepath.Dir(f.store), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := OpenPrivateFile(f.roots, "connections.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(filepath.Dir(f.store), "connections.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("file mode %v, %v", info, err)
	}
	directory, err := os.Stat(filepath.Dir(f.store))
	if err != nil || directory.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode %v, %v", directory.Mode(), err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.store), ".connections.json.tmp-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
}

func TestWithoutARootsStoreThereIsNoPrivateFile(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	roots, err := OpenRoots(f.browser, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	file, err := OpenPrivateFile(roots, "connections.json")
	if file != nil || err != nil {
		t.Fatalf("PrivateFile without a store = %v, %v", file, err)
	}
}

func TestAPrivateFileIsOnePlainNameAndNotTheRootsStore(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	for _, name := range []string{"", ".", "..", "a/b", "../x", ".hidden", "a\x00b", "roots.json"} {
		if _, err := OpenPrivateFile(f.roots, name); KindOf(err) != KindInvalid {
			t.Errorf("PrivateFile(%q) = %v, want invalid", name, err)
		}
	}
}

func TestAnUnusablePrivateFileIsMovedAsideBeforeItIsWrittenOver(t *testing.T) {
	t.Parallel()
	f := newRootsFixture(t)
	path := filepath.Join(filepath.Dir(f.store), "connections.json")
	mustMkdirAll(t, filepath.Dir(path))
	mustWriteFile(t, path, "not what its owner wrote")
	file, err := OpenPrivateFile(f.roots, "connections.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, found, problem := file.Read()
	if !found || problem != "" || string(raw) != "not what its owner wrote" {
		t.Fatalf("Read = %q %v %q", raw, found, problem)
	}
	if err := file.SetAside(); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	if kept, _ := os.ReadFile(backups[0]); string(kept) != "not what its owner wrote" {
		t.Fatalf("the backup holds %q", kept)
	}
	if !strings.HasSuffix(backups[0], "Z") {
		t.Fatalf("the backup %s is not named by its UTC time", backups[0])
	}
	if _, _, problem := file.Read(); problem != "" {
		t.Fatalf("after the move there is no file, got %q", problem)
	}
}

func TestAnUnreadablePrivateFileThatCannotBeMovedAsideIsNeverWrittenOver(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads and renames anything")
	}
	f := newRootsFixture(t)
	directory := filepath.Dir(f.store)
	path := filepath.Join(directory, "connections.json")
	mustMkdirAll(t, directory)
	mustWriteFile(t, path, "secret bytes")
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	file, err := OpenPrivateFile(f.roots, "connections.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, problem := file.Read(); problem != storeUnreadable {
		t.Fatalf("Read problem %q, want unreadable", problem)
	}
	// The directory cannot be changed, so the file cannot be moved aside.
	if err := os.Chmod(directory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(directory, 0o700); _ = os.Chmod(path, 0o600) })
	if err := file.Write([]byte("new")); KindOf(err) != KindIO {
		t.Fatalf("Write = %v, want io", err)
	}
	_ = os.Chmod(directory, 0o700)
	_ = os.Chmod(path, 0o600)
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "secret bytes" {
		t.Fatalf("the unreadable file was written over: %q %v", raw, err)
	}
	if err := file.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	backups, _ := filepath.Glob(path + ".corrupt-*")
	if len(backups) != 1 {
		t.Fatalf("backups %v", backups)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "new" {
		t.Fatalf("after the move the file holds %q %v", raw, err)
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		t.Fatal("gone")
	}
}
