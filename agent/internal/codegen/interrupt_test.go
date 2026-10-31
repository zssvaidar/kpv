package codegen

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

var three = map[string]string{"a/x.txt": "x1", "b/y.txt": "y1", "c/z.txt": "z1"}
var threeChanged = map[string]string{"a/x.txt": "x2", "b/y.txt": "y2", "c/z.txt": "z2"}

func TestAFailingGeneratorWritesNothing(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), emitting(three))
	before := p.whole(t)
	cause := errors.New("template exploded")
	broken := GeneratorFunc("broken", func(*pipeline.Pipeline) ([]File, error) { return nil, cause })

	report, err := Run(context.Background(), p.dir, model("orders"), emitting(threeChanged), broken)

	problem := problemOf(t, report, err)
	if problem.Kind != ProblemGeneratorFailed || problem.Generator != "broken" || !errors.Is(err, cause) {
		t.Fatalf("problem %+v err %v", problem, err)
	}
	sameSnapshot(t, "the whole tree", before, p.whole(t))
}

func TestAWriteFailingPartWayReportsWhatWasWrittenAndKeepsTheRestsPreviousBytes(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	p := newProject(t)
	runOK(t, p, model("orders"), emitting(three))
	before := p.outside(t)
	// A disk error, as far as the run can tell: b/ cannot be written.
	if err := os.Chmod(p.generated("b"), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.generated("b"), 0o755) })

	report, err := Run(context.Background(), p.dir, model("orders"), emitting(threeChanged))

	if problemOf(t, report, err).Kind != ProblemWriteFailed {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusFailed, "c/z.txt": StatusNotWritten})
	if kind := entryOf(t, report, "b/y.txt").Kind; kind != projectfs.KindUnreadable {
		t.Fatalf("kind %s", kind)
	}
	if read(t, p.generated("a", "x.txt")) != "x2" || read(t, p.generated("b", "y.txt")) != "y1" || read(t, p.generated("c", "z.txt")) != "z1" {
		t.Fatal("a file it did not write lost its previous bytes")
	}
	// What it did write is recorded, so changing the model again is not
	// taken for an edit.
	document := readManifestFile(t, p)
	if !report.ManifestWritten || document.Files[0].SHA256 != hashOf([]byte("x2")) || document.Files[1].SHA256 != hashOf([]byte("y1")) {
		t.Fatalf("manifest %+v", document)
	}
	assertNoTemporaries(t, p)
	sameSnapshot(t, "the tree outside generated/", before, p.outside(t))

	if err := os.Chmod(p.generated("b"), 0o755); err != nil {
		t.Fatal(err)
	}
	wantStatuses(t, runOK(t, p, model("orders"), emitting(map[string]string{"a/x.txt": "x3", "b/y.txt": "y3", "c/z.txt": "z3"})),
		map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusWritten, "c/z.txt": StatusWritten})
}

func TestARootRevokedPartWayStopsTheRunWithTheGuardsForbidden(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), emitting(three))
	before := p.outside(t)
	revoke := hooks{beforeWrite: func(path string) {
		if path == "b/y.txt" {
			if _, err := p.guard.Revoke(p.root); err != nil {
				t.Error(err)
			}
		}
	}}

	report, err := run(context.Background(), p.dir, model("orders"), []Generator{emitting(threeChanged)}, revoke)

	problem := problemOf(t, report, err)
	if problem.Kind != ProblemWriteFailed || problem.GuardKind != projectfs.KindForbidden {
		t.Fatalf("problem %+v", problem)
	}
	wantStatuses(t, report, map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusFailed, "c/z.txt": StatusNotWritten})
	if entryOf(t, report, "b/y.txt").Kind != projectfs.KindForbidden {
		t.Fatalf("report %+v", report.Files)
	}
	if read(t, p.generated("a", "x.txt")) != "x2" || read(t, p.generated("b", "y.txt")) != "y1" || read(t, p.generated("c", "z.txt")) != "z1" {
		t.Fatal("a file it did not write lost its previous bytes")
	}
	if report.ManifestWritten {
		t.Fatal("a manifest was written through a revoked root")
	}
	sameSnapshot(t, "the tree outside generated/", before, p.outside(t))
}

