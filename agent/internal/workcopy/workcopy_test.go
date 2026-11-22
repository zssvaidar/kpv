// The working copy, the review and its revision, the store's place and its
// survival of a restart, discard: every test against a scratch home and
// store, with no route, comparing the rest of the root byte for byte.

package workcopy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/catalog"
)

func TestAWorkingCopyHoldsOnlyTheStagedFilesOutsideTheProject(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	before := snapshot(t, root)
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("print('staged')\n")})
	if !revisionPattern.MatchString(revision) {
		t.Fatalf("revision %q", revision)
	}
	sameTree(t, before, snapshot(t, root))
	files := w.treeFiles()
	if len(files) != 1 || !strings.HasPrefix(files[0], copyPrefix) {
		t.Fatalf("the store holds %v", files)
	}
	stored, err := os.ReadFile(filepath.Join(w.config, TreeName, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"do-not-copy", "ZG8tbm90LWNvcHk", ".env", "print('hi')"} {
		if strings.Contains(string(stored), leak) {
			t.Fatalf("the working copy holds %q, a file nothing staged", leak)
		}
	}
	for path, want := range map[string]os.FileMode{filepath.Join(w.config, TreeName): 0o700, filepath.Join(w.config, TreeName, files[0]): 0o600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatalf("%s: %v %v", path, info, err)
		}
	}
	if strings.HasPrefix(w.config, root) {
		t.Fatal("the store is inside the root")
	}
	review := w.review(location)
	if review.Revision != revision || len(review.Files) != 1 {
		t.Fatalf("review %+v", review)
	}
	file := review.Files[0]
	if file.Path != "src/main.py" || file.Change != Modified || file.State != Ready || string(file.Base) != "print('hi')\n" || string(file.Staged) != "print('staged')\n" {
		t.Fatalf("file %+v", file)
	}
	// The project file is answered as it is on disk.
	source, err := w.catalog.ReadSource(location, "src/main.py")
	if err != nil || string(source.Text) != "print('hi')\n" {
		t.Fatalf("ReadSource = %v, %v", source, err)
	}
}

func TestStagedPathsAndBytesKeepTheSourceRules(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	before := snapshot(t, root)
	refusals := map[ErrorKind][]Change{
		KindInvalid: {
			{Path: "../x", Text: []byte("x")}, {Path: "src/../x", Text: []byte("x")}, {Path: ".env", Text: []byte("x")},
			{Path: "src/.hidden", Text: []byte("x")}, {Path: "/etc/passwd", Text: []byte("x")}, {Path: "src/", Text: []byte("x")},
			{Path: "", Text: []byte("x")}, {Path: "a\\b", Text: []byte("x")}, {Path: strings.Repeat("a", 513), Text: []byte("x")},
			{Path: "x", Text: []byte("x"), Delete: true},
		},
		KindGenerated: {{Path: "generated/x.py", Text: []byte("x")}, {Path: "GENERATED/x.py", Text: []byte("x")}},
		KindReserved:  {{Path: "pipeline.yaml", Delete: true}, {Path: "Pipeline.YAML", Delete: true}},
		KindNotText:   {{Path: "a.bin", Text: []byte("a\x00b")}, {Path: "b.bin", Text: []byte{0xff, 0xfe}}},
		KindTooLarge:  {{Path: "big.txt", Text: []byte(strings.Repeat("a", catalog.MaxFileSize+1))}},
		KindNotFound:  {{Path: "nothing.txt", Delete: true}},
	}
	for kind, changes := range refusals {
		for _, change := range changes {
			_, err := w.store.Stage(location, change)
			_ = kindIs(t, err, kind)
		}
	}
	_, err := w.store.Stage(location, Change{Path: "a", Text: []byte("1")}, Change{Path: "a", Text: []byte("2")})
	_ = kindIs(t, err, KindInvalid)
	if files := w.treeFiles(); len(files) != 0 {
		t.Fatalf("a refused stage wrote %v", files)
	}
	// pipeline.yaml at the top may be staged, because #77 rewrites it.
	w.stage(location, Change{Path: "pipeline.yaml", Text: []byte("id: app\nname: Renamed\n")})
	// More than MaxFiles is refused and the working copy keeps what it had.
	var many []Change
	for index := 0; index < MaxFiles; index++ {
		many = append(many, Change{Path: "new/" + strings.Repeat("f", index+1), Text: []byte("x")})
	}
	_, err = w.store.Stage(location, many...)
	_ = kindIs(t, err, KindTooMany)
	if review := w.review(location); len(review.Files) != 1 {
		t.Fatalf("after tooMany: %+v", review.Files)
	}
	sameTree(t, before, snapshot(t, root))
}

