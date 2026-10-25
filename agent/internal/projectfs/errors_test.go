package projectfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The criterion: rejection is a distinct error type from an ordinary
// missing-file error. This is the test that pins the distinction from both
// ends, because getting it half right is easy -- a refusal that wraps ENOENT,
// or one that answers yes to errors.Is(err, fs.ErrNotExist), is a refusal a
// caller will report as "file not found" and act on as if the path were simply
// free to create.
func TestARefusalIsNotAMissingFile(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.symlink("../outside", filepath.Join("project", "escape"))

	refusals := map[string]error{}
	add := func(name string, err error) {
		if err == nil {
			t.Fatalf("%s was allowed", name)
		}
		refusals[name] = err
	}
	add("a path outside every root", mustFail(f.guard.ReadFile(f.at("outside", "secret.txt"))))
	add("a path outside every root that does not exist", mustFail(f.guard.ReadFile(f.at("outside", "nothing"))))
	add("a traversal out of the root", mustFail(f.guard.ReadFile(f.root+"/../outside/secret.txt")))
	add("a link out of the root", mustFail(f.guard.ReadFile(f.in("escape", "secret.txt"))))
	add("the sibling with the root's name as a prefix", mustFail(f.guard.ReadFile(f.at("projectevil", "loot.txt"))))
	add("a path against a guard with no roots", mustFail((&Guard{}).ReadFile(f.in("inside", "ok.txt"))))

	for name, err := range refusals {
		var fault *PathError
		if !errors.As(err, &fault) {
			t.Errorf("%s: %T is not a *PathError", name, err)
			continue
		}
		if fault.Kind != KindForbidden {
			t.Errorf("%s: kind %q, want %q", name, fault.Kind, KindForbidden)
		}
		if !IsForbidden(err) {
			t.Errorf("%s: IsForbidden says no", name)
		}
		if IsNotFound(err) {
			t.Errorf("%s: IsNotFound says yes", name)
		}
		if errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: errors.Is(err, fs.ErrNotExist) says yes, so a caller cannot tell a refusal from a missing file", name)
		}
		if os.IsNotExist(err) {
			t.Errorf("%s: os.IsNotExist says yes", name)
		}
		if !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s: a refusal should read as a permission failure to a caller that only knows the io/fs sentinels", name)
		}
	}

	// And the other end: a missing file inside the root is a missing file,
	// with the sentinel a caller expects, and is not a refusal.
	_, err := f.guard.ReadFile(f.in("inside", "absent.txt"))
	if err == nil {
		t.Fatal("reading a file that is not there succeeded")
	}
	if !IsNotFound(err) {
		t.Errorf("a missing file inside the root: IsNotFound says no (%v)", err)
	}
	if IsForbidden(err) {
		t.Errorf("a missing file inside the root was reported as a refusal: %v", err)
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file inside the root is not fs.ErrNotExist: %v", err)
	}
	if KindOf(err) != KindNotFound {
		t.Errorf("a missing file inside the root has kind %q, want %q", KindOf(err), KindNotFound)
	}
}

// The kinds map onto the io/fs sentinels the way a caller that knows nothing
// about this package would expect, and no kind maps onto two of them.
func TestTheKindsMapOntoTheFilesystemSentinels(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		kind                                  ErrorKind
		notExist, permission, invalidArgument bool
	}{
		{KindForbidden, false, true, false},
		{KindNotFound, true, false, false},
		{KindUnreadable, false, true, false},
		{KindNotDirectory, false, false, false},
		{KindInvalid, false, false, true},
		{KindIO, false, false, false},
	} {
		err := error(&PathError{Kind: row.kind, Message: "under test"})
		if got := errors.Is(err, fs.ErrNotExist); got != row.notExist {
			t.Errorf("%q: fs.ErrNotExist = %v, want %v", row.kind, got, row.notExist)
		}
		if got := errors.Is(err, fs.ErrPermission); got != row.permission {
			t.Errorf("%q: fs.ErrPermission = %v, want %v", row.kind, got, row.permission)
		}
		if got := errors.Is(err, fs.ErrInvalid); got != row.invalidArgument {
			t.Errorf("%q: fs.ErrInvalid = %v, want %v", row.kind, got, row.invalidArgument)
		}
		if KindOf(err) != row.kind {
			t.Errorf("KindOf lost the kind %q", row.kind)
		}
	}
	if KindOf(nil) != "" || KindOf(errors.New("something else")) != "" {
		t.Error("KindOf claims a kind for an error that is not this package's")
	}
	if IsForbidden(nil) || IsNotFound(nil) {
		t.Error("a nil error is being reported as a refusal or a missing file")
	}
}

// A refusal wrapping a cause must not let the cause decide what the refusal
// is. This is the hole the mapping above would have if Is deferred to the
// chain: errors.Is walks on past Is when it answers no, so a forbidden error
// carrying an ENOENT cause would still answer yes to fs.ErrNotExist.
func TestACauseCannotTurnARefusalIntoAMissingFile(t *testing.T) {
	t.Parallel()
	err := error(&PathError{
		Kind:    KindForbidden,
		Message: "under test",
		Err:     &fs.PathError{Op: "openat", Path: "x", Err: fs.ErrNotExist},
	})
	if errors.Is(err, fs.ErrNotExist) {
		t.Error("a forbidden error carrying an ENOENT cause reads as a missing file")
	}
	if !IsForbidden(err) {
		t.Error("IsForbidden lost the kind because of the cause")
	}
	// The cause is still reachable for a log, which is why it is kept.
	var cause *fs.PathError
	if !errors.As(err, &cause) {
		t.Error("the cause cannot be reached at all")
	}
}

