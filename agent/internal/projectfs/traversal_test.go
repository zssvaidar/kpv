package projectfs

import (
	"os"
	"path/filepath"
	"testing"
)

// A path whose ".." segments take it out of the root is refused, and refused
// before anything is read.
//
// Every path here is written out as text rather than built with filepath.Join,
// because Join cleans: filepath.Join(root, "..", "outside") returns the
// already-reduced sibling path and the ".." the test is named after is gone
// before the guard ever sees it. containsDotDot asserts that has not happened.
func TestDotDotSegmentsThatLeaveTheRootAreRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, attempt := range []struct {
		name   string
		suffix string
	}{
		{"one step out", "/../outside/secret.txt"},
		{"out of a subdirectory", "/inside/../../outside/secret.txt"},
		{"out from deep inside", "/inside/deep/../../../outside/secret.txt"},
		{"out to the parent itself", "/.."},
		{"out to the parent with a trailing separator", "/../"},
		{"out to the grandparent", "/../.."},
		{"dot before the climb", "/./../outside/secret.txt"},
		{"dots mixed into the climb", "/inside/.././../outside/secret.txt"},
		{"doubled separators around the climb", "/..//outside//secret.txt"},
		{"all the way to the filesystem root", "/../../../../../../../../../.."},
		{"past the filesystem root and back down", "/../../../../../../../../../../../../etc/passwd"},
		{"out and into the case sibling", "/../Project/loot.txt"},
		{"out and into the prefix sibling", "/../projectevil/loot.txt"},
		{"up out of the root and back down to a sibling of it", "/inside/deep/../../../projectevil/loot.txt"},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			t.Parallel()
			path := f.root + attempt.suffix
			if !containsDotDot(path) {
				t.Fatalf("%s carries no .. segment, so this case is not testing a traversal", path)
			}
			_, err := f.guard.Resolve(path)
			refused(t, err, "resolving "+path)
			refused(t, mustFail(f.guard.ReadFile(path)), "reading "+path)
			refused(t, f.guard.WriteFile(path, []byte("planted"), 0o644), "writing to "+path)
			refused(t, f.guard.Remove(path), "removing "+path)
			if _, err := f.guard.ReadDir(path); err != nil {
				refused(t, err, "listing "+path)
			} else {
				t.Fatalf("listing %s was allowed", path)
			}
		})
	}

	// Nothing above was created, so no write slipped through under a refusal.
	if _, err := os.Stat(f.at("outside", "planted")); err == nil {
		t.Error("a refused write created a file outside the root")
	}
	if data, err := os.ReadFile(f.at("outside", "secret.txt")); err != nil || string(data) != "secret" {
		t.Errorf("the file outside the root reads %q (%v), want %q untouched", data, err, "secret")
	}
}

// containsDotDot reports whether a path still carries a ".." segment, so a
// traversal test cannot pass because the traversal was cleaned away.
func containsDotDot(path string) bool {
	for _, part := range splitSegments(path) {
		if part == ".." {
			return true
		}
	}
	return false
}

// containsDotDot has to be able to say no, or the assertion above is a
// decoration.
func TestContainsDotDotIsNotAlwaysTrue(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/a/b/c", "/a/..b/c", "/a/b../c", "/a/./b", "/", "/...."} {
		if containsDotDot(path) {
			t.Errorf("containsDotDot(%q) says yes and there is no .. segment in it", path)
		}
	}
	for _, path := range []string{"/a/../b", "/..", "/a/..", "/../a"} {
		if !containsDotDot(path) {
			t.Errorf("containsDotDot(%q) says no and there is a .. segment in it", path)
		}
	}
	// And the premise the whole table rests on: Join really does clean.
	if joined := filepath.Join("/root", "..", "outside"); containsDotDot(joined) {
		t.Fatalf("filepath.Join kept the .. in %q, so these tests could have used it after all", joined)
	}
}

// ".." that stays inside the root is not a traversal, and refusing it would be
// refusing an ordinary path. The file really is read, so this is not just a
// resolution that happens not to error.
func TestDotDotInsideTheRootIsAnOrdinaryPath(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, path := range []string{
		f.root + "/inside/deep/../ok.txt",
		f.root + "/inside/../inside/ok.txt",
		f.root + "/inside/deep/../../inside/ok.txt",
		f.root + "/./inside/deep/../ok.txt",
	} {
		data, err := f.guard.ReadFile(path)
		if err != nil {
			t.Fatalf("%s was refused (%v) and it names a file inside the root", path, err)
		}
		if string(data) != "ok" {
			t.Errorf("%s read %q, want %q", path, data, "ok")
		}
	}
}

// A path that climbs back up through a component that does not exist is not a
// path to anything: the kernel answers ENOENT for it. It is refused as an
// invalid path rather than quietly reduced to a different file, which is what
// a lexical clean would turn it into.
func TestClimbingThroughAMissingComponentIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// The file the lexical reduction would land on exists, so if the guard
	// took that shortcut this test would read it instead of failing.
	f.write(filepath.Join("project", "target.txt"), "target")
	path := f.root + "/nowhere/../target.txt"

	data, err := f.guard.ReadFile(path)
	if err == nil {
		t.Fatalf("%s read %q; it names nothing, because nowhere does not exist", path, data)
	}
	if KindOf(err) != KindInvalid {
		t.Errorf("%s gave %v, want %q", path, err, KindInvalid)
	}
	// The kernel agrees, which is the point of refusing it.
	if _, direct := os.ReadFile(path); direct == nil {
		t.Fatalf("the operating system read %s, so this test's premise is wrong", path)
	}

	// And once the component exists, the same spelling is an ordinary path.
	f.mkdirAll("project", "nowhere")
	again, err := f.guard.ReadFile(path)
	if err != nil {
		t.Fatalf("%s was still refused after nowhere was created: %v", path, err)
	}
	if string(again) != "target" {
		t.Errorf("read %q, want %q", again, "target")
	}
}

// A file that does not exist inside the root is reported missing, not
// forbidden. The two have to be distinguishable, and this is the pair that
// shows the distinction is drawn on the right line.
func TestAMissingFileInsideTheRootIsNotARefusal(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	inside := f.in("inside", "nothing-here.txt")
	resolved, err := f.guard.Resolve(inside)
	if err != nil {
		t.Fatalf("a file that does not exist yet inside the root must still resolve: %v", err)
	}
	if resolved.Real() != inside {
		t.Errorf("resolved to %s, want %s", resolved.Real(), inside)
	}
	_, err = resolved.ReadFile()
	if !IsNotFound(err) {
		t.Errorf("reading a missing file inside the root gave %v, want a not-found error", err)
	}
	if IsForbidden(err) {
		t.Error("a missing file inside the root was reported as forbidden")
	}

	// Writing it is allowed, which is what resolving a path that is not there
	// yet is for.
	if err := resolved.WriteFile([]byte("new"), 0o644); err != nil {
		t.Fatalf("write a new file inside the root: %v", err)
	}
	if data, err := os.ReadFile(inside); err != nil || string(data) != "new" {
		t.Errorf("the new file reads %q (%v), want %q", data, err, "new")
	}
}
