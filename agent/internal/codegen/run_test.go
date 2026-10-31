package codegen

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

func TestTheFakeGeneratorsFirstRunCreatesGeneratedAndRecordsTheFile(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	before := p.outside(t)

	report := runOK(t, p, model("orders"), fake)

	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusWritten})
	if !report.ManifestWritten {
		t.Fatal("the first run did not write a manifest")
	}
	info, err := os.Lstat(p.generated())
	if err != nil || !info.IsDir() {
		t.Fatalf("generated/ is not a directory: %v %v", info, err)
	}
	if got := read(t, p.generated("pipeline.txt")); got != fakeContent("orders") {
		t.Fatalf("content %q", got)
	}
	document := readManifestFile(t, p)
	if document.Version != ManifestVersion || len(document.Files) != 1 ||
		document.Files[0] != (manifestEntry{Path: "pipeline.txt", SHA256: hashOf([]byte(fakeContent("orders")))}) {
		t.Fatalf("manifest %+v", document)
	}
	sameSnapshot(t, "the tree outside generated/", before, p.outside(t))
}

func TestARunWithNoOutputsStillCreatesGeneratedAndAnEmptyManifest(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	report := runOK(t, p, model("orders"))
	if len(report.Files) != 0 || !report.ManifestWritten {
		t.Fatalf("report %+v", report)
	}
	if got := read(t, p.generated(ManifestName)); got != "{\n  \"version\": 1,\n  \"files\": []\n}\n" {
		t.Fatalf("manifest %q", got)
	}
}

func TestRunningTwiceLeavesGeneratedByteIdenticalAndReportsEveryFileUnchanged(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	generator := emitting(map[string]string{"b.txt": "b\n", "a/z.txt": "z\n", "a/b/c.txt": "c\n"})
	runOK(t, p, model("orders"), generator, fake)
	before := p.whole(t)
	manifest := read(t, p.generated(ManifestName))

	report := runOK(t, p, model("orders"), generator, fake)

	wantStatuses(t, report, map[string]Status{
		"a/b/c.txt": StatusUnchanged, "a/z.txt": StatusUnchanged, "b.txt": StatusUnchanged, "pipeline.txt": StatusUnchanged,
	})
	if report.ManifestWritten {
		t.Fatal("a run with nothing to change rewrote the manifest")
	}
	sameSnapshot(t, "the whole tree", before, p.whole(t))
	if got := read(t, p.generated(ManifestName)); got != manifest {
		t.Fatalf("manifest changed:\n%s\n%s", manifest, got)
	}
}

func TestTheManifestIsSortedAndTheSameSetIsTheSameBytes(t *testing.T) {
	t.Parallel()
	files := map[string]string{"z.txt": "1", "a.txt": "2", "m/n.txt": "3", "B.txt": "4"}
	first, second := newProject(t), newProject(t)
	runOK(t, first, model("orders"), emitting(files))
	// A different generator order and a different map order, same set.
	runOK(t, second, model("orders"), emitting(map[string]string{"m/n.txt": "3"}), emitting(map[string]string{"B.txt": "4", "z.txt": "1", "a.txt": "2"}))
	if read(t, first.generated(ManifestName)) != read(t, second.generated(ManifestName)) {
		t.Fatal("the same set of files gave two manifests")
	}
	if got := strings.Join(manifestPaths(t, first), " "); got != "B.txt a.txt m/n.txt z.txt" {
		t.Fatalf("manifest order %s", got)
	}
}

func TestANewModelOverwritesAGeneratedFileTheUserDidNotTouch(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	report := runOK(t, p, model("payments"), fake)
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusWritten})
	if got := read(t, p.generated("pipeline.txt")); got != fakeContent("payments") {
		t.Fatalf("content %q", got)
	}
	if readManifestFile(t, p).Files[0].SHA256 != hashOf([]byte(fakeContent("payments"))) {
		t.Fatal("the manifest does not record the new hash")
	}
}

func TestAnEditedGeneratedFileIsReportedModifiedAndNotOverwritten(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	write(t, p.generated("pipeline.txt"), "// my edit\n")
	before := p.whole(t)

	report, err := Run(context.Background(), p.dir, model("payments"), fake)

	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusModified})
	sameSnapshot(t, "the whole tree", before, p.whole(t))
}

func TestAnEditedFileThatAlreadyHoldsTheNewBytesIsUnchangedAndRecorded(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	write(t, p.generated("pipeline.txt"), fakeContent("payments"))

	report := runOK(t, p, model("payments"), fake)

	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusUnchanged})
	if readManifestFile(t, p).Files[0].SHA256 != hashOf([]byte(fakeContent("payments"))) {
		t.Fatal("the manifest does not record what is on disk")
	}
}

