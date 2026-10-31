package secrets

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/projectfs"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
}

func TestALinkAtEitherNameIsReportedAndNeitherReadWrittenThroughNorReplaced(t *testing.T) {
	t.Parallel()
	for _, file := range []string{EnvFile, GitignoreFile} {
		for _, target := range []string{"inside", "outside", "dangling"} {
			t.Run(file+" "+target, func(t *testing.T) {
				t.Parallel()
				p := newProject(t)
				outside := filepath.Join(t.TempDir(), "target")
				write(t, outside, "SECRET_OUTSIDE=1\n")
				write(t, p.path("inside.txt"), "TRACKED=1\n")
				link := map[string]string{"inside": p.path("inside.txt"), "outside": outside, "dangling": p.path("nothing")}[target]
				if err := os.Symlink(link, p.path(file)); err != nil {
					t.Fatal(err)
				}
				before := snapshot(t, p.pipe)
				outsideBefore := read(t, outside)
				_, err := p.write(t, map[string]string{"K": canary})
				refusal := refusedWith(t, err, KindLink)
				if refusal[0].File != file {
					t.Fatalf("%+v", refusal)
				}
				sameSnapshot(t, before, snapshot(t, p.pipe))
				if read(t, outside) != outsideBefore {
					t.Fatal("written through the link")
				}
				if file == EnvFile {
					_, err := p.files.ReadEnv(p.pipe)
					refusedWith(t, err, KindLink)
				} else {
					_, err := p.files.WriteGitignore(context.Background(), p.pipe)
					refusedWith(t, err, KindLink)
				}
				sameSnapshot(t, before, snapshot(t, p.pipe))
			})
		}
	}
}

func TestADirectoryAtEitherNameIsReported(t *testing.T) {
	t.Parallel()
	for _, file := range []string{EnvFile, GitignoreFile} {
		p := newProject(t)
		if err := os.Mkdir(p.path(file), 0o755); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, p.pipe)
		_, err := p.write(t, map[string]string{"K": canary})
		refusedWith(t, err, KindNotRegularFile)
		sameSnapshot(t, before, snapshot(t, p.pipe))
	}
}

func TestAnUnreadableFileIsReportedAndNothingWritten(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	for _, file := range []string{EnvFile, GitignoreFile} {
		p := newProject(t)
		write(t, p.path(file), "A=1\n")
		if err := os.Chmod(p.path(file), 0o000); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, p.pipe)
		_, err := p.write(t, map[string]string{"K": canary})
		refusal := refusedWith(t, err, KindRefused)
		if refusal[0].Guard != projectfs.KindUnreadable || refusal[0].File != file {
			t.Fatalf("%+v", refusal)
		}
		sameSnapshot(t, before, snapshot(t, p.pipe))
	}
}

// A directory that cannot be written stops the .gitignore write, and so .env
// is not written either.
func TestAnUnwritableDirectoryStopsTheGitignoreAndSoTheEnv(t *testing.T) {
	t.Parallel()
	skipIfRoot(t)
	p := newProject(t)
	if err := os.Chmod(p.pipe, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.pipe, 0o755) })
	before := snapshot(t, p.pipe)
	report, err := p.write(t, map[string]string{"K": canary})
	refusal := refusedWith(t, err, KindRefused)
	if refusal[0].File != GitignoreFile || report.Gitignore != NotWritten || report.Env != NotWritten {
		t.Fatalf("%+v %+v", report, refusal)
	}
	sameSnapshot(t, before, snapshot(t, p.pipe))
}

func TestARefusedGitignoreMeansNoEnv(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if err := os.Symlink("elsewhere", p.path(GitignoreFile)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.write(t, map[string]string{"K": canary}); err == nil {
		t.Fatal("no error")
	}
	if _, err := os.Lstat(p.path(EnvFile)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(".env was written")
	}
}

// The .gitignore write fails, after both files were checked: .env stays as it
// was.
func TestAGitignoreWriteThatFailsLeavesTheEnvAlone(t *testing.T) {
	skipIfRoot(t)
	p := newProject(t)
	write(t, p.path(EnvFile), "A=1\n")
	if err := os.Chmod(p.path(EnvFile), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p.pipe, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.pipe, 0o755) })
	_, err := p.write(t, map[string]string{"K": canary})
	refusedWith(t, err, KindRefused)
	if p.env(t) != "A=1\n" {
		t.Fatal(".env changed")
	}
}

