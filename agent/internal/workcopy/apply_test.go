// Apply, its drift check and snapshot, restore and the serialization with
// catalog.WriteSource, against a scratch home and store, with no route.

package workcopy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func results(report *Report) string {
	var out []string
	for _, file := range report.Files {
		entry := file.Path + ":" + string(file.Result)
		if file.Kind != "" {
			entry += ":" + string(file.Kind)
		}
		out = append(out, entry)
	}
	return strings.Join(out, ",")
}

func TestApplyWritesAddedModifiedAndDeletedFilesExactly(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	app := filepath.Join(root, "app")
	write(t, filepath.Join(app, "src", "crlf.py"), "a\r\nb\r\n")
	write(t, filepath.Join(app, "src", "gone.py"), "gone\n")
	write(t, filepath.Join(app, "src", "emptied.py"), "full\n")
	if err := os.Chmod(filepath.Join(app, "src", "main.py"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, root)
	changes := map[string]string{
		"src/main.py":          "print('applied')", // no final newline
		"src/crlf.py":          "a\r\nb changed\r\n",
		"src/new.py":           "new\n",
		"deep/er/missing.py":   "in a missing directory\n",
		"src/empty.py":         "",
		"src/emptied.py":       "",
		"pipeline.yaml":        "id: app\nname: Renamed\n",
		"src/crlf-to-lf.py":    "a\nb\n",
		"src/no-final-newline": "x",
	}
	var staged []Change
	for path, content := range changes {
		staged = append(staged, Change{Path: path, Text: []byte(content)})
	}
	staged = append(staged, Change{Path: "src/gone.py", Delete: true})
	revision := w.stage(location, staged...)
	report, err := w.store.Apply(location, revision)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || len(report.Files) != len(changes)+1 || report.Snapshot == "" {
		t.Fatalf("report %+v", report)
	}
	for _, file := range report.Files {
		if file.Result != Written {
			t.Fatalf("report %s", results(report))
		}
	}
	for path, content := range changes {
		if got := readFile(t, filepath.Join(app, filepath.FromSlash(path))); got != content {
			t.Fatalf("%s = %q, want %q", path, got, content)
		}
	}
	if _, err := os.Lstat(filepath.Join(app, "src", "gone.py")); !os.IsNotExist(err) {
		t.Fatalf("the deleted file is there: %v", err)
	}
	for path, want := range map[string]os.FileMode{"src/new.py": 0o644, "src/empty.py": 0o644, "src/main.py": 0o600, "deep": 0o755 | os.ModeDir, "deep/er": 0o755 | os.ModeDir} {
		info, err := os.Lstat(filepath.Join(app, filepath.FromSlash(path)))
		if err != nil || info.Mode() != want {
			t.Fatalf("%s: %v, %v; want %v", path, info.Mode(), err, want)
		}
	}
	changed := []string{"app/src/gone.py", "app/src/new.py", "app/deep", "app/deep/er", "app/deep/er/missing.py", "app/src/empty.py", "app/src/crlf-to-lf.py", "app/src/no-final-newline"}
	for path := range changes {
		changed = append(changed, "app/"+path)
	}
	sameTree(t, before, snapshot(t, root), changed...)
	if review := w.review(location); len(review.Files) != 0 || review.Revision != "" || len(review.Snapshots) != 1 || review.Snapshots[0].ID != report.Snapshot {
		t.Fatalf("after apply: %+v", review)
	}
	// Restore puts every file back, byte for byte.
	restored, err := w.store.Restore(location, report.Snapshot)
	if err != nil || !restored.Complete {
		t.Fatalf("Restore = %+v, %v", restored, err)
	}
	after := snapshot(t, root)
	// The directories the apply made are kept, empty.
	delete(after, "app/deep")
	delete(after, "app/deep/er")
	sameTree(t, before, after)
}

func TestADeletionLeavesItsDirectoryAndAnEmptyStagedFileIsWrittenAsZeroBytes(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	write(t, filepath.Join(root, "app", "lone", "only.py"), "x\n")
	revision := w.stage(location, Change{Path: "lone/only.py", Delete: true}, Change{Path: "zero.txt", Text: []byte{}})
	if _, err := w.store.Apply(location, revision); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(root, "app", "lone")); err != nil || !info.IsDir() {
		t.Fatalf("the emptied directory was removed: %v", err)
	}
	if info, err := os.Lstat(filepath.Join(root, "app", "zero.txt")); err != nil || info.Size() != 0 || !info.Mode().IsRegular() {
		t.Fatalf("zero.txt: %v %v", info, err)
	}
}

func TestEveryKindOfDriftIsListedAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	app := filepath.Join(root, "app")
	for _, name := range []string{"changed.py", "deleted.py", "link.py", "dir.py", "fine.py"} {
		write(t, filepath.Join(app, name), name+"\n")
	}
	revision := w.stage(location,
		Change{Path: "changed.py", Text: []byte("x")},
		Change{Path: "created.py", Text: []byte("x")},
		Change{Path: "deleted.py", Text: []byte("x")},
		Change{Path: "link.py", Text: []byte("x")},
		Change{Path: "dir.py", Text: []byte("x")},
		Change{Path: "fine.py", Text: []byte("x")},
	)
	write(t, filepath.Join(app, "changed.py"), "changed in the project\n")
	write(t, filepath.Join(app, "created.py"), "created in the project\n")
	if err := os.Remove(filepath.Join(app, "deleted.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(app, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "other.txt"), filepath.Join(app, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(app, "dir.py")); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(app, "dir.py"))
	before := snapshot(t, root)
	_, err := w.store.Apply(location, revision)
	fault := kindIs(t, err, KindDrift)
	var got []string
	for _, drift := range fault.Drift {
		got = append(got, drift.Path+":"+string(drift.Now))
	}
	want := "changed.py:changed,created.py:created,deleted.py:deleted,dir.py:notRegularFile,link.py:link"
	if strings.Join(got, ",") != want {
		t.Fatalf("drift\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	sameTree(t, before, snapshot(t, root))
	if review := w.review(location); len(review.Files) != 6 || len(review.Snapshots) != 0 {
		t.Fatalf("after a drift: %+v", review)
	}
}

func TestARevokedOrRenamedRootStopsTheApplyWithNothingWritten(t *testing.T) {
	t.Parallel()
	t.Run("revoked", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		root, location := w.pipeline()
		revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
		if _, err := w.roots.Revoke(root); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, root)
		_, err := w.store.Apply(location, revision)
		_ = kindIs(t, err, KindNotAuthorized)
		sameTree(t, before, snapshot(t, root))
	})
	t.Run("renamed away", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		root, location := w.pipeline()
		revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
		moved := root + "-moved"
		if err := os.Rename(root, moved); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, moved)
		_, err := w.store.Apply(location, revision)
		fault := kindIs(t, err, KindRootUnavailable)
		if fault.Reason == "" {
			t.Fatal("no reason")
		}
		sameTree(t, before, snapshot(t, moved))
	})
	t.Run("renamed away between the check and the write", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t)
		root, location := w.pipeline()
		revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
		moved := root + "-moved"
		w.store.hook.afterCheck = func() {
			if err := os.Rename(root, moved); err != nil {
				t.Error(err)
			}
		}
		before := snapshot(t, root)
		_, err := w.store.Apply(location, revision)
		_ = kindIs(t, err, KindRootUnavailable)
		sameTree(t, before, snapshot(t, moved))
		for _, name := range w.treeFiles() {
			if strings.HasPrefix(name, snapshotPrefix) {
				t.Fatalf("a snapshot was taken: %s", name)
			}
		}
	})
}

func TestALinkSwappedIntoThePathBetweenTheCheckAndTheWriteWritesNothing(t *testing.T) {
	t.Parallel()
	for _, swapped := range []string{"app/src", "app"} {
		t.Run(swapped, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			root, location := w.pipeline()
			elsewhere := filepath.Join(w.home, "elsewhere")
			mkdir(t, elsewhere)
			write(t, filepath.Join(elsewhere, "main.py"), "elsewhere\n")
			write(t, filepath.Join(elsewhere, "pipeline.yaml"), "id: app\n")
			mkdir(t, filepath.Join(elsewhere, "src"))
			revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
			target := filepath.Join(root, filepath.FromSlash(swapped))
			w.store.hook.afterCheck = func() {
				if err := os.Rename(target, target+"-old"); err != nil {
					t.Error(err)
				}
				if err := os.Symlink(elsewhere, target); err != nil {
					t.Error(err)
				}
			}
			outside := snapshot(t, elsewhere)
			_, err := w.store.Apply(location, revision)
			_ = kindIs(t, err, KindLink)
			sameTree(t, outside, snapshot(t, elsewhere))
			if got := readFile(t, filepath.Join(target+"-old", map[string]string{"app/src": "main.py", "app": "src/main.py"}[swapped])); got != "print('hi')\n" {
				t.Fatalf("the pinned directory was written: %q", got)
			}
		})
	}
}

func TestASnapshotThatCannotBeWrittenLeavesTheProjectUntouched(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")}, Change{Path: "src/new.py", Text: []byte("y")})
	tree := filepath.Join(w.config, TreeName)
	w.store.hook.afterCheck = func() {
		// The store's tree becomes a file, so nothing can be written in it.
		if err := os.RemoveAll(tree); err != nil {
			t.Error(err)
		}
		write(t, tree, "not a directory")
	}
	before := snapshot(t, root)
	_, err := w.store.Apply(location, revision)
	_ = kindIs(t, err, KindSnapshot)
	sameTree(t, before, snapshot(t, root))
}

