package projectfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// swapLink replaces the symbolic link at path with one pointing at target, in
// one step. A remove followed by a create would leave a window in which the
// link is simply absent; a rename over it does not, so what this exercises is
// the swap and not the gap.
func swapLink(t *testing.T, path, target string) {
	t.Helper()
	staging := path + ".staging"
	if err := os.Symlink(target, staging); err != nil {
		t.Fatalf("stage a link to %s: %v", target, err)
	}
	if err := os.Rename(staging, path); err != nil {
		t.Fatalf("swap %s: %v", path, err)
	}
}

// The criterion: a symbolic link swapped in between the path being validated
// and the file being opened must not slip through.
//
// There are two ways that can be true, and both of them are here, because
// which one applies depends on where the link is.
//
// This test is the first: the path was validated through a link, so what the
// handle holds is the real file the link pointed at, named relative to the
// root. Swapping the link afterwards does not move the handle, because the
// handle was never a link to follow. The read keeps returning the file that
// was authorized and never the file that was planted.
//
// It also shows the race is real rather than theoretical: the same swap does
// fool a design that validates a path and then opens that path with os.
func TestALinkSwappedInAfterTheCheckDoesNotMoveTheHandle(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.symlink("inside", filepath.Join("project", "dir"))
	linkPath := f.in("dir")
	through := f.in("dir", "ok.txt")

	// Checked while the link is honest.
	handle, err := f.guard.Resolve(through)
	if err != nil {
		t.Fatalf("%s is inside the root while the link points inside: %v", through, err)
	}
	if want := f.in("inside", "ok.txt"); handle.Real() != want {
		t.Fatalf("the handle holds %s, want the real %s", handle.Real(), want)
	}

	// The swap. From here on the same name reaches outside the root.
	swapLink(t, linkPath, f.outside)
	f.write(filepath.Join("outside", "ok.txt"), "secret")

	// A design that validated the path and then opened it with os is fooled
	// by exactly this swap, and reads the planted file.
	fooled, err := os.ReadFile(through)
	if err != nil || string(fooled) != "secret" {
		t.Fatalf("opening the validated path with os gave %q (%v); this test's premise is that it reads the planted file", fooled, err)
	}

	// The handle is not, because it does not open by path.
	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("the handle stopped working after the link was swapped: %v", err)
	}
	if string(data) == "secret" {
		t.Fatal("the handle returned the content of the file outside the root")
	}
	if string(data) != "ok" {
		t.Fatalf("the handle read %q, want the authorized file's own %q", data, "ok")
	}
	// Writing through it writes to the authorized file, not to the planted one.
	if err := handle.WriteFile([]byte("written"), 0o644); err != nil {
		t.Fatalf("write through the handle: %v", err)
	}
	if planted, err := os.ReadFile(f.at("outside", "ok.txt")); err != nil || string(planted) != "secret" {
		t.Errorf("the planted file now reads %q (%v), want %q untouched", planted, err, "secret")
	}
	if authorized, err := os.ReadFile(f.in("inside", "ok.txt")); err != nil || string(authorized) != "written" {
		t.Errorf("the authorized file reads %q (%v), want the %q the handle wrote", authorized, err, "written")
	}
}