func TestAChangeIsBasedOnTheFileItWasFirstStagedFromAndCanBeUnstaged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, location := w.pipeline()
	w.stage(location, Change{Path: "src/main.py", Text: []byte("one\n")})
	w.stage(location, Change{Path: "src/main.py", Text: []byte("two\n")})
	review := w.review(location)
	if len(review.Files) != 1 || string(review.Files[0].Staged) != "two\n" || string(review.Files[0].Base) != "print('hi')\n" {
		t.Fatalf("review %+v", review.Files)
	}
	// Back to its base, it is no longer staged; so is a new file deleted.
	w.stage(location, Change{Path: "src/main.py", Text: []byte("print('hi')\n")}, Change{Path: "src/new.py", Text: []byte("x")})
	w.stage(location, Change{Path: "src/new.py", Delete: true})
	if review := w.review(location); len(review.Files) != 0 || review.Revision != "" {
		t.Fatalf("review %+v", review)
	}
	if files := w.treeFiles(); len(files) != 0 {
		t.Fatalf("an empty working copy is kept: %v", files)
	}
}

func TestAStageBasedOnAFileThatIsNotTextIsRefusedWithItsKind(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	write(t, filepath.Join(root, "app", "bin.dat"), "a\x00b")
	if err := os.Symlink(filepath.Join(root, "other.txt"), filepath.Join(root, "app", "link.txt")); err != nil {
		t.Fatal(err)
	}
	_, err := w.store.Stage(location, Change{Path: "bin.dat", Text: []byte("x")})
	_ = kindIs(t, err, KindNotText)
	_, err = w.store.Stage(location, Change{Path: "link.txt", Text: []byte("x")})
	_ = kindIs(t, err, KindLink)
	_, err = w.store.Stage(catalog.Location{Root: root, Directory: "missing"}, Change{Path: "a", Text: []byte("x")})
	_ = kindIs(t, err, KindNotFound)
}

func TestTheReviewListsEveryFileSortedWithItsChangeAndWhatTheProjectFileIsNow(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	write(t, filepath.Join(root, "app", "src", "b.py"), "b\n")
	write(t, filepath.Join(root, "app", "src", "c.py"), "c\n")
	w.stage(location,
		Change{Path: "src/main.py", Text: []byte("m\n")},
		Change{Path: "src/b.py", Delete: true},
		Change{Path: "src/a.py", Text: []byte{}},
		Change{Path: "src/c.py", Text: []byte("c2\n")},
		Change{Path: "Z.txt", Text: []byte("z")},
	)
	// The project's c.py stops being text after it was staged.
	write(t, filepath.Join(root, "app", "src", "c.py"), "c\x00")
	review := w.review(location)
	var got []string
	for _, file := range review.Files {
		got = append(got, file.Path+":"+string(file.Change)+":"+string(file.State))
	}
	want := "Z.txt:added:ready,src/a.py:added:ready,src/b.py:deleted:ready,src/c.py:modified:notText,src/main.py:modified:ready"
	if strings.Join(got, ",") != want {
		t.Fatalf("review\n got %s\nwant %s", strings.Join(got, ","), want)
	}
	if review.Files[1].Staged == nil || len(review.Files[1].Staged) != 0 || review.Files[1].Base != nil {
		t.Fatalf("an empty added file: %+v", review.Files[1])
	}
	if string(review.Files[2].Base) != "b\n" || review.Files[2].Staged != nil {
		t.Fatalf("a deletion: %+v", review.Files[2])
	}
	if review.Files[3].Base != nil || review.Files[3].Staged != nil {
		t.Fatalf("a file that is not text has a diff: %+v", review.Files[3])
	}
}