func TestATooLargeEnvIsReportedAndNotRead(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	write(t, p.path(EnvFile), strings.Repeat("A=1234567\n", MaxFileSize/10+1))
	before := snapshot(t, p.pipe)
	_, err := p.write(t, map[string]string{"K": canary})
	refusedWith(t, err, KindTooLarge)
	_, err = p.files.ReadEnv(p.pipe)
	refusedWith(t, err, KindTooLarge)
	sameSnapshot(t, before, snapshot(t, p.pipe))
}

func TestAPipelineDirectoryOutsideTheRootIsTheGuardsForbidden(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(p.root, "escape")); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{outside, filepath.Join(p.root, "escape"), filepath.Join(p.root, "..", "x")} {
		_, err := p.files.WriteEnv(context.Background(), dir, secretsOf(map[string]string{"K": canary}))
		refusal := refusedWith(t, err, KindRefused)
		if refusal[0].Guard != projectfs.KindForbidden || !projectfs.IsForbidden(err) {
			t.Fatalf("%s: %+v", dir, refusal)
		}
		_, err = p.files.WriteGitignore(context.Background(), dir)
		if !projectfs.IsForbidden(err) {
			t.Fatalf("%s: %v", dir, err)
		}
		_, err = p.files.ReadEnv(dir)
		if !projectfs.IsForbidden(err) {
			t.Fatalf("%s: %v", dir, err)
		}
	}
	entries, _ := os.ReadDir(outside)
	if len(entries) != 0 {
		t.Fatal("something was written outside")
	}
}

func TestARevokedRootIsTheGuardsForbidden(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	if _, err := p.guard.Revoke(p.root); err != nil {
		t.Fatal(err)
	}
	_, err := p.write(t, map[string]string{"K": canary})
	if refusedWith(t, err, KindRefused)[0].Guard != projectfs.KindForbidden {
		t.Fatal(err)
	}
}

func TestAMissingPipelineDirectoryIsReported(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	_, err := p.files.WriteEnv(context.Background(), p.path("missing"), nil)
	if refusedWith(t, err, KindRefused)[0].Guard != projectfs.KindNotFound {
		t.Fatal(err)
	}
	if _, err := os.Lstat(p.path("missing")); err == nil {
		t.Fatal("created")
	}
}

// An interrupted .env write leaves the previous file intact; its temporary
// file exists only while it is being written, and is no wider than .env.
func TestAnInterruptedWriteLeavesThePreviousFileAndANarrowTemporaryFile(t *testing.T) {
	t.Parallel()
	for name, previous := range map[string]fs.FileMode{"new": 0, "0600": 0o600, "0640": 0o640, "0400": 0o400} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			p := newProject(t)
			if previous != 0 {
				write(t, p.path(EnvFile), "A=1\n")
				if err := os.Chmod(p.path(EnvFile), previous); err != nil {
					t.Fatal(err)
				}
			}
			want := previous
			if want == 0 {
				want = 0o600
			}
			var seen []fs.FileMode
			p.files.hooks.envEmit = func() error {
				matches, _ := filepath.Glob(filepath.Join(p.pipe, "..env.tmp-*"))
				for _, match := range matches {
					seen = append(seen, mode(t, match))
				}
				return errors.New("interrupted")
			}
			before := snapshot(t, p.pipe, GitignoreFile)
			report, err := p.write(t, map[string]string{"K": canary})
			if refusedWith(t, err, KindRefused)[0].Guard != projectfs.KindIO || report.Env != NotWritten {
				t.Fatalf("%+v %v", report, err)
			}
			if len(seen) != 1 || seen[0] != want {
				t.Fatalf("temporary file modes %v, want [%v]", seen, want)
			}
			sameSnapshot(t, before, snapshot(t, p.pipe, GitignoreFile))
		})
	}
}

