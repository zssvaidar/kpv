package projectfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// The seam #3 left fits, and it fits without this package importing the
// pipeline package: Go interfaces are satisfied structurally, so the
// compile-time check belongs in a test, where importing pipeline costs
// nothing. This is the assertion that would fail if either side's shape
// changed.
func TestThePipelineSeamSatisfiesThePathGuardInterface(t *testing.T) {
	t.Parallel()
	var _ pipeline.PathGuard = PipelineSeam{}
	var _ pipeline.PathGuard = (&Guard{}).PipelineSeam()

	// A seam with no guard behind it authorizes nothing, rather than passing
	// every path through.
	if _, err := (PipelineSeam{}).ResolvePipelinePath("/etc/passwd"); !IsForbidden(err) {
		t.Errorf("a zero seam gave %v, want a refusal", err)
	}
}

// A Store handed the seam reads and writes inside the root and is refused
// outside it, with no change to the shape of Load or Save, which is what #3
// promised.
func TestAStoreBehindTheSeamIsHeldToTheAuthorizedRoot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	store := pipeline.Store{Guard: f.guard.PipelineSeam()}

	// The smallest legal pipeline, taken from the shape the shared fixtures
	// use: a source, a destination and no processing steps.
	source := `id: orders-to-billing
language: python
source:
    id: orders
    type: kafkaSource
    topic: orders.v1
destination:
    id: billing
    type: kafkaDestination
    topic: billing.v1
runtimeConfiguration:
    networkMode: docker
`
	f.write(filepath.Join("project", "pipeline.yaml"), source)
	inside := f.in("pipeline.yaml")

	loaded, steps, err := store.Load(inside)
	if err != nil {
		t.Fatalf("load a pipeline inside the authorized root: %v", err)
	}
	if loaded.ID != "orders-to-billing" {
		t.Errorf("loaded %q, want %q", loaded.ID, "orders-to-billing")
	}
	if len(steps) != 0 {
		t.Errorf("loaded %d steps, want none", len(steps))
	}

	// Saving it back, inside the root.
	if err := store.Save(f.in("saved.yaml"), loaded); err != nil {
		t.Fatalf("save a pipeline inside the authorized root: %v", err)
	}
	if _, err := os.Stat(f.in("saved.yaml")); err != nil {
		t.Errorf("the saved pipeline is not there: %v", err)
	}

	// And outside it, in every shape: an absolute path, a traversal and a
	// link. The refusal arrives as the pipeline package's own FileError with
	// the kind it reserved for a guard refusal, and this package's PathError
	// is still reachable inside it.
	f.write(filepath.Join("outside", "pipeline.yaml"), source)
	f.symlink("../outside/pipeline.yaml", filepath.Join("project", "escape.yaml"))

	for _, attempt := range []struct {
		name string
		path string
	}{
		{"an absolute path outside the root", f.at("outside", "pipeline.yaml")},
		{"a traversal out of the root", f.root + "/../outside/pipeline.yaml"},
		{"a link out of the root", f.in("escape.yaml")},
		{"a path outside the root that does not exist", f.at("outside", "new.yaml")},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			_, _, err := store.Load(attempt.path)
			assertRefusedThroughTheSeam(t, err, "loading "+attempt.name)
			err = store.Save(attempt.path, loaded)
			assertRefusedThroughTheSeam(t, err, "saving "+attempt.name)
		})
	}

	// Nothing was written outside the root by any of those saves.
	entries, err := os.ReadDir(f.outside)
	if err != nil {
		t.Fatalf("list outside: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "pipeline.yaml" && entry.Name() != "secret.txt" {
			t.Errorf("a refused save created %s outside the root", entry.Name())
		}
	}
	if data, err := os.ReadFile(f.at("outside", "pipeline.yaml")); err != nil || string(data) != source {
		t.Error("a refused save changed the pipeline file outside the root")
	}
}

func assertRefusedThroughTheSeam(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was allowed", what)
	}
	var reported *pipeline.FileError
	if !errors.As(err, &reported) {
		t.Fatalf("%s failed with %T (%v), want the pipeline package's own *FileError", what, err, err)
	}
	if reported.Kind != pipeline.KindForbidden {
		t.Errorf("%s was reported as %q, want %q", what, reported.Kind, pipeline.KindForbidden)
	}
	if !IsForbidden(err) {
		t.Errorf("%s: this package's refusal is no longer reachable inside the FileError: %v", what, err)
	}
}

// The store's own guarantee, unchanged: a Store with no guard still accepts
// every path, so this package changed nothing about the shape of #3.
func TestAStoreWithNoGuardIsStillUnguarded(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	if _, _, err := pipeline.LoadFile(f.at("outside", "secret.txt")); err == nil {
		t.Fatal("a file that is not a pipeline loaded")
	} else if IsForbidden(err) {
		t.Errorf("a Store with no guard refused a path as forbidden: %v", err)
	}
}