func TestWithNoConfigDirectoryEverythingIsUnavailableAndNothingIsWritten(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.open(false)
	root := filepath.Join(w.home, "project")
	mkdir(t, root)
	if _, err := w.roots.Authorize(root); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(root, "app", "pipeline.yaml"), "id: app\n")
	location := catalog.Location{Root: root, Directory: "app"}
	before := snapshot(t, root)
	_, err := w.store.Stage(location, Change{Path: "a", Text: []byte("x")})
	_ = kindIs(t, err, KindUnavailable)
	_, err = w.store.Review(location)
	_ = kindIs(t, err, KindUnavailable)
	_, err = w.store.List()
	_ = kindIs(t, err, KindUnavailable)
	_, err = w.store.Apply(location, strings.Repeat("0", 32))
	_ = kindIs(t, err, KindUnavailable)
	_, err = w.store.Restore(location, "20260101T000000Z-00000000")
	_ = kindIs(t, err, KindUnavailable)
	_, err = w.store.Discard(location, strings.Repeat("0", 32))
	_ = kindIs(t, err, KindUnavailable)
	_ = kindIs(t, w.store.RemoveSnapshot(location, "20260101T000000Z-00000000"), KindUnavailable)
	sameTree(t, before, snapshot(t, root))
	if _, err := os.Stat(w.config); !os.IsNotExist(err) {
		t.Fatalf("something was written to the config directory: %v", err)
	}
}

func TestAWorkingCopySurvivesARestart(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, location := w.pipeline()
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("print('staged')\r\n")}, Change{Path: "src/new.py", Text: []byte("new")})
	w.open(true)
	review := w.review(location)
	if review.Revision != revision || len(review.Files) != 2 || string(review.Files[0].Staged) != "print('staged')\r\n" || string(review.Files[1].Staged) != "new" {
		t.Fatalf("after a restart: %+v", review)
	}
}

func TestACorruptWorkingCopyIsMovedAsideAndThePipelineHasNoStagedChanges(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, location := w.pipeline()
	w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
	name := copyName(location)
	if err := os.WriteFile(filepath.Join(w.config, TreeName, name), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	w.open(true)
	review := w.review(location)
	if !review.Unreadable || len(review.Files) != 0 || review.Revision != "" {
		t.Fatalf("review of a corrupt store: %+v", review)
	}
	moved, _ := filepath.Glob(filepath.Join(w.config, TreeName, name+".corrupt-*"))
	if len(moved) != 1 {
		t.Fatalf("moved aside: %v", moved)
	}
	if !strings.Contains(w.logs.String(), "kind=corrupt") {
		t.Fatalf("not logged by kind: %s", w.logs.String())
	}
	list, err := w.store.List()
	if err != nil || len(list.Copies) != 0 || list.Unreadable != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	// Staging again starts a new working copy, and the page stops saying so.
	w.stage(location, Change{Path: "src/main.py", Text: []byte("y")})
	if review := w.review(location); review.Unreadable || len(review.Files) != 1 {
		t.Fatalf("after staging again: %+v", review)
	}
}

func TestATamperedEntryIsRefusedWhenItIsReadAndNeverFollowed(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"../x", "../../outside.txt", "src/.hidden", ".env", "/etc/passwd", "generated/x.py", "src//x"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t)
			root, location := w.pipeline()
			revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
			file := filepath.Join(w.config, TreeName, copyName(location))
			raw, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			tampered := strings.Replace(string(raw), `"path":"src/main.py"`, `"path":"`+path+`"`, 1)
			if tampered == string(raw) {
				t.Fatal("the entry was not tampered")
			}
			if err := os.WriteFile(file, []byte(tampered), 0o600); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(w.home, "outside.txt")
			write(t, outside, "outside\n")
			before := snapshot(t, w.home)
			delete(before, filepath.Join(".config", "kpv", TreeName, copyName(location)))
			_, err = w.store.Apply(location, revision)
			_ = kindIs(t, err, KindChanged)
			review := w.review(location)
			if !review.Unreadable || len(review.Files) != 0 {
				t.Fatalf("review %+v", review)
			}
			after := snapshot(t, w.home)
			for rel := range after {
				if strings.Contains(rel, ".corrupt-") {
					delete(after, rel)
				}
			}
			sameTree(t, before, after)
			if !strings.Contains(w.logs.String(), "kind=invalidPath") && !strings.Contains(w.logs.String(), "kind=corrupt") {
				t.Fatalf("not reported by kind: %s", w.logs.String())
			}
			_ = root
		})
	}
}

