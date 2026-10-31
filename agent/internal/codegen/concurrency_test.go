package codegen

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/pipeline"
)

// blocking is a generator that says when it starts and does not return until
// released.
func blocking(started chan<- string, release <-chan struct{}, name string, files map[string]string) Generator {
	return GeneratorFunc(name, func(m *pipeline.Pipeline) ([]File, error) {
		started <- name
		<-release
		return emitting(files).Generate(m)
	})
}

func waitForLockUsers(t *testing.T, key string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for lockUsers(key) != want {
		if time.Now().After(deadline) {
			t.Fatalf("lock users %d, want %d", lockUsers(key), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// The second run waits for the first: it has asked for the directory's lock,
// and it has not started generating, until the first has finished.
func TestASecondRunForTheSameDirectoryWaitsForTheFirst(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	// The same directory under another spelling still waits.
	again, err := p.guard.Resolve(p.pipe + "/../pipe/")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 2)
	releaseFirst, releaseSecond := make(chan struct{}), make(chan struct{})
	close(releaseSecond)

	results := make(chan Report, 2)
	go func() {
		report, _ := Run(context.Background(), p.dir, model("orders"), blocking(started, releaseFirst, "first", map[string]string{"x.txt": "first"}))
		results <- report
	}()
	if got := <-started; got != "first" {
		t.Fatalf("%s started first", got)
	}
	go func() {
		report, _ := Run(context.Background(), again, model("orders"), blocking(started, releaseSecond, "second", map[string]string{"x.txt": "second"}))
		results <- report
	}()
	waitForLockUsers(t, p.dir.Real(), 2)
	select {
	case name := <-started:
		t.Fatalf("%s started generating while the first run held the directory", name)
	default:
	}
	if exists(p.generated()) {
		t.Fatal("something was written while the first run was still generating")
	}

	close(releaseFirst)
	first := <-results
	if got := <-started; got != "second" {
		t.Fatalf("%s", got)
	}
	second := <-results
	wantStatuses(t, first, map[string]Status{"x.txt": StatusWritten})
	// The second saw the first's finished work: its own file, its manifest.
	wantStatuses(t, second, map[string]Status{"x.txt": StatusWritten})
	if read(t, p.generated("x.txt")) != "second" {
		t.Fatal("the runs interleaved")
	}
	waitForLockUsers(t, p.dir.Real(), 0)
}

func TestARunWaitingForTheDirectoryGivesUpWhenItsContextEnds(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	started := make(chan string, 1)
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := Run(context.Background(), p.dir, model("orders"), blocking(started, release, "first", nil))
		done <- err
	}()
	<-started

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report, err := Run(ctx, p.dir, model("orders"), fake)
	if problemOf(t, report, err).Kind != ProblemInterrupted || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("report %+v err %v", report, err)
	}
	if _, err := Inspect(ctx, p.dir); err == nil {
		t.Fatal("inspect did not wait for the run")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRunsForDifferentDirectoriesDoNotWaitForEachOther(t *testing.T) {
	t.Parallel()
	p, q := newProject(t), newProject(t)
	started := make(chan string, 1)
	release := make(chan struct{})
	defer close(release)
	go func() {
		_, _ = Run(context.Background(), p.dir, model("orders"), blocking(started, release, "first", nil))
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := Run(ctx, q.dir, model("orders"), fake); err != nil {
		t.Fatal(err)
	}
}

func TestManyConcurrentRunsLeaveAConsistentDirectory(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			files := map[string]string{"shared.txt": "same", fmt.Sprintf("run/%d.txt", i%4): fmt.Sprint(i % 4)}
			if _, err := Run(context.Background(), p.dir, model("orders"), emitting(files)); err != nil {
				errs <- err
			}
			if _, err := Inspect(context.Background(), p.dir); err != nil {
				errs <- err
			}
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	inspection, err := Inspect(context.Background(), p.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range inspection.Files {
		if file.Status != InspectUnchanged && file.Status != InspectOrphaned {
			t.Fatalf("after concurrent runs: %+v", inspection.Files)
		}
	}
	assertNoTemporaries(t, p)
}