// The second way, and the one where the re-check does the work: the path had
// no link on it when it was validated, and a component of it is replaced by an
// escaping link afterwards. The handle's own name now goes through that link,
// so every operation on it is refused -- by the kernel, walking the name from
// the root's descriptor, not by this package reading the path again.
func TestAnEscapingLinkPutInThePathAfterTheCheckIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(filepath.Join("outside", "ok.txt"), "secret")
	target := f.in("inside", "ok.txt")

	// Validated while the whole path is real directories and a real file.
	handle, err := f.guard.Resolve(target)
	if err != nil {
		t.Fatalf("resolve %s: %v", target, err)
	}
	if handle.Rel() != filepath.Join("inside", "ok.txt") {
		t.Fatalf("the handle is %q inside its root, want %q", handle.Rel(), filepath.Join("inside", "ok.txt"))
	}
	if data, err := handle.ReadFile(); err != nil || string(data) != "ok" {
		t.Fatalf("the handle read %q (%v) before the swap, want %q", data, err, "ok")
	}

	// The last component becomes a link pointing outside the root.
	swapLink(t, target, f.at("outside", "ok.txt"))
	if fooled, err := os.ReadFile(target); err != nil || string(fooled) != "secret" {
		t.Fatalf("os read %q (%v) through the planted link; the premise is that it reads the planted file", fooled, err)
	}

	data, err := handle.ReadFile()
	if err == nil {
		t.Fatalf("the handle read %q through the link planted under it", data)
	}
	if string(data) == "secret" {
		t.Fatal("the handle returned the content of the file outside the root")
	}
	refused(t, err, "reading through a link planted after the check")

	// Every other operation on the same handle is re-checked too, not just
	// the read.
	if _, err := handle.Open(); err == nil {
		t.Error("the handle opened the file through the planted link")
	} else {
		refused(t, err, "opening through the planted link")
	}
	if _, err := handle.Stat(); err == nil {
		t.Error("the handle stat'ed the file through the planted link")
	} else {
		refused(t, err, "stat through the planted link")
	}
	if err := handle.WriteFile([]byte("planted"), 0o644); err == nil {
		t.Error("the handle wrote through the planted link")
	} else {
		refused(t, err, "writing through the planted link")
	}

	// Nothing outside the root changed, and nothing new was created there.
	if data, err := os.ReadFile(f.at("outside", "ok.txt")); err != nil || string(data) != "secret" {
		t.Errorf("the planted file outside now reads %q (%v), want %q untouched", data, err, "secret")
	}
	entries, err := os.ReadDir(f.outside)
	if err != nil {
		t.Fatalf("list the directory outside: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "ok.txt" && entry.Name() != "secret.txt" {
			t.Errorf("something was created outside the root: %s", entry.Name())
		}
	}

	// The atomic write is the one operation that does not refuse here, and it
	// is worth being precise about why rather than asserting a refusal that
	// would be the wrong answer. It writes a temporary file beside the
	// destination and renames it over the name, and a rename replaces the
	// name itself: it does not follow a link sitting there. So the planted
	// link is destroyed and the content lands inside the root, under the name
	// that was authorized. Nothing is written through the link, which is the
	// property that matters.
	if err := handle.WriteAtomically(0o644, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("replaced"))
		return writeErr
	}); err != nil {
		t.Fatalf("an atomic write over a planted link: %v", err)
	}
	if planted, err := os.ReadFile(f.at("outside", "ok.txt")); err != nil || string(planted) != "secret" {
		t.Errorf("the atomic write reached the file outside the root: %q (%v)", planted, err)
	}
	info, err := os.Lstat(target)
	if err != nil {
		t.Fatalf("lstat the destination: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Error("the atomic write left the planted link in place")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "replaced" {
		t.Errorf("the destination inside the root reads %q (%v), want %q", data, err, "replaced")
	}
}

// The same swap one level up: the component that is replaced is a directory in
// the middle of the path rather than the last one.
func TestADirectoryComponentSwappedForALinkDoesNotSlipThrough(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.mkdirAll("project", "work", "nested")
	f.write(filepath.Join("project", "work", "nested", "file.txt"), "ok")
	f.mkdirAll("outside", "nested")
	f.write(filepath.Join("outside", "nested", "file.txt"), "secret")

	through := f.in("work", "nested", "file.txt")
	handle, err := f.guard.Resolve(through)
	if err != nil {
		t.Fatalf("resolve %s: %v", through, err)
	}

	// Replace the real directory with a link pointing outside, in one step.
	staging := f.at("project", "work.staging")
	if err := os.Symlink(f.outside, staging); err != nil {
		t.Fatalf("stage the link: %v", err)
	}
	if err := os.RemoveAll(f.in("work")); err != nil {
		t.Fatalf("remove the directory: %v", err)
	}
	if err := os.Rename(staging, f.in("work")); err != nil {
		t.Fatalf("put the link in its place: %v", err)
	}

	if fooled, err := os.ReadFile(through); err != nil || string(fooled) != "secret" {
		t.Fatalf("os read %q (%v) through the swapped directory; the premise is that it reads the planted file", fooled, err)
	}
	data, err := handle.ReadFile()
	if err == nil || string(data) == "secret" {
		t.Fatalf("the handle read %q (%v) through the swapped directory", data, err)
	}
	refused(t, err, "reading through a directory swapped for a link")
}