func TestACancelledRunStopsBetweenFiles(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), emitting(three))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stop := hooks{beforeWrite: func(path string) {
		if path == "a/x.txt" {
			cancel()
		}
	}}
	report, err := run(ctx, p.dir, model("orders"), []Generator{emitting(threeChanged)}, stop)
	if problemOf(t, report, err).Kind != ProblemInterrupted || !errors.Is(err, context.Canceled) {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusNotWritten, "c/z.txt": StatusNotWritten})
	if read(t, p.generated("b", "y.txt")) != "y1" {
		t.Fatal("previous bytes lost")
	}
}

func TestARunInterruptedBeforeItsManifestIsUnchangedNextTimeAndRecorded(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	p := newProject(t)
	runOK(t, p, model("orders"), emitting(three))
	manifest := read(t, p.generated(ManifestName))
	// The files are in subdirectories that can be written; the manifest is
	// in generated/, which cannot, so the run gets as far as its files and
	// no further.
	if err := os.Chmod(p.generated(), 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.generated(), 0o755) })

	report, err := Run(context.Background(), p.dir, model("orders"), emitting(threeChanged))
	if problemOf(t, report, err).Kind != ProblemWriteFailed || report.ManifestWritten {
		t.Fatalf("report %+v", report)
	}
	wantStatuses(t, report, map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusWritten, "c/z.txt": StatusWritten})
	if read(t, p.generated(ManifestName)) != manifest {
		t.Fatal("the old manifest changed")
	}
	if err := os.Chmod(p.generated(), 0o755); err != nil {
		t.Fatal(err)
	}

	report = runOK(t, p, model("orders"), emitting(threeChanged))

	wantStatuses(t, report, map[string]Status{"a/x.txt": StatusUnchanged, "b/y.txt": StatusUnchanged, "c/z.txt": StatusUnchanged})
	document := readManifestFile(t, p)
	for i, path := range []string{"a/x.txt", "b/y.txt", "c/z.txt"} {
		if document.Files[i].SHA256 != hashOf([]byte(threeChanged[path])) {
			t.Fatalf("manifest %+v", document)
		}
	}
}

func TestATemporaryFileLeftByAnInterruptedWriteIsNeverListedAndIsRemoved(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake, emitting(map[string]string{"pkg/x.go": "x"}))
	leftovers := []string{
		p.generated(".pipeline.txt.tmp-0123456789abcdef"),
		p.generated("pkg", ".x.go.tmp-fedcba9876543210"),
		p.generated("." + ManifestName + ".tmp-00000000deadbeef"),
	}
	for _, leftover := range leftovers {
		write(t, leftover, "half a file")
	}
	// Shaped like one, for a file this package does not write: the user's.
	users := p.generated(".notes.md.tmp-0123456789abcdef")
	write(t, users, "mine")

	report := runOK(t, p, model("orders"), fake, emitting(map[string]string{"pkg/x.go": "x"}))

	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusUnchanged, "pkg/x.go": StatusUnchanged})
	for _, leftover := range leftovers {
		if exists(leftover) {
			t.Fatalf("%s was not removed", leftover)
		}
	}
	if read(t, users) != "mine" {
		t.Fatal("a user file was removed")
	}
	if got := strings.Join(manifestPaths(t, p), " "); got != "pipeline.txt pkg/x.go" {
		t.Fatalf("manifest %s", got)
	}
}

func assertNoTemporaries(t *testing.T, p *project) {
	t.Helper()
	for path := range snapshot(t, p.generated()) {
		if _, temporary := temporaryBase(path[strings.LastIndexByte(path, '/')+1:]); temporary {
			t.Fatalf("a temporary file was left: %s", path)
		}
	}
}