func TestALeftoverTemporaryFileIsIgnoredAndRemovedByTheNextWrite(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	gitInit(t, p.ceiling(), p.pipe)
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	leftover := p.path("..env.tmp-0123456789abcdef")
	write(t, leftover, "A=\""+canary+"\"\n")
	if err := os.Chmod(leftover, 0o600); err != nil {
		t.Fatal(err)
	}
	if !gitIgnores(t, p.ceiling(), p.pipe, filepath.Base(leftover)) {
		t.Fatal("the leftover is not ignored")
	}
	out, _ := git(t, p.ceiling(), p.pipe, "status", "--porcelain", "--untracked-files=all")
	if strings.Contains(out, "tmp") || strings.Contains(out, ".env") {
		t.Fatalf("git status: %s", out)
	}
	// A link by that name is not ours to remove.
	if err := os.Symlink("x", p.path("..env.tmp-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.write(t, map[string]string{"A": "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(leftover); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the leftover is still there")
	}
	if _, err := os.Lstat(p.path("..env.tmp-link")); err != nil {
		t.Fatal("the link was removed")
	}
}

// Not parallel: the umask is the process's.
func TestANewEnvIsOwnerOnlyWhateverTheUmask(t *testing.T) {
	for _, umask := range []int{0, 0o022, 0o077} {
		old := syscall.Umask(umask)
		p := newProject(t)
		_, err := p.write(t, map[string]string{"K": "v"})
		syscall.Umask(old)
		if err != nil {
			t.Fatal(err)
		}
		if got := mode(t, p.path(EnvFile)); got != 0o600 {
			t.Errorf("umask %o: .env mode %o", umask, got)
		}
		if got := mode(t, p.path(GitignoreFile)); got != 0o644 {
			t.Errorf("umask %o: .gitignore mode %o", umask, got)
		}
	}
}

func TestARewriteKeepsTheEnvsModeAndWarnsWhenOthersCanRead(t *testing.T) {
	t.Parallel()
	for _, previous := range []fs.FileMode{0o600, 0o400, 0o640, 0o604, 0o644} {
		p := newProject(t)
		write(t, p.path(EnvFile), "A=1\n")
		if err := os.Chmod(p.path(EnvFile), previous); err != nil {
			t.Fatal(err)
		}
		report, err := p.write(t, map[string]string{"K": "v"})
		if err != nil {
			t.Fatal(err)
		}
		if got := mode(t, p.path(EnvFile)); got != previous {
			t.Errorf("mode %o became %o", previous, got)
		}
		exposed := hasKind(report.Problems, KindExposedMode)
		if (exposed != nil) != (previous&0o044 != 0) {
			t.Errorf("mode %o: warnings %+v", previous, report.Problems)
		}
		if exposed != nil && (exposed.File != EnvFile || !exposed.Warning || !strings.Contains(p.logs.String(), "exposedMode")) {
			t.Errorf("%+v", exposed)
		}
	}
}

// Two writes to one directory do not interleave: the second waits for the
// first, and each keeps the other's keys.
func TestTwoWritesToOneDirectoryDoNotInterleave(t *testing.T) {
	t.Parallel()
	p := newProject(t)
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
	var wg sync.WaitGroup
	errs := make([]error, 2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[0] = p.write(t, map[string]string{"FIRST": "1"})
	}()
	<-entered
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, errs[1] = p.write(t, map[string]string{"SECOND": "2"})
	}()
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
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil || len(env.Entries) != 2 {
		t.Fatalf("%+v %v", env, err)
	}
}

func TestManyConcurrentWritesKeepEveryKey(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	var wg sync.WaitGroup
	keys := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	for _, key := range keys {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.write(t, map[string]string{key: key}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	env, err := p.files.ReadEnv(p.pipe)
	if err != nil || len(env.Entries) != len(keys) {
		t.Fatalf("%+v %v", env.Entries, err)
	}
}

func TestACancelledWaitWritesNothing(t *testing.T) {
	t.Parallel()
	p := newProject(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	before := snapshot(t, p.pipe)
	if _, err := p.files.WriteEnv(ctx, p.pipe, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	sameSnapshot(t, before, snapshot(t, p.pipe))
}