// Replacing the authorized root itself does not redirect anything: the root is
// held open as a descriptor, so operations keep reaching the directory that
// was authorized rather than whatever now has its name.
func TestReplacingTheAuthorizedRootDoesNotRedirectAnything(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	handle, err := f.guard.Resolve(f.in("inside", "ok.txt"))
	if err != nil {
		t.Fatalf("resolve the file: %v", err)
	}
	f.mkdirAll("outside", "inside")
	f.write(filepath.Join("outside", "inside", "ok.txt"), "secret")

	// Move the real root aside and give its name to a link pointing outside.
	if err := os.Rename(f.root, f.at("moved")); err != nil {
		t.Fatalf("move the root aside: %v", err)
	}
	if err := os.Symlink(f.outside, f.root); err != nil {
		t.Fatalf("give the root's name to a link: %v", err)
	}

	data, err := handle.ReadFile()
	if err != nil {
		t.Fatalf("the handle stopped working when the root's name was taken: %v", err)
	}
	if string(data) != "ok" {
		t.Fatalf("the handle read %q, want the authorized directory's own %q", data, "ok")
	}
}

// The swap, raced. Many readers go through the guard while the link under them
// is swapped between a target inside the root and a target outside it, as fast
// as both sides can go, with -race watching.
//
// The invariant is the one that matters and the one a narrowed race would
// break: no read ever returns the content of the file outside the root. A read
// may succeed with the file inside, and it may fail; it may not succeed with
// the file outside.
//
// There are no sleeps and no fixed timing here: the readers run until the
// swapper says it is done, which is what makes this not a flaky test.
func TestRacingTheSwapNeverReadsOutsideTheRoot(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.write(filepath.Join("outside", "ok.txt"), "secret")
	f.symlink("inside", filepath.Join("project", "dir"))
	linkPath := f.in("dir")
	through := f.in("dir", "ok.txt")

	const (
		swaps   = 300
		readers = 4
	)
	var (
		inside   atomic.Int64
		outside  atomic.Int64
		refusals atomic.Int64
		other    atomic.Int64
	)
	done := make(chan struct{})
	var waiting sync.WaitGroup

	for range readers {
		waiting.Add(1)
		go func() {
			defer waiting.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				data, err := f.guard.ReadFile(through)
				switch {
				case err == nil && string(data) == "ok":
					inside.Add(1)
				case err == nil:
					outside.Add(1)
					t.Errorf("a read through the raced link returned %q", data)
				case IsForbidden(err):
					refusals.Add(1)
				default:
					other.Add(1)
				}
			}
		}()
	}

	targets := []string{f.outside, f.in("inside")}
	for swap := range swaps {
		staging := fmt.Sprintf("%s.staging-%d", linkPath, swap)
		if err := os.Symlink(targets[swap%2], staging); err != nil {
			t.Errorf("stage a link: %v", err)
			break
		}
		if err := os.Rename(staging, linkPath); err != nil {
			t.Errorf("swap the link: %v", err)
			break
		}
	}
	close(done)
	waiting.Wait()

	if outside.Load() != 0 {
		t.Fatalf("%d reads came back with the file outside the root", outside.Load())
	}
	// The counts below are about the test, not about the guard: if nothing
	// succeeded and nothing was refused, the race never happened and the
	// invariant above held vacuously.
	if inside.Load() == 0 {
		t.Errorf("no read ever succeeded, so this test never exercised the allowed path (refused %d, other %d)",
			refusals.Load(), other.Load())
	}
	if refusals.Load() == 0 {
		t.Errorf("no read was ever refused, so the swap was never observed in flight (succeeded %d, other %d)",
			inside.Load(), other.Load())
	}
	t.Logf("raced %d swaps: %d reads inside, %d refused, %d other", swaps, inside.Load(), refusals.Load(), other.Load())
}
