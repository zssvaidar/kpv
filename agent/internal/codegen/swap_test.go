package codegen

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// layered is what the swap tests generate: a file in generated/, one in
// generated/sub and one in generated/sub/deep, with content that depends on
// the model so a second run has something to write in each.
func layered(id string) Generator {
	return emitting(map[string]string{
		"pipeline.txt":   "top " + id,
		"sub/x.txt":      "x " + id,
		"sub/deep/y.txt": "y " + id,
	})
}

// relative is a snapshot of dir keyed by the path inside it, so a directory
// that has moved can be compared with what it held before.
func relative(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for path, description := range snapshot(t, dir) {
		out[strings.TrimPrefix(path, dir)] = description
	}
	return out
}

// Each directory a run writes into, renamed away and replaced by a link to
// src/ after the run checked it, is never written through: the run keeps
// writing into the directory it checked, in its new place, and src/ is byte
// for byte what it was.
func TestADirectorySwappedForALinkDuringARunIsNeverWrittenThrough(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		// swapped is the directory replaced, relative to the pipeline
		// directory, and link is what the link put there holds.
		swapped, link func(p *project) string
		// moved is where generated/ is after the swap.
		generated func(p *project) string
		// src is where the user's src/ is after the swap.
		src func(p *project) string
	}{
		"the pipeline directory": {
			swapped:   func(p *project) string { return p.pipe },
			link:      func(p *project) string { return filepath.Join(p.pipe+".moved", SourceDir) },
			generated: func(p *project) string { return filepath.Join(p.pipe+".moved", GeneratedDir) },
			src:       func(p *project) string { return filepath.Join(p.pipe+".moved", SourceDir) },
		},
		"generated/": {
			swapped:   func(p *project) string { return p.generated() },
			link:      func(*project) string { return SourceDir },
			generated: func(p *project) string { return p.generated() + ".moved" },
			src:       func(p *project) string { return filepath.Join(p.pipe, SourceDir) },
		},
		"generated/sub": {
			swapped:   func(p *project) string { return p.generated("sub") },
			link:      func(*project) string { return filepath.Join("..", "..", SourceDir) },
			generated: func(p *project) string { return p.generated() },
			src:       func(p *project) string { return filepath.Join(p.pipe, SourceDir) },
		},
		"generated/sub/deep": {
			swapped:   func(p *project) string { return p.generated("sub", "deep") },
			link:      func(*project) string { return filepath.Join("..", "..", "..", SourceDir) },
			generated: func(p *project) string { return p.generated() },
			src:       func(p *project) string { return filepath.Join(p.pipe, SourceDir) },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			runOK(t, p, model("a"), layered("a"))
			src := relative(t, filepath.Join(p.pipe, SourceDir))
			swapped := false
			swap := hooks{beforeWrite: func(string) {
				if swapped {
					return
				}
				swapped = true
				path := c.swapped(p)
				if err := os.Rename(path, path+".moved"); err != nil {
					t.Error(err)
					return
				}
				if err := os.Symlink(c.link(p), path); err != nil {
					t.Error(err)
				}
			}}

			report, err := run(context.Background(), p.dir, model("b"), []Generator{layered("b")}, swap)

			if err != nil || !swapped {
				t.Fatalf("run: %v (swapped %v); report %+v", err, swapped, report)
			}
			sameSnapshot(t, "src/", src, relative(t, c.src(p)))
			if target, err := os.Readlink(c.swapped(p)); err != nil || target != c.link(p) {
				t.Fatalf("the link is %q, %v", target, err)
			}
			// The writes went to the directories that were checked,
			// wherever they now are.
			generated := c.generated(p)
			deep := filepath.Join(generated, "sub", "deep")
			sub := filepath.Join(generated, "sub")
			switch name {
			case "generated/sub":
				sub += ".moved"
				deep = filepath.Join(sub, "deep")
			case "generated/sub/deep":
				deep += ".moved"
			}
			if read(t, filepath.Join(generated, "pipeline.txt")) != "top b" || read(t, filepath.Join(sub, "x.txt")) != "x b" || read(t, filepath.Join(deep, "y.txt")) != "y b" {
				t.Fatal("a write did not land in the directory that was checked")
			}
			wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusWritten, "sub/x.txt": StatusWritten, "sub/deep/y.txt": StatusWritten})
			if !report.ManifestWritten || !strings.Contains(read(t, filepath.Join(generated, ManifestName)), hashOf([]byte("y b"))) {
				t.Fatal("the manifest in the checked generated/ does not describe what was written")
			}
		})
	}
}

