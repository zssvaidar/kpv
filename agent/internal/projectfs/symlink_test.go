package projectfs

import (
	"os"
	"path/filepath"
	"testing"
)

// A symbolic link that points out of the root is refused, in every shape a
// link can take. These are real links in a real directory, because a link is
// the one thing a path cannot be reasoned about without the filesystem.
func TestASymlinkPointingOutOfTheRootIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// Links at the top of the root.
	f.symlink(f.outside, filepath.Join("project", "absolute-dir"))
	f.symlink(f.at("outside", "secret.txt"), filepath.Join("project", "absolute-file"))
	f.symlink("../outside", filepath.Join("project", "relative-dir"))
	f.symlink("../outside/secret.txt", filepath.Join("project", "relative-file"))
	f.symlink("/etc", filepath.Join("project", "etc"))
	// A link to the root's own parent, which is out by one step.
	f.symlink("..", filepath.Join("project", "up"))
	// A link that points at another link that points out.
	f.symlink("relative-dir", filepath.Join("project", "indirect"))
	// A link into the sibling whose name starts with the root's.
	f.symlink("../projectevil", filepath.Join("project", "prefix-sibling"))

	for _, attempt := range []struct {
		name string
		path string
	}{
		{"an absolute link to a directory outside", f.in("absolute-dir", "secret.txt")},
		{"the absolute link itself", f.in("absolute-dir")},
		{"an absolute link to a file outside", f.in("absolute-file")},
		{"a relative link to a directory outside", f.in("relative-dir", "secret.txt")},
		{"a relative link to a file outside", f.in("relative-file")},
		{"a link to a system directory", f.in("etc", "passwd")},
		{"a link to the root's parent", f.in("up", "outside", "secret.txt")},
		{"a link through a link", f.in("indirect", "secret.txt")},
		{"a link into the prefix sibling", f.in("prefix-sibling", "loot.txt")},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			t.Parallel()
			_, err := f.guard.Resolve(attempt.path)
			refused(t, err, "resolving "+attempt.path)
			refused(t, mustFail(f.guard.ReadFile(attempt.path)), "reading "+attempt.path)
			refused(t, f.guard.WriteFile(attempt.path, []byte("planted"), 0o644), "writing to "+attempt.path)
		})
	}

	// The file the links point at is still exactly as it was, so nothing was
	// written through a refused path.
	if data, err := os.ReadFile(f.at("outside", "secret.txt")); err != nil || string(data) != "secret" {
		t.Errorf("the file outside reads %q (%v), want %q untouched", data, err, "secret")
	}
}

// The case the criterion calls out by name: a link whose final target is
// outside the root while every directory on the way to the link is inside it.
//
// This is what a design that checks the string, or that checks only the first
// component, or that stops resolving once it has seen a prefix it likes, lets
// through. Resolution has to follow the whole path to the end.
func TestALinkWhoseIntermediatePathIsInsideAndFinalTargetIsOutsideIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	// base/project/a/b/c/ is all inside the root; only the last link leaves.
	f.mkdirAll("project", "a", "b", "c")
	f.symlink("../../../../outside", filepath.Join("project", "a", "b", "c", "out"))
	f.symlink(f.at("outside"), filepath.Join("project", "a", "b", "c", "out-absolute"))

	// Everything up to the link is genuinely inside, which is what makes this
	// the hard case rather than an easy one.
	for _, inside := range []string{f.in("a"), f.in("a", "b"), f.in("a", "b", "c")} {
		if _, err := f.guard.Resolve(inside); err != nil {
			t.Fatalf("%s is inside the root and was refused: %v", inside, err)
		}
	}

	for _, path := range []string{
		f.in("a", "b", "c", "out"),
		f.in("a", "b", "c", "out", "secret.txt"),
		f.in("a", "b", "c", "out-absolute", "secret.txt"),
	} {
		_, err := f.guard.Resolve(path)
		refused(t, err, "resolving "+path)
		refused(t, mustFail(f.guard.ReadFile(path)), "reading "+path)
	}

	// And an intermediate link that leaves and comes back is still refused at
	// the end of it, because the end is what the path names.
	f.symlink(f.at("outside"), filepath.Join("project", "a", "hop"))
	f.symlink(f.in("inside"), filepath.Join("outside", "back"))
	refused(t, mustFail(f.guard.ReadFile(f.in("a", "hop", "secret.txt"))), "a file reached through a link that leaves")
	// Through the link out and then back in through another link: the real
	// path is inside the root, so this one is allowed, and it reads the file
	// inside. Links are judged by where they really lead. The caller wrote no
	// ".." here; the targets were written by whoever could write the root.
	data, err := f.guard.ReadFile(f.in("a", "hop", "back", "ok.txt"))
	if err != nil {
		t.Fatalf("a path whose real target is inside the root was refused: %v", err)
	}
	if string(data) != "ok" {
		t.Errorf("read %q, want %q", data, "ok")
	}
	// But the caller's own ".." after that link is out of the root, and a ".."
	// that climbs out is refused even when the path would come back in.
	for _, path := range []string{
		f.in("a", "hop") + "/../project/inside/ok.txt",
		f.in("a", "hop") + "/../outside/back/ok.txt",
	} {
		refused(t, mustFail(f.guard.ReadFile(path)), "reading "+path)
	}
}