func TestChangedLineEndingsAreAModification(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	generator := emitting(map[string]string{"main.go": "package main\n\nfunc main() {}\n"})
	runOK(t, p, model("orders"), generator)
	// What git's autocrlf does on checkout.
	write(t, p.generated("main.go"), "package main\r\n\r\nfunc main() {}\r\n")

	report, err := Run(context.Background(), p.dir, model("orders"), emitting(map[string]string{"main.go": "package main\n"}))
	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"main.go": StatusModified})

	// A dropped trailing newline is a modification too.
	write(t, p.generated("main.go"), "package main\n\nfunc main() {}")
	report, err = Run(context.Background(), p.dir, model("orders"), emitting(map[string]string{"main.go": "package main\n"}))
	if err == nil || statuses(report)["main.go"] != StatusModified {
		t.Fatalf("report %+v", report)
	}
}

func TestADeletedGeneratedFileIsReportedAndRegenerated(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	if err := os.Remove(p.generated("pipeline.txt")); err != nil {
		t.Fatal(err)
	}
	report := runOK(t, p, model("orders"), fake)
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusRegenerated})
	if read(t, p.generated("pipeline.txt")) != fakeContent("orders") {
		t.Fatal("not regenerated")
	}

	// A deleted directory of generated files is regenerated too.
	nested := emitting(map[string]string{"deep/er/x.txt": "x"})
	runOK(t, p, model("orders"), nested)
	if err := os.RemoveAll(p.generated("deep")); err != nil {
		t.Fatal(err)
	}
	report = runOK(t, p, model("orders"), nested)
	wantStatuses(t, report, map[string]Status{"deep/er/x.txt": StatusRegenerated, "pipeline.txt": StatusOrphaned})
}

func TestAnOrphanStaysOnDiskAndInTheManifestUntilSomeoneElseDeletesIt(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	both := emitting(map[string]string{"keep.txt": "k", "old/drop.txt": "d"})
	only := emitting(map[string]string{"keep.txt": "k"})
	runOK(t, p, model("orders"), both)

	for run := 0; run < 2; run++ {
		report := runOK(t, p, model("orders"), only)
		wantStatuses(t, report, map[string]Status{"keep.txt": StatusUnchanged, "old/drop.txt": StatusOrphaned})
		if read(t, p.generated("old", "drop.txt")) != "d" {
			t.Fatal("the orphan was touched")
		}
		document := readManifestFile(t, p)
		if len(document.Files) != 2 || document.Files[1].Path != "old/drop.txt" || !document.Files[1].Orphaned ||
			document.Files[1].SHA256 != hashOf([]byte("d")) {
			t.Fatalf("manifest %+v", document)
		}
	}

	// An orphan the user edited is still only reported, never removed.
	write(t, p.generated("old", "drop.txt"), "edited")
	wantStatuses(t, runOK(t, p, model("orders"), only), map[string]Status{"keep.txt": StatusUnchanged, "old/drop.txt": StatusOrphaned})

	if err := os.Remove(p.generated("old", "drop.txt")); err != nil {
		t.Fatal(err)
	}
	report := runOK(t, p, model("orders"), only)
	wantStatuses(t, report, map[string]Status{"keep.txt": StatusUnchanged})
	if got := manifestPaths(t, p); len(got) != 1 || got[0] != "keep.txt" {
		t.Fatalf("manifest %v", got)
	}

	// An orphan that is emitted again, edited, is modified, not overwritten.
	runOK(t, p, model("orders"), both)
	runOK(t, p, model("orders"), only)
	write(t, p.generated("old", "drop.txt"), "edited")
	report, err := Run(context.Background(), p.dir, model("orders"), both)
	if err == nil || statuses(report)["old/drop.txt"] != StatusModified {
		t.Fatalf("report %+v", report)
	}
}

func TestAUserFileUnderGeneratedThatNothingEmitsIsLeftAloneAndNotReported(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.generated("notes.md"), "mine")
	write(t, p.generated("sub", "mine.txt"), "mine")
	report := runOK(t, p, model("orders"), fake)
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusWritten})
	if read(t, p.generated("notes.md")) != "mine" || read(t, p.generated("sub", "mine.txt")) != "mine" {
		t.Fatal("a user file was touched")
	}
	if got := manifestPaths(t, p); len(got) != 1 {
		t.Fatalf("manifest %v", got)
	}
	wantStatuses(t, runOK(t, p, model("orders"), fake), map[string]Status{"pipeline.txt": StatusUnchanged})
}

func TestAFileTheManifestDoesNotListIsUnownedUnlessItAlreadyHoldsTheBytes(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	write(t, p.generated("hand.txt"), "written by hand")
	write(t, p.generated("same.txt"), "same")
	before := p.whole(t)

	report, err := Run(context.Background(), p.dir, model("orders"), fake, emitting(map[string]string{"hand.txt": "generated", "same.txt": "same"}))
	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"hand.txt": StatusUnowned, "pipeline.txt": StatusUnchanged, "same.txt": StatusUnchanged})
	sameSnapshot(t, "the whole tree", before, p.whole(t))

	// Without the conflict, the identical file is adopted into the manifest.
	report = runOK(t, p, model("orders"), fake, emitting(map[string]string{"same.txt": "same"}))
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusUnchanged, "same.txt": StatusUnchanged})
	if got := strings.Join(manifestPaths(t, p), " "); got != "pipeline.txt same.txt" {
		t.Fatalf("manifest %s", got)
	}
}