// A directory renamed, not replaced, during a run keeps receiving the run's
// writes in its new place, because it is the directory that was checked; the
// report and the manifest describe that directory.
func TestADirectoryRenamedDuringARunKeepsReceivingItsWrites(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("a"), layered("a"))
	before := p.outside(t)
	renamed := false
	rename := hooks{beforeWrite: func(string) {
		if !renamed {
			renamed = true
			if err := os.Rename(p.generated("sub"), p.generated("elsewhere")); err != nil {
				t.Error(err)
			}
		}
	}}

	report, err := run(context.Background(), p.dir, model("b"), []Generator{layered("b")}, rename)

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusWritten, "sub/x.txt": StatusWritten, "sub/deep/y.txt": StatusWritten})
	if read(t, p.generated("elsewhere", "x.txt")) != "x b" || read(t, p.generated("elsewhere", "deep", "y.txt")) != "y b" {
		t.Fatal("the renamed directory did not receive the writes")
	}
	if exists(p.generated("sub")) {
		t.Fatal("a new sub/ was made at the old name")
	}
	if got := strings.Join(manifestPaths(t, p), " "); got != "pipeline.txt sub/deep/y.txt sub/x.txt" {
		t.Fatalf("manifest %s", got)
	}
	sameSnapshot(t, "the tree outside generated/", before, p.outside(t))

	// The next run finds sub/ gone and regenerates it there.
	wantStatuses(t, runOK(t, p, model("b"), layered("b")),
		map[string]Status{"pipeline.txt": StatusUnchanged, "sub/x.txt": StatusRegenerated, "sub/deep/y.txt": StatusRegenerated})
}

// A real race, no hooks: a goroutine flips generated/ and generated/sub
// between a directory and a link to src/ while runs loop. Runs may fail or
// refuse; none of them may write into src/.
func TestRunsRacingALinkFlipperNeverWriteIntoSrc(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("a"), layered("a"))
	srcDir := filepath.Join(p.pipe, SourceDir)
	src := relative(t, srcDir)

	done := make(chan struct{})
	var flipper sync.WaitGroup
	flipper.Add(1)
	go func() {
		defer flipper.Done()
		flip := func(path, link string) {
			if os.Rename(path, path+".real") != nil {
				return
			}
			_ = os.Symlink(link, path)
			_ = os.Remove(path)
			_ = os.Rename(path+".real", path)
		}
		for {
			select {
			case <-done:
				return
			default:
			}
			flip(p.generated(), SourceDir)
			flip(p.generated("sub"), filepath.Join("..", "..", SourceDir))
		}
	}()

	const iterations = 200
	for i := 0; i < iterations; i++ {
		id := "a"
		if i%2 == 1 {
			id = "b"
		}
		_, _ = Run(context.Background(), p.dir, model(id), layered(id))
	}
	close(done)
	flipper.Wait()

	sameSnapshot(t, "src/", src, relative(t, srcDir))
}

