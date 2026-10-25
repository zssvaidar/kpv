package projectfs

import (
	"os"
	"path/filepath"
	"testing"
)

// caseInsensitive reports whether the filesystem under directory treats two
// spellings that differ only in case as the same name.
//
// It is measured rather than assumed from runtime.GOOS: a macOS volume can be
// case-sensitive, a Linux directory can be on a case-insensitive mount, and a
// test that guesses would be testing the guess.
func caseInsensitive(t *testing.T, directory string) bool {
	t.Helper()
	probe := filepath.Join(directory, "case-probe")
	if err := os.WriteFile(probe, []byte("probe"), 0o644); err != nil {
		t.Fatalf("write the case probe: %v", err)
	}
	defer func() { _ = os.Remove(probe) }()
	_, err := os.Stat(filepath.Join(directory, "CASE-PROBE"))
	return err == nil
}

// A case-collision attempt is refused.
//
// On a case-insensitive filesystem the attempt is the real one the criterion
// names: a differently cased spelling of an authorized root names the same
// directory, and is not the spelling that was authorized. That half can only
// be exercised where the filesystem behaves that way, so it is measured and
// skipped honestly where it does not, rather than passing for the wrong
// reason.
//
// On a case-sensitive filesystem -- ext4, which is where this runs in CI --
// the attempt is a different one and it is exercisable: a sibling directory
// whose name differs from the root's only in case is a directory an attacker
// can create and fill, and a comparison that folded case would hand it over.
// That half runs everywhere, and it is not a weaker substitute: it drives the
// same code with the same inputs. The only thing a case-insensitive filesystem
// changes is that os.Lstat succeeds for a differently cased spelling, and the
// walk then keeps the spelling the caller wrote (TestTheResolverDoesNotCanonicaliseCase).
// A real sibling directory that differs only in case produces exactly that:
// resolution succeeds, the real path carries the caller's case, and within is
// what decides. The one thing it cannot show is that the two spellings name
// the same inode, which is why the other half is skipped rather than faked.
func TestACaseCollisionIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	insensitive := caseInsensitive(t, f.base)
	t.Logf("the filesystem under %s is case-%s", f.base, map[bool]string{true: "insensitive", false: "sensitive"}[insensitive])

	t.Run("a sibling differing from the root only in case", func(t *testing.T) {
		if insensitive {
			t.Skip("base/Project and base/project are the same directory here, so there is no sibling to attack with")
		}
		// The sibling is real, has a file in it, and is readable without the
		// guard, so the refusal is the guard's.
		loot := f.at("Project", "loot.txt")
		if data, err := os.ReadFile(loot); err != nil || string(data) != "loot" {
			t.Fatalf("the sibling this test is about reads %q (%v); the premise is wrong", data, err)
		}
		refused(t, mustFail(f.guard.ReadFile(loot)), loot)
		refused(t, mustFail(f.guard.ReadFile(f.at("Project"))), f.at("Project"))
		_, err := f.guard.Resolve(f.at("PROJECT", "loot.txt"))
		refused(t, err, "an upper-case spelling of the root")
	})

	t.Run("the authorized root spelled in another case", func(t *testing.T) {
		if !insensitive {
			t.Skip("this filesystem is case-sensitive, so base/PROJECT is not base/project and there is no collision to attempt; the comparison that decides it is exercised directly in TestTheContainmentComparisonDoesNotFoldCase")
		}
		// The collision is real on this filesystem: the differently cased
		// path genuinely reaches the authorized file.
		upper := filepath.Join(f.base, "PROJECT", "inside", "ok.txt")
		if data, err := os.ReadFile(upper); err != nil || string(data) != "ok" {
			t.Fatalf("%s reads %q (%v); the filesystem was reported case-insensitive, so the premise is wrong", upper, data, err)
		}
		// And it is still refused, because it is not the spelling that was
		// authorized.
		refused(t, mustFail(f.guard.ReadFile(upper)), upper)
		_, err := f.guard.Resolve(filepath.Join(f.base, "PROJECT"))
		refused(t, err, "the root in upper case")
	})
}

// The comparison that decides a case collision, exercised directly, so the
// rule is pinned on every filesystem rather than only where a collision can be
// built.
//
// The dangerous direction is the one in the middle: folding case here would
// accept "/home/user/Project" as the authorized "/home/user/project", and on a
// case-sensitive filesystem that is a different directory which anyone able to
// write to the parent can create.
func TestTheContainmentComparisonDoesNotFoldCase(t *testing.T) {
	t.Parallel()
	for _, pair := range []struct {
		root, candidate string
		want            bool
	}{
		{"/home/user/project", "/home/user/project", true},
		{"/home/user/project", "/home/user/Project", false},
		{"/home/user/project", "/home/user/PROJECT", false},
		{"/home/user/project", "/home/user/pRoJeCt", false},
		{"/home/user/project", "/home/user/Project/inside/ok.txt", false},
		{"/home/user/project", "/Home/user/project/inside/ok.txt", false},
		{"/home/user/project", "/home/User/project/inside/ok.txt", false},
		{"/home/user/project", "/home/user/project/Inside/ok.txt", true},
		{"/home/user/Project", "/home/user/project", false},
		{"/home/user/ça", "/home/user/ÇA", false},
		{"/home/user/ça", "/home/user/ça/x", true},
	} {
		if _, inside := within(pair.root, pair.candidate); inside != pair.want {
			t.Errorf("within(%q, %q) = %v, want %v", pair.root, pair.candidate, inside, pair.want)
		}
	}
}

// Nothing in resolution folds or canonicalises case either, which is the other
// half of the argument: the comparison is byte for byte, and the strings it
// compares still carry the case the caller wrote. If resolution canonicalised
// the case of a path on a case-insensitive filesystem, a collision would be
// folded away before the comparison ever saw it.
func TestTheResolverDoesNotCanonicaliseCase(t *testing.T) {
	t.Parallel()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	mixed := filepath.Join(base, "MiXeD", "CaSe")
	if err := os.MkdirAll(mixed, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	reached, missing, err := realPath(mixed)
	if err != nil {
		t.Fatalf("resolve %s: %v", mixed, err)
	}
	if len(missing) != 0 {
		t.Fatalf("%s is there and the resolver says %v is missing", mixed, missing)
	}
	if reached != mixed {
		t.Errorf("the resolver returned %q for %q; the case was changed", reached, mixed)
	}
	// And the segments it compares keep that case.
	got := segments(mixed)
	if len(got) < 2 || got[len(got)-2] != "MiXeD" || got[len(got)-1] != "CaSe" {
		t.Errorf("segments(%q) = %v; the case was changed", mixed, got)
	}
}

// caseInsensitive has to be able to answer both ways, or the skips above are
// hiding a probe that always says one thing.
func TestTheCaseProbeMeasuresSomething(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The probe's own answer, and the fact it rests on a real stat: a name
	// that is not there at all is not found whatever the filesystem does.
	_ = caseInsensitive(t, directory)
	if _, err := os.Stat(filepath.Join(directory, "SENTINEL")); err == nil {
		// Case-insensitive filesystem: then the probe must say so.
		if !caseInsensitive(t, directory) {
			t.Error("the stat found SENTINEL but the probe reports a case-sensitive filesystem")
		}
	} else if caseInsensitive(t, directory) {
		t.Error("the stat did not find SENTINEL but the probe reports a case-insensitive filesystem")
	}
	if _, err := os.Stat(filepath.Join(directory, "case-probe")); err == nil {
		t.Error("the probe left its file behind")
	}
}