func TestAWorkingCopyWhosePipelineIsGoneIsListedAndCanBeDiscardedButNotApplied(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
	if err := os.Rename(filepath.Join(root, "app"), filepath.Join(root, "renamed")); err != nil {
		t.Fatal(err)
	}
	list, err := w.store.List()
	if err != nil || len(list.Copies) != 1 {
		t.Fatalf("List = %+v, %v", list, err)
	}
	entry := list.Copies[0]
	if entry.Root != root || entry.Directory != "app" || entry.Available || entry.Reason != KindNotFound || entry.Files != 1 || entry.Revision != revision {
		t.Fatalf("an orphaned working copy: %+v", entry)
	}
	before := snapshot(t, root)
	_, err = w.store.Apply(location, revision)
	_ = kindIs(t, err, KindNotFound)
	if _, err := w.roots.Revoke(root); err != nil {
		t.Fatal(err)
	}
	list, _ = w.store.List()
	if list.Copies[0].Available || list.Copies[0].Reason != KindNotAuthorized {
		t.Fatalf("after revoke: %+v", list.Copies[0])
	}
	count, err := w.store.Discard(location, revision)
	if err != nil || count != 1 {
		t.Fatalf("Discard = %d, %v", count, err)
	}
	if list, _ := w.store.List(); len(list.Copies) != 0 {
		t.Fatalf("after discard: %+v", list)
	}
	sameTree(t, before, snapshot(t, root))
}

func TestApplyWithAnOlderRevisionWritesNothingAndAnswersChanged(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	drawn := w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
	w.stage(location, Change{Path: "src/other.py", Text: []byte("y")})
	before := snapshot(t, root)
	_, err := w.store.Apply(location, drawn)
	_ = kindIs(t, err, KindChanged)
	sameTree(t, before, snapshot(t, root))
	// Discarded and staged again, an old revision is still not the new one.
	current := w.review(location).Revision
	if _, err := w.store.Discard(location, drawn); KindOf(err) != KindChanged {
		t.Fatalf("Discard with an old revision: %v", err)
	}
	if _, err := w.store.Discard(location, current); err != nil {
		t.Fatal(err)
	}
	w.stage(location, Change{Path: "src/main.py", Text: []byte("x")})
	_, err = w.store.Apply(location, current)
	_ = kindIs(t, err, KindChanged)
	sameTree(t, before, snapshot(t, root))
}

func TestDiscardRemovesTheWorkingCopyAndLeavesTheRootByteForByte(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	before := snapshot(t, root)
	revision := w.stage(location,
		Change{Path: "src/main.py", Text: []byte("x")},
		Change{Path: "pipeline.yaml", Text: []byte("id: app\nname: x\n")},
		Change{Path: "new/dir/file.py", Text: []byte("n")},
	)
	count, err := w.store.Discard(location, revision)
	if err != nil || count != 3 {
		t.Fatalf("Discard = %d, %v", count, err)
	}
	sameTree(t, before, snapshot(t, root))
	if review := w.review(location); len(review.Files) != 0 || review.Revision != "" {
		t.Fatalf("after discard: %+v", review)
	}
	if files := w.treeFiles(); len(files) != 0 {
		t.Fatalf("the store still holds %v", files)
	}
}

func TestADirectSaveOfAStagedFileIsAllowedAndShowsAsDriftAtApply(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	root, location := w.pipeline()
	revision := w.stage(location, Change{Path: "src/main.py", Text: []byte("staged\n")})
	read, err := w.catalog.ReadSource(location, "src/main.py")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.catalog.WriteSource(catalog.SourceWrite{Location: location, Path: "src/main.py", Text: "typed\n", ExpectedSHA256: read.SHA256}); err != nil {
		t.Fatalf("a direct save of a staged file: %v", err)
	}
	before := snapshot(t, root)
	_, err = w.store.Apply(location, revision)
	fault := kindIs(t, err, KindDrift)
	if len(fault.Drift) != 1 || fault.Drift[0] != (Drift{Path: "src/main.py", Now: Changed}) {
		t.Fatalf("drift %+v", fault.Drift)
	}
	sameTree(t, before, snapshot(t, root))
	if got, _ := os.ReadFile(filepath.Join(root, "app", "src", "main.py")); string(got) != "typed\n" {
		t.Fatalf("the saved file was overwritten: %q", got)
	}
}