func TestWithNoManifestAnEmptyGeneratedIsAFirstRunAndExistingFilesAreUnowned(t *testing.T) {
	t.Parallel()
	empty := newProject(t)
	if err := os.Mkdir(empty.generated(), 0o755); err != nil {
		t.Fatal(err)
	}
	wantStatuses(t, runOK(t, empty, model("orders"), fake), map[string]Status{"pipeline.txt": StatusWritten})

	full := newProject(t)
	write(t, full.generated("pipeline.txt"), "from before the manifest")
	write(t, full.generated("other.txt"), "from before the manifest")
	before := full.whole(t)
	report, err := Run(context.Background(), full.dir, model("orders"), fake, emitting(map[string]string{"other.txt": "new"}))
	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"other.txt": StatusUnowned, "pipeline.txt": StatusUnowned})
	sameSnapshot(t, "the whole tree", before, full.whole(t))
}

func TestARunWithConflictsWritesNothingAndReportsEveryConflict(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	generator := emitting(map[string]string{"a.txt": "a", "b.txt": "b", "c.txt": "c", "d.txt": "d", "e/f.txt": "f", "new.txt": "n"})
	runOK(t, p, model("orders"), generator)
	write(t, p.generated("a.txt"), "edited")
	if err := os.Remove(p.generated("b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(p.generated("b.txt"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, p.generated("hand.txt"), "mine")
	if err := os.RemoveAll(p.generated("e")); err != nil {
		t.Fatal(err)
	}
	write(t, p.generated("e"), "a file where a directory was")
	if err := os.Remove(p.generated("d.txt")); err != nil {
		t.Fatal(err)
	}
	before := p.whole(t)

	changed := emitting(map[string]string{"a.txt": "A", "b.txt": "B", "c.txt": "C", "d.txt": "D", "e/f.txt": "F", "hand.txt": "H", "new.txt": "N", "brand.txt": "x"})
	report, err := Run(context.Background(), p.dir, model("orders"), changed)

	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{
		"a.txt": StatusModified, "b.txt": StatusNotRegularFile, "brand.txt": StatusNotWritten, "c.txt": StatusNotWritten,
		"d.txt": StatusNotWritten, "e/f.txt": StatusBlocked, "hand.txt": StatusUnowned, "new.txt": StatusNotWritten,
	})
	if blocked := entryOf(t, report, "e/f.txt"); blocked.Reason != ReasonDirectoryNotDirectory || blocked.At != "e" {
		t.Fatalf("blocked %+v", blocked)
	}
	sameSnapshot(t, "the whole tree", before, p.whole(t))
}

func TestAReportNeverHoldsFileContents(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	secret := "content-that-must-not-be-reported"
	runOK(t, p, model("orders"), emitting(map[string]string{"x.txt": secret + "1"}))
	write(t, p.generated("x.txt"), secret+"2")
	report, err := Run(context.Background(), p.dir, model("orders"), emitting(map[string]string{"x.txt": secret + "3"}))
	if err == nil {
		t.Fatal("want a conflict")
	}
	for _, text := range []string{err.Error(), strings.ReplaceAll(strings.ReplaceAll(stringOf(report), "\n", ""), " ", "")} {
		if strings.Contains(text, secret) {
			t.Fatalf("contents reported: %s", text)
		}
	}
}

func stringOf(report Report) string {
	out := ""
	for _, file := range report.Files {
		out += file.Path + string(file.Status) + string(file.Reason) + file.At + string(file.Kind)
	}
	return out
}

func TestANilModelIsRefused(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	report, err := Run(context.Background(), p.dir, nil, fake)
	if problemOf(t, report, err).Kind != ProblemInvalidModel || exists(p.generated()) {
		t.Fatalf("report %+v", report)
	}
}

func TestAGeneratorHoldingOnToItsBytesCannotChangeWhatIsWritten(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	var kept []byte
	generator := GeneratorFunc("sneaky", func(*pipeline.Pipeline) ([]File, error) {
		kept = []byte("original")
		return []File{{Path: "x.txt", Content: kept}}, nil
	})
	unlockAfter := hooks{beforeWrite: func(string) { copy(kept, "CHANGED!") }}
	if _, err := run(context.Background(), p.dir, model("orders"), []Generator{generator}, unlockAfter); err != nil {
		t.Fatal(err)
	}
	if read(t, p.generated("x.txt")) != "original" {
		t.Fatal("the generator's later change was written")
	}
}
