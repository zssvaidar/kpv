package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// realPath is this package's own link resolver, and the standard library has
// one: filepath.EvalSymlinks. Where both can answer, they have to agree.
//
// This is the test that would have caught the resolver being subtly wrong
// about what a relative link target is relative to, which is the kind of
// mistake that reads as correct and opens the wrong file. The tree below is
// built to make the two disagree if the resolver takes any shortcut: links to
// directories, links to files, links to links, relative and absolute targets,
// targets with ".." in them, and ".." in the paths being resolved.
func TestTheResolverAgreesWithEvalSymlinksWhereverBothCanAnswer(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	build := func(parts ...string) string { return filepath.Join(append([]string{base}, parts...)...) }
	mkdir := func(parts ...string) {
		t.Helper()
		if err := os.MkdirAll(build(parts...), 0o755); err != nil {
			t.Fatalf("mkdir %v: %v", parts, err)
		}
	}
	write := func(parts ...string) {
		t.Helper()
		if err := os.WriteFile(build(parts...), []byte("x"), 0o644); err != nil {
			t.Fatalf("write %v: %v", parts, err)
		}
	}
	link := func(target string, parts ...string) {
		t.Helper()
		if err := os.Symlink(target, build(parts...)); err != nil {
			t.Fatalf("link %v -> %s: %v", parts, target, err)
		}
	}

	mkdir("a", "b", "c")
	mkdir("other", "deep")
	write("a", "file")
	write("a", "b", "file")
	write("other", "deep", "file")
	link("b", "a", "to-b")                   // relative, to a directory
	link("b/file", "a", "to-b-file")         // relative, to a file one down
	link("../other", "a", "to-other")        // relative, with .. in the target
	link("../../other/deep", "a", "b", "up") // relative, two .. in the target
	link(build("other"), "a", "absolute")    // absolute target
	link("to-b", "a", "chained")             // a link to a link
	link("to-other/deep/file", "a", "long")  // a link whose target has links above it
	link("a", "top")                         // a link at the top of the tree
	link("nowhere", "a", "dangling")         // a link to nothing

	names := []string{
		"", "a", "a/b", "a/b/c", "a/file", "a/b/file", "other", "other/deep", "other/deep/file",
		"a/to-b", "a/to-b/file", "a/to-b-file", "a/to-other", "a/to-other/deep",
		"a/to-other/deep/file", "a/b/up", "a/b/up/file", "a/absolute", "a/absolute/deep",
		"a/chained", "a/chained/file", "a/long", "top", "top/b", "top/b/file", "top/to-b/file",
		"a/dangling", "a/dangling/more", "a/missing", "a/missing/deeper",
		"a/b/..", "a/b/../file", "a/to-b/../file", "a/to-b/..", "a/to-other/../a/file",
		"a/b/c/../../file", "a/to-b-file/../file", "a/./b/./file", "a//b//file",
		"..", "../..", "a/../..", "a/to-other/..",
	}

	compared := 0
	missing := 0
	for _, name := range names {
		path := base + "/" + name
		wantReal, wantErr := filepath.EvalSymlinks(path)
		gotReal, gotMissing, gotErr := realPath(path)

		switch {
		case wantErr == nil:
			if gotErr != nil {
				t.Errorf("%s: EvalSymlinks says %s, the resolver failed: %v", name, wantReal, gotErr)
				continue
			}
			if len(gotMissing) != 0 {
				t.Errorf("%s: EvalSymlinks says %s, the resolver says %s is missing %v", name, wantReal, gotReal, gotMissing)
				continue
			}
			if gotReal != wantReal {
				t.Errorf("%s: the resolver says %s, EvalSymlinks says %s", name, gotReal, wantReal)
				continue
			}
			compared++
		case errors.Is(wantErr, fs.ErrNotExist):
			// EvalSymlinks will not answer for a path that is not all there.
			// The resolver answers with the part that is, plus what is not,
			// which is what a file about to be created needs.
			if gotErr != nil {
				continue
			}
			if len(gotMissing) == 0 {
				t.Errorf("%s: EvalSymlinks says it does not exist, the resolver says %s exists", name, gotReal)
				continue
			}
			missing++
		default:
			// Any other failure (a cycle, a permission) is allowed to differ
			// in wording; both have to fail.
			if gotErr == nil && len(gotMissing) == 0 {
				t.Errorf("%s: EvalSymlinks failed with %v and the resolver resolved it to %s", name, wantErr, gotReal)
			}
		}
	}
	if compared < 25 {
		t.Fatalf("only %d paths were actually compared against EvalSymlinks, so this test is not checking much", compared)
	}
	if missing < 3 {
		t.Fatalf("only %d paths exercised the missing-tail answer, which is the half EvalSymlinks cannot check", missing)
	}
}