func TestAFailurePartwayReportsEveryPathKeepsTheRestStagedAndRestoreUndoesIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	write(t, filepath.Join(root, "app", "src", "b.py"), "b\r\n")
	before := snapshot(t, root)
	revision := w.stage(location,
		Change{Path: "src/a.py", Text: []byte("a\n")},
		Change{Path: "src/b.py", Text: []byte("b2\r\n")},
		Change{Path: "src/main.py", Delete: true},
	)
	w.store.hook.beforeFile = func(index int, _ string) error {
		if index == 1 {
			return errors.New("the disk is full")
		}
		return nil
	}
	report, err := w.store.Apply(location, revision)
	if err != nil {
		t.Fatal(err)
	}
	if report.Complete || results(report) != "src/a.py:written,src/b.py:failed:io,src/main.py:notWritten" {
		t.Fatalf("report %+v %s", report, results(report))
	}
	w.store.hook.beforeFile = nil
	review := w.review(location)
	if len(review.Files) != 2 || review.Files[0].Path != "src/b.py" || review.Files[1].Path != "src/main.py" || len(review.Snapshots) != 1 {
		t.Fatalf("after a partial apply: %+v", review)
	}
	if readFile(t, filepath.Join(root, "app", "src", "a.py")) != "a\n" {
		t.Fatal("the written file is not there")
	}
	restored, err := w.store.Restore(location, report.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Complete || results(restored) != "src/a.py:written,src/b.py:unchanged,src/main.py:unchanged" {
		t.Fatalf("restore %s", results(restored))
	}
	sameTree(t, before, snapshot(t, root))
}

func TestRestoreRefusesAFileChangedSinceTheApplyAndWritesNothing(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("applied\n")}, Change{Path: "src/new.py", Text: []byte("new\n")})
	report, err := w.store.Apply(location, revision)
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "app", "src", "new.py"), "edited after the apply\n")
	before := snapshot(t, root)
	_, err = w.store.Restore(location, report.Snapshot)
	fault := kindIs(t, err, KindDrift)
	if len(fault.Drift) != 1 || fault.Drift[0] != (Drift{Path: "src/new.py", Now: Changed}) {
		t.Fatalf("drift %+v", fault.Drift)
	}
	sameTree(t, before, snapshot(t, root))
	_, err = w.store.Restore(location, "20260101T000000Z-00000000")
	_ = kindIs(t, err, KindNotFound)
	_, err = w.store.Restore(location, "../../x")
	_ = kindIs(t, err, KindNotFound)
}

func TestSnapshotsAreListedNewestFirstAndRemovedOnlyByTheUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, location := w.pipeline()
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	w.store.now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	var ids []string
	for _, content := range []string{"one", "two", "three"} {
		revision := w.stage(location, Change{Path: "src/main.py", Text: []byte(content)})
		report, err := w.store.Apply(location, revision)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, report.Snapshot)
	}
	review := w.review(location)
	if len(review.Snapshots) != 3 || review.Snapshots[0].ID != ids[2] || review.Snapshots[2].ID != ids[0] {
		t.Fatalf("snapshots %+v", review.Snapshots)
	}
	first := review.Snapshots[2]
	if !first.Taken.Equal(time.Date(2026, 11, 22, 10, 1, 0, 0, time.UTC)) || first.Taken.Location() != time.UTC || strings.Join(first.Paths, ",") != "src/main.py" {
		t.Fatalf("snapshot %+v", first)
	}
	if err := w.store.RemoveSnapshot(location, ids[1]); err != nil {
		t.Fatal(err)
	}
	_ = kindIs(t, w.store.RemoveSnapshot(location, ids[1]), KindNotFound)
	w.open(true)
	if review := w.review(location); len(review.Snapshots) != 2 {
		t.Fatalf("after remove and restart: %+v", review.Snapshots)
	}
}

func TestAnApplyAndAWriteSourceNeverInterleave(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, location := w.pipeline()
	read, err := w.catalog.ReadSource(location, "src/main.py")
	if err != nil {
		t.Fatal(err)
	}
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("applied\n")})
	done := make(chan error, 1)
	w.store.hook.afterCheck = func() {
		go func() {
			_, err := w.catalog.WriteSource(catalog.SourceWrite{Location: location, Path: "src/main.py", Text: "saved\n", ExpectedSHA256: read.SHA256})
			done <- err
		}()
		select {
		case err := <-done:
			t.Errorf("WriteSource ran inside the apply: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if _, err := w.store.Apply(location, revision); err != nil {
		t.Fatal(err)
	}
	// The save waited for the apply, then found the file it read gone.
	if err := <-done; catalog.KindOf(err) != catalog.KindChanged {
		t.Fatalf("WriteSource after the apply: %v", err)
	}
}
