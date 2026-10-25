//go:build unix

package projectfs

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A named pipe is described and never opened: opening one for reading waits
// for a writer, so a listing that opened it would hang. Asking for the pipe
// itself is answered as not a directory, also without opening it.
func TestANamedPipeIsListedWithoutHangingTheListing(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	pipe := filepath.Join(f.home, "pipe")
	if err := syscall.Mkfifo(pipe, 0o644); err != nil {
		t.Fatalf("make a named pipe: %v", err)
	}
	f.link("pipe", filepath.Join(f.home, "to-pipe"))

	listing := f.list(f.home)
	if entry := entryNamed(t, listing, "pipe"); entry.Kind != EntryOther || entry.Unreadable {
		t.Errorf("the pipe is %+v, want other", entry)
	}
	if entry := entryNamed(t, listing, "to-pipe"); entry.Kind != EntrySymlink || entry.LinkTarget != LinkToOther {
		t.Errorf("the link to the pipe is %+v, want a symlink to other", entry)
	}
	for _, path := range []string{pipe, filepath.Join(f.home, "to-pipe")} {
		if _, err := f.browser.List(path, MaxListing); KindOf(err) != KindNotDirectory {
			t.Errorf("listing %s gave %v, want notDirectory", path, err)
		}
	}
}

// The open that decides readability is the non-blocking one, so a pipe put
// where a file was a moment ago cannot hang it either.
func TestTheReadabilityProbeDoesNotBlockOnANamedPipe(t *testing.T) {
	t.Parallel()
	f := newBrowseFixture(t)
	if err := syscall.Mkfifo(filepath.Join(f.project, "pipe"), 0o644); err != nil {
		t.Fatalf("make a named pipe: %v", err)
	}
	root, err := f.guard.Resolve(f.project)
	if err != nil {
		t.Fatalf("resolve the project: %v", err)
	}
	pipe, err := root.Join("pipe")
	if err != nil {
		t.Fatalf("join the pipe: %v", err)
	}
	// This is the probe a file gets. Were it a blocking open, the test would
	// never return; it returns, and the pipe opened.
	if !pipe.canRead(false) {
		t.Fatal("the non-blocking probe could not open a named pipe")
	}
}