// Every refusal says something, names the path it is about, and prints without
// panicking. An error nobody can read is an error nobody will act on.
func TestEveryRefusalReads(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, attempt := range []string{
		"",
		"with\x00nul",
		f.at("outside", "secret.txt"),
		f.root + "/../outside/secret.txt",
		f.root + "/nowhere/../target",
		f.in("inside", "ok.txt", "deeper"),
		f.in("inside", "absent.txt"),
	} {
		_, err := f.guard.Resolve(attempt)
		if err == nil {
			continue
		}
		var fault *PathError
		if !errors.As(err, &fault) {
			t.Errorf("%q gave %T, want a *PathError", attempt, err)
			continue
		}
		if fault.Kind == "" {
			t.Errorf("%q gave an error with no kind", attempt)
		}
		if fault.Message == "" {
			t.Errorf("%q gave an error with no message", attempt)
		}
		if fault.Op == "" {
			t.Errorf("%q gave an error with no operation", attempt)
		}
		if text := err.Error(); text == "" {
			t.Errorf("%q printed as nothing", attempt)
		}
	}
	// The zero value prints too, rather than panicking on a nil field.
	if text := (&PathError{Kind: KindIO, Message: "nothing in particular"}).Error(); text == "" {
		t.Error("a PathError with no path printed as nothing")
	}
}

// os.Root's refusal is matched by asking the root for the sentinel rather than
// by reading its message. If that stops working the fallback is a string
// match, which is weaker, so this test says when it has happened.
func TestTheEscapeSentinelIsObtainedFromTheRootItself(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	owner, _ := f.guard.locate(f.root)
	if owner == nil {
		t.Fatal("the fixture's root is not authorized")
	}

	sentinel := owner.escapeSentinel()
	if sentinel == nil {
		t.Fatal("os.Root no longer refuses \"..\" with an error this package can hold on to; isEscape has fallen back to matching a message")
	}
	if sentinel.Error() != escapeMessage {
		t.Errorf("the sentinel reads %q, and the fallback string is %q; one of the two is now stale", sentinel.Error(), escapeMessage)
	}

	// It identifies a real escape, and does not identify a missing file.
	_, escape := owner.directory.ReadFile("../outside/secret.txt")
	if !owner.isEscape(escape) {
		t.Errorf("a real escape (%v) was not recognised", escape)
	}
	_, absent := owner.directory.ReadFile("inside/absent.txt")
	if owner.isEscape(absent) {
		t.Errorf("a missing file (%v) was taken for an escape", absent)
	}
	if owner.isEscape(nil) {
		t.Error("a nil error was taken for an escape")
	}
}

// A path with a NUL byte is refused before the filesystem is touched, because
// a NUL truncates the path at the system call and the path the checks read
// would not be the path the kernel opens.
func TestAPathWithANulByteIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)

	for _, path := range []string{
		"\x00",
		f.in("inside", "ok.txt") + "\x00",
		f.in("inside", "ok.txt") + "\x00/../../outside/secret.txt",
		f.root + "\x00" + "/../outside",
	} {
		_, err := f.guard.Resolve(path)
		if KindOf(err) != KindInvalid {
			t.Errorf("%q gave %v, want %q", path, err, KindInvalid)
		}
	}
	if _, err := f.guard.Resolve(""); KindOf(err) != KindInvalid {
		t.Errorf("the empty path gave %v, want %q", err, KindInvalid)
	}
}

// The mask over the cause must stop exactly three questions and no more, or it
// has traded one wrong answer for a lot of missing ones.
func TestTheMaskOverTheCauseIsNotOverBroad(t *testing.T) {
	t.Parallel()
	inner := errors.New("the real reason")
	wrapped := &fs.PathError{Op: "openat", Path: "x", Err: inner}
	err := error(&PathError{Kind: KindForbidden, Message: "under test", Err: wrapped})

	if !errors.Is(err, inner) {
		t.Error("the cause's own sentinel is no longer reachable with errors.Is")
	}
	var reached *fs.PathError
	if !errors.As(err, &reached) || reached.Op != "openat" {
		t.Error("the cause is no longer reachable with errors.As")
	}
	if text := err.Error(); text == "" || !strings.Contains(text, "the real reason") {
		t.Errorf("the cause is no longer in the message: %q", text)
	}
	if errors.Unwrap(err) == nil {
		t.Error("a refusal with a cause unwraps to nothing")
	}
	if errors.Unwrap(&PathError{Kind: KindIO, Message: "no cause"}) != nil {
		t.Error("a refusal with no cause unwraps to something")
	}

	// And a real one, end to end: the kernel's own refusal is the cause, and
	// the kind is still what decides.
	f := newFixture(t)
	f.symlink("../outside/secret.txt", filepath.Join("project", "escape"))
	root, err := f.guard.Resolve(f.root)
	if err != nil {
		t.Fatalf("resolve the root: %v", err)
	}
	entry, err := root.Join("escape")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	_, err = entry.ReadFile()
	refused(t, err, "reading through an escaping link")
	var fault *PathError
	if !errors.As(err, &fault) || fault.Err == nil {
		t.Fatalf("the kernel's own reason was not kept: %v", err)
	}
}