// The resolver has to be able to disagree with EvalSymlinks, or the test above
// proves nothing: a resolver that just called EvalSymlinks would pass it and
// would be unable to answer for a path that is not there yet.
func TestTheResolverAnswersForAPathThatDoesNotExistYet(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "here"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	path := base + "/here/not/there/yet.txt"
	if _, err := filepath.EvalSymlinks(path); err == nil {
		t.Fatal("EvalSymlinks answered for a path that does not exist, so the premise is wrong")
	}
	reached, missing, err := realPath(path)
	if err != nil {
		t.Fatalf("the resolver failed on a path whose tail does not exist: %v", err)
	}
	if want := filepath.Join(base, "here"); reached != want {
		t.Errorf("the resolver reached %s, want %s", reached, want)
	}
	if got, want := filepath.Join(missing...), filepath.Join("not", "there", "yet.txt"); got != want {
		t.Errorf("the resolver says %q is missing, want %q", got, want)
	}
}

// Descending through a file is not a path to anything, and the resolver says
// so rather than stepping over it.
func TestDescendingThroughAFileIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	path := f.in("inside", "ok.txt", "deeper")
	if _, err := os.Open(path); err == nil {
		t.Fatal("the operating system opened a path through a file, so the premise is wrong")
	}
	_, err := f.guard.Resolve(path)
	if KindOf(err) != KindNotDirectory {
		t.Errorf("%s gave %v, want %q", path, err, KindNotDirectory)
	}
}

func TestSegmentsSplitsAbsolutePaths(t *testing.T) {
	t.Parallel()
	for _, pair := range []struct {
		path string
		want []string
	}{
		{"/", nil},
		{"//", nil},
		{"/a", []string{"a"}},
		{"/a/b", []string{"a", "b"}},
		{"/a//b/", []string{"a", "b"}},
		{"/a/./b", []string{"a", "b"}},
		{"/home/user/project", []string{"home", "user", "project"}},
	} {
		got := segments(pair.path)
		if len(got) != len(pair.want) {
			t.Errorf("segments(%q) = %v, want %v", pair.path, got, pair.want)
			continue
		}
		for index := range got {
			if got[index] != pair.want[index] {
				t.Errorf("segments(%q) = %v, want %v", pair.path, got, pair.want)
				break
			}
		}
	}
}

func TestReduceTailAppliesDotsToWhatIsNotThere(t *testing.T) {
	t.Parallel()
	for _, pair := range []struct {
		base        string
		missing     []string
		wantBase    string
		wantTail    string
		wantClimbed bool
	}{
		{"/root", []string{"a", "b"}, "/root", "a/b", false},
		{"/root", []string{"a", ".", "b"}, "/root", "a/b", false},
		{"/root", []string{"a", "..", "b"}, "/root", "b", true},
		{"/root", []string{"..", "b"}, "/", "b", true},
		{"/root/deep", []string{"..", "..", "b"}, "/", "b", true},
		{"/root", []string{"a", "b", "..", ".."}, "/root", "", true},
	} {
		base, tail, climbed := reduceTail(pair.base, pair.missing)
		if base != pair.wantBase || filepath.Join(tail...) != pair.wantTail || climbed != pair.wantClimbed {
			t.Errorf("reduceTail(%q, %v) = %q, %q, %v; want %q, %q, %v",
				pair.base, pair.missing, base, filepath.Join(tail...), climbed,
				pair.wantBase, pair.wantTail, pair.wantClimbed)
		}
	}
}