// A link that stays inside the root is followed, because refusing it would
// refuse an ordinary project layout.
func TestALinkInsideTheRootIsFollowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.symlink("inside", filepath.Join("project", "alias"))
	f.symlink("inside/ok.txt", filepath.Join("project", "file-alias"))
	// Relative to the directory the link is in, which is deep/.
	f.symlink("../ok.txt", filepath.Join("project", "inside", "deep", "up-alias"))

	for _, path := range []string{
		f.in("alias", "ok.txt"),
		f.in("file-alias"),
		f.in("inside", "deep", "up-alias"),
	} {
		resolved, err := f.guard.Resolve(path)
		if err != nil {
			t.Fatalf("%s is a link inside the root and was refused: %v", path, err)
		}
		// Reported under the real name, not the name it was reached by.
		if want := f.in("inside", "ok.txt"); resolved.Real() != want {
			t.Errorf("%s resolved to %s, want the real %s", path, resolved.Real(), want)
		}
		data, err := resolved.ReadFile()
		if err != nil || string(data) != "ok" {
			t.Errorf("%s read %q (%v), want %q", path, data, err, "ok")
		}
	}
}

// An absolute link inside the root that points back inside the root: os.Root
// refuses absolute links outright, so the operation fails even though the
// target is inside. Resolution agrees with the kernel about the target, and
// the open is what decides, so this is written down rather than guessed at.
func TestAnAbsoluteLinkIsRefusedEvenWhenItPointsInside(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.symlink(f.in("inside", "ok.txt"), filepath.Join("project", "absolute-inside"))

	path := f.in("absolute-inside")
	resolved, err := f.guard.Resolve(path)
	if err != nil {
		t.Fatalf("resolution follows the link to a target inside the root, so it should not refuse: %v", err)
	}
	if want := f.in("inside", "ok.txt"); resolved.Real() != want {
		t.Errorf("resolved to %s, want %s", resolved.Real(), want)
	}
	// Reading it by the resolved real path works, because that path has no
	// link on it any more.
	if data, err := f.guard.ReadFile(resolved.Real()); err != nil || string(data) != "ok" {
		t.Errorf("the resolved real path read %q (%v), want %q", data, err, "ok")
	}
	// Reading it by the link's own name does not, because the descriptor walk
	// will not follow an absolute link. Refused, never redirected. Resolve
	// rewrites the name to the real one, so the link's own name has to be
	// reached with Join, which does not resolve.
	root, err := f.guard.Resolve(f.root)
	if err != nil {
		t.Fatalf("resolve the root: %v", err)
	}
	linkName, err := root.Join("absolute-inside")
	if err != nil {
		t.Fatalf("join the link's name: %v", err)
	}
	if _, err := linkName.ReadFile(); err == nil {
		t.Error("the descriptor walk followed an absolute link")
	} else {
		refused(t, err, "reading through an absolute link by its own name")
	}
}

// A link cycle ends in a refusal rather than a loop.
func TestALinkCycleIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	f.symlink("loop-b", filepath.Join("project", "loop-a"))
	f.symlink("loop-a", filepath.Join("project", "loop-b"))
	f.symlink("self", filepath.Join("project", "self"))

	for _, path := range []string{f.in("loop-a"), f.in("loop-b"), f.in("self"), f.in("self", "deeper")} {
		_, err := f.guard.Resolve(path)
		if err == nil {
			t.Fatalf("%s is a link cycle and resolved", path)
		}
		if kind := KindOf(err); kind != KindInvalid && kind != KindForbidden {
			t.Errorf("%s gave %v (%q); a cycle is an invalid path", path, err, kind)
		}
	}
}

// Lstat and Readlink report a link as a link, including one that points out of
// the root, which is what a directory listing needs in order to mark it. They
// report it; they do not follow it.
func TestALinkOutOfTheRootCanBeReportedWithoutBeingFollowed(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.symlink("../outside/secret.txt", filepath.Join("project", "escape"))

	root, err := f.guard.Resolve(f.root)
	if err != nil {
		t.Fatalf("resolve the root: %v", err)
	}
	entry, err := root.Join("escape")
	if err != nil {
		t.Fatalf("join the link's name: %v", err)
	}

	info, err := entry.Lstat()
	if err != nil {
		t.Fatalf("lstat the link: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("lstat followed the link instead of reporting it")
	}
	target, err := entry.Readlink()
	if err != nil {
		t.Fatalf("read the link: %v", err)
	}
	if target != "../outside/secret.txt" {
		t.Errorf("the link reads %q, want %q", target, "../outside/secret.txt")
	}
	// Following it is still refused, which is the point of being able to see
	// it without following it.
	if _, err := entry.ReadFile(); err == nil {
		t.Error("reading through the escaping link succeeded")
	} else {
		refused(t, err, "reading through the escaping link")
	}
	if _, err := entry.Stat(); err == nil {
		t.Error("stat followed the escaping link")
	} else {
		refused(t, err, "stat through the escaping link")
	}
}

// Join takes an entry name, not a path, so a directory listing cannot be
// turned into a traversal by an entry name that is not really a name.
func TestJoinRefusesAnythingThatIsNotAnEntryName(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	root, err := f.guard.Resolve(f.root)
	if err != nil {
		t.Fatalf("resolve the root: %v", err)
	}

	for _, name := range []string{"", ".", "..", "../outside", "inside/ok.txt", "/etc", "a/b", "with\x00nul"} {
		if _, err := root.Join(name); err == nil {
			t.Errorf("Join accepted %q, which is not an entry name", name)
		} else if KindOf(err) != KindInvalid {
			t.Errorf("Join(%q) gave %v, want %q", name, err, KindInvalid)
		}
	}
	joined, err := root.Join("inside", "ok.txt")
	if err != nil {
		t.Fatalf("Join on two real entry names: %v", err)
	}
	if want := f.in("inside", "ok.txt"); joined.Real() != want {
		t.Errorf("Join gave %s, want %s", joined.Real(), want)
	}
	if data, err := joined.ReadFile(); err != nil || string(data) != "ok" {
		t.Errorf("the joined path read %q (%v), want %q", data, err, "ok")
	}
}
