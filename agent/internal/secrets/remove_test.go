package secrets

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// blockOf is the .env block holding exactly these lines, LF endings.
func blockOf(body ...string) string {
	if len(body) == 0 {
		return StartMarker + "\n" + endMarker(nil) + "\n"
	}
	return StartMarker + "\n" + strings.Join(body, "\n") + "\n" + endMarker(body) + "\n"
}

func TestRemovingAKeyDropsItsLineAndKeepsTheRestInOneWrite(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	writeOwnerOnly(t, p.path(EnvFile), "USER=1\n")
	if _, err := p.write(t, map[string]string{"A": "1", "B": "2", "C": "3"}); err != nil {
		t.Fatal(err)
	}
	report, err := p.change(t, map[string]string{"D": "4"}, "B")
	if err != nil || report.Env != Updated || len(report.Problems) != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	if want := "USER=1\n" + blockOf(`A="1"`, `C="3"`, `D="4"`); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
}

func TestRemovingAKeyNotInTheBlockChangesNothing(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, p.pipe)
	report, err := p.change(t, nil, "MISSING", "MISSING")
	if err != nil || report.Env != Unchanged || report.Gitignore != Unchanged || len(report.Problems) != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	sameSnapshot(t, before, snapshot(t, p.pipe))
}

// #68 removes a side's variables after its connection is removed; a user's
// own line for the same key is theirs and stays, with nothing said about it.
func TestRemovingAKeyAUserLineSetsLeavesTheLineWithNoRefusalOrWarning(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	writeOwnerOnly(t, p.path(EnvFile), "KAFKA_PASSWORD=mine\n")
	if _, err := p.write(t, map[string]string{"OTHER": "1"}); err != nil {
		t.Fatal(err)
	}
	logged := p.logs.String()
	report, err := p.change(t, nil, "KAFKA_PASSWORD", "OTHER")
	if err != nil || len(report.Problems) != 0 {
		t.Fatalf("%+v %v", report, err)
	}
	if want := "KAFKA_PASSWORD=mine\n" + blockOf(); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
	if p.logs.String() != logged {
		t.Fatalf("something was logged: %s", strings.TrimPrefix(p.logs.String(), logged))
	}
}

func TestRemovingEveryKeyLeavesAnEmptyBlock(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "# mine\n")
	if _, err := p.write(t, map[string]string{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.change(t, nil, "A", "B"); err != nil {
		t.Fatal(err)
	}
	if want := "# mine\n" + blockOf(); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
}

func TestAKeyBothSetAndRemovedOrAnInvalidKeyToRemoveIsRefusedByName(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, p.pipe)
	_, err := p.change(t, map[string]string{"A": canary, "B": "2"}, "A", "1BAD", "B")
	problems := refusedWith(t, err, KindSetAndRemoved, KindSetAndRemoved, KindInvalidKey)
	if problems[0].Key != "A" || problems[1].Key != "B" || problems[2].Key != "1BAD" {
		t.Fatalf("%+v", problems)
	}
	sameSnapshot(t, before, snapshot(t, p.pipe))
}

// Setting and removing are one replacement of .env: interrupted, neither
// happened.
func TestAnInterruptedSetAndRemoveLeavesThePreviousEnv(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.write(t, map[string]string{"A": "1", "B": "2"}); err != nil {
		t.Fatal(err)
	}
	previous := p.env(t)
	p.files.hooks.envEmit = func() error { return errors.New("interrupted") }
	report, err := p.change(t, map[string]string{"C": canary}, "A")
	if err == nil || report.Env != NotWritten {
		t.Fatalf("%+v %v", report, err)
	}
	if p.env(t) != previous {
		t.Fatalf(".env changed: %q", p.env(t))
	}
}

// One write sets a key while another removes a different one: the second
// waits for the first, and the result has both changes.
func TestASetAndARemoveToOneDirectoryDoNotInterleave(t *testing.T) {
	t.Parallel()
	for _, setFirst := range []bool{true, false} {
		p := newProject(t)
		if _, err := p.write(t, map[string]string{"OLD": "1", "KEEP": "2"}); err != nil {
			t.Fatal(err)
		}
		entered := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		p.files.hooks.envEmit = func() error {
			once.Do(func() {
				close(entered)
				<-release
			})
			return nil
		}
		set := func() error { _, err := p.write(t, map[string]string{"NEW": "3"}); return err }
		remove := func() error { _, err := p.change(t, nil, "OLD"); return err }
		first, second := set, remove
		if !setFirst {
			first, second = remove, set
		}
		var wg sync.WaitGroup
		errs := make([]error, 2)
		wg.Add(2)
		go func() { defer wg.Done(); errs[0] = first() }()
		<-entered
		go func() { defer wg.Done(); errs[1] = second() }()
		deadline := time.Now().Add(5 * time.Second)
		for lockUsers(p.pipe) < 2 {
			if time.Now().After(deadline) {
				t.Fatal("the second write never waited")
			}
			time.Sleep(time.Millisecond)
		}
		close(release)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatal(errs)
		}
		if want := blockOf(`KEEP="2"`, `NEW="3"`); p.env(t) != want {
			t.Fatalf("set first %v: .env %q\nwant %q", setFirst, p.env(t), want)
		}
	}
}

// A block appended to a file whose last line has no newline: the file gains
// one line ending after that line, and the end marker has none, so the file
// still does not end with a newline.
func TestABlockAppendedAfterALastLineWithNoNewlineEndsWithoutOne(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "A=1")
	write(t, p.path(GitignoreFile), "dist")
	if _, err := p.write(t, map[string]string{"K": "v"}); err != nil {
		t.Fatal(err)
	}
	body := []string{`K="v"`}
	if want := "A=1\n" + StartMarker + "\n" + body[0] + "\n" + endMarker(body); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
	gitignore := p.gitignore(t)
	if !strings.HasPrefix(gitignore, "dist\n"+StartMarker+"\n") || !strings.HasSuffix(gitignore, "<<<") {
		t.Fatalf(".gitignore %q", gitignore)
	}
	env := p.env(t)
	if _, err := p.write(t, map[string]string{"K": "v"}); err != nil || p.env(t) != env || p.gitignore(t) != gitignore {
		t.Fatalf("a second write changed it: %v", err)
	}
}

// A block edited by hand is refused; deleting the whole block, both markers
// included, is how the user clears that, and the next write appends a new one.
func TestDeletingAnEditedBlockClearsIt(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), "USER=1\n")
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	write(t, p.path(EnvFile), strings.Replace(p.env(t), `A="1"`, `A="2"`, 1))
	_, err := p.write(t, map[string]string{"B": "2"})
	refusedWith(t, err, KindBlockEdited)
	write(t, p.path(EnvFile), "USER=1\n")
	if _, err := p.write(t, map[string]string{"B": "2"}); err != nil {
		t.Fatal(err)
	}
	if want := "USER=1\n" + blockOf(`B="2"`); p.env(t) != want {
		t.Fatalf(".env %q\nwant %q", p.env(t), want)
	}
}

// writeOwnerOnly writes a .env the way a careful user keeps it, 0600, so no
// mode warning is expected.
func writeOwnerOnly(t *testing.T, path, content string) {
	t.Helper()
	write(t, path, content)
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
}