// A run that fails part-way carries over the previous entry for a listed file
// it could not regenerate. The next run reports it regenerated, and a file
// put at that path in the meantime is not overwritten on the strength of the
// carried-over entry.
func TestAFailedRunsManifestCarriesOverAnEntryItCouldNotRegenerate(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	for name, between := range map[string]func(t *testing.T, p *project) map[string]Status{
		"left missing": func(*testing.T, *project) map[string]Status {
			return map[string]Status{"a/x.txt": StatusUnchanged, "b/y.txt": StatusRegenerated, "c/z.txt": StatusWritten}
		},
		"a user file put there": func(t *testing.T, p *project) map[string]Status {
			write(t, p.generated("b", "y.txt"), "the user's")
			return map[string]Status{"a/x.txt": StatusUnchanged, "b/y.txt": StatusModified, "c/z.txt": StatusNotWritten}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			runOK(t, p, model("orders"), emitting(three))
			if err := os.Remove(p.generated("b", "y.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(p.generated("b"), 0o555); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(p.generated("b"), 0o755) })

			report, err := Run(context.Background(), p.dir, model("orders"), emitting(threeChanged))

			if problemOf(t, report, err).Kind != ProblemWriteFailed {
				t.Fatalf("problem %+v", report.Problem)
			}
			wantStatuses(t, report, map[string]Status{"a/x.txt": StatusWritten, "b/y.txt": StatusFailed, "c/z.txt": StatusNotWritten})
			document := readManifestFile(t, p)
			if len(document.Files) != 3 || document.Files[1].Path != "b/y.txt" || document.Files[1].SHA256 != hashOf([]byte("y1")) || exists(p.generated("b", "y.txt")) {
				t.Fatalf("manifest %+v", document)
			}
			if err := os.Chmod(p.generated("b"), 0o755); err != nil {
				t.Fatal(err)
			}
			want := between(t, p)
			report, _ = Run(context.Background(), p.dir, model("orders"), emitting(threeChanged))
			wantStatuses(t, report, want)
			if want["b/y.txt"] == StatusModified && read(t, p.generated("b", "y.txt")) != "the user's" {
				t.Fatal("a carried-over entry let a run overwrite a file whose bytes differ from it")
			}
		})
	}
}

// A first run killed before any manifest exists leaves files the next run
// treats like any file not in a manifest: adopted when they hold exactly what
// it would write, otherwise unowned, and the run refuses until they are gone.
func TestAFirstRunKilledBeforeItsManifestFailsClosedForADifferentModel(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake)
	if err := os.Remove(p.generated(ManifestName)); err != nil {
		t.Fatal(err)
	}
	before := p.whole(t)

	report, err := Run(context.Background(), p.dir, model("payments"), fake)

	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	wantStatuses(t, report, map[string]Status{"pipeline.txt": StatusUnowned})
	sameSnapshot(t, "the whole tree", before, p.whole(t))

	wantStatuses(t, runOK(t, p, model("orders"), fake), map[string]Status{"pipeline.txt": StatusUnchanged})
	if got := strings.Join(manifestPaths(t, p), " "); got != "pipeline.txt" {
		t.Fatalf("manifest %s", got)
	}
}

// Removing a leftover temporary file is a write, so a run that writes nothing
// leaves it; the next run that writes removes it.
func TestARunThatWritesNothingLeavesTemporaryFilesForTheNextRunThatWrites(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	runOK(t, p, model("orders"), fake, emitting(map[string]string{"pkg/x.go": "x"}))
	leftover := p.generated(".pipeline.txt.tmp-0123456789abcdef")
	write(t, leftover, "half a file")
	write(t, p.generated("pkg", "x.go"), "edited")
	before := p.whole(t)

	report, err := Run(context.Background(), p.dir, model("payments"), fake, emitting(map[string]string{"pkg/x.go": "x"}))

	if problemOf(t, report, err).Kind != ProblemConflicts {
		t.Fatalf("problem %+v", report.Problem)
	}
	sameSnapshot(t, "the whole tree", before, p.whole(t))
	if _, ok := statuses(report)[".pipeline.txt.tmp-0123456789abcdef"]; ok {
		t.Fatal("the temporary file was reported")
	}

	write(t, p.generated("pkg", "x.go"), "x")
	runOK(t, p, model("payments"), fake, emitting(map[string]string{"pkg/x.go": "x"}))
	if exists(leftover) {
		t.Fatal("the next run that wrote did not remove the temporary file")
	}
}
