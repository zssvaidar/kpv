//go:build unix

package process_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/process"
)

// failed asserts err is a *process.Error of kind and answers a copy of it.
func failed(t *testing.T, err error, kind process.Kind) process.Error {
	t.Helper()
	var typed *process.Error
	if !errors.As(err, &typed) {
		t.Fatalf("got %v (%T), want a *process.Error of kind %s", err, err, kind)
	}
	if typed.Kind != kind {
		t.Fatalf("got kind %s (%v), want %s", typed.Kind, err, kind)
	}
	if process.KindOf(err) != kind {
		t.Fatalf("KindOf answers %s, want %s", process.KindOf(err), kind)
	}
	return *typed
}

func TestAZeroProgramStartsNothing(t *testing.T) {
	t.Parallel()
	_, err := process.Run(context.Background(), process.Program{}, []byte("x"))
	failed(t, err, process.KindInvalidProgram)
}

func TestAProgramWithoutLimitsIsInvalid(t *testing.T) {
	t.Parallel()
	for _, limits := range []process.Limits{{Timeout: 0, MaxStdout: 1}, {Timeout: time.Second, MaxStdout: 0}, {Timeout: -1, MaxStdout: -1}} {
		_, err := process.Run(context.Background(), fake(t, limits, "echo"), nil)
		failed(t, err, process.KindInvalidProgram)
	}
}

func TestAnExecutableThatIsNotInstalledIsNotFound(t *testing.T) {
	t.Parallel()
	program := process.ForTest("kpv-no-such-program-anywhere", nil, nil, generous)
	_, err := process.Run(context.Background(), program, nil)
	failed(t, err, process.KindNotFound)

	relative := process.ForTest("bin/kpv-relative", nil, nil, generous)
	_, err = process.Run(context.Background(), relative, nil)
	failed(t, err, process.KindNotFound)
}

// install links the test binary into directory under name, which is how a
// toolchain appears on PATH.
func install(t *testing.T, directory, name string) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	if err := os.Symlink(self, filepath.Join(directory, name)); err != nil {
		t.Fatalf("install %s: %v", name, err)
	}
}

// Not parallel: it changes PATH and the working directory.
func TestAnExecutableFoundOnlyInTheCurrentDirectoryIsNotFound(t *testing.T) {
	directory := t.TempDir()
	install(t, directory, "kpv-fake-tool")
	t.Chdir(directory)
	for _, path := range []string{".", "", "./"} {
		t.Setenv("PATH", path)
		_, err := process.Run(context.Background(), process.ForTest("kpv-fake-tool", []string{fakeMarker, "echo"}, nil, generous), nil)
		failed(t, err, process.KindNotFound)
	}
}

// Not parallel: it changes PATH.
func TestTheExecutableIsLookedUpOnEveryRun(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("PATH", directory)
	program := process.ForTest("kpv-late-tool", []string{fakeMarker, "echo"}, nil, generous)
	_, err := process.Run(context.Background(), program, []byte("hi"))
	failed(t, err, process.KindNotFound)

	install(t, directory, "kpv-late-tool")
	output, err := process.Run(context.Background(), program, []byte("hi"))
	if err != nil {
		t.Fatalf("a tool installed after the first run is not found: %v", err)
	}
	if string(output.Stdout) != "hi" {
		t.Fatalf("stdout %q, want hi", output.Stdout)
	}
}

// Not parallel: it sets variables in the agent's environment.
func TestTheChildSeesOnlyPathAndItsListedVariables(t *testing.T) {
	t.Setenv("KPV_PROCESS_TEST_SECRET", "do-not-pass")
	t.Setenv("KPV_PROCESS_TEST_LISTED", "pass-me")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := process.ForTest(self, []string{fakeMarker, "env"}, []string{"KPV_PROCESS_TEST_LISTED", "KPV_PROCESS_TEST_UNSET"}, generous)
	output, err := process.Run(context.Background(), program, nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := strings.Split(string(output.Stdout), "\n")
	sort.Strings(got)
	want := []string{"KPV_PROCESS_TEST_LISTED=pass-me", "PATH=" + os.Getenv("PATH")}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("the child's environment is %q, want exactly %q", got, want)
	}
}

func TestTheChildRunsInTheRootDirectory(t *testing.T) {
	t.Parallel()
	output, err := process.Run(context.Background(), fake(t, generous, "cwd"), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(output.Stdout) != "/" {
		t.Fatalf("the child ran in %q, want /", output.Stdout)
	}
}

func TestStdinIsWrittenAndClosed(t *testing.T) {
	t.Parallel()
	input := []byte("é and more\n")
	output, err := process.Run(context.Background(), fake(t, generous, "echo"), input)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// echo copies until end of input, so it returning at all says stdin was
	// closed.
	if !bytes.Equal(output.Stdout, input) {
		t.Fatalf("stdout %q, want %q", output.Stdout, input)
	}
}

func TestAChildThatIgnoresItsStdinSucceeds(t *testing.T) {
	t.Parallel()
	// Far more than a pipe holds, so the write cannot finish before the child
	// exits.
	output, err := process.Run(context.Background(), fake(t, generous, "ignore-stdin"), bytes.Repeat([]byte("x"), 4<<20))
	if err != nil {
		t.Fatalf("a child that exits 0 without reading stdin failed: %v", err)
	}
	if len(output.Stdout) != 0 {
		t.Fatalf("stdout %q, want none", output.Stdout)
	}
}

func TestAChildThatExitsZeroReturnsStdoutAndStderr(t *testing.T) {
	t.Parallel()
	output, err := process.Run(context.Background(), fake(t, generous, "stderr", "5"), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if string(output.Stdout) != "done" || string(output.Stderr) != "eeeee" || output.StderrTruncated {
		t.Fatalf("got %+v", output)
	}
}

func TestAChildThatExitsNonZeroIsExited(t *testing.T) {
	t.Parallel()
	_, err := process.Run(context.Background(), fake(t, generous, "exit", "3", "it broke"), nil)
	typed := failed(t, err, process.KindExited)
	if typed.ExitCode != 3 || string(typed.Stderr) != "it broke" {
		t.Fatalf("got exit code %d and stderr %q", typed.ExitCode, typed.Stderr)
	}
}

func TestAChildKilledByASignalIsSignaled(t *testing.T) {
	t.Parallel()
	_, err := process.Run(context.Background(), fake(t, generous, "signal"), nil)
	typed := failed(t, err, process.KindSignaled)
	if typed.Signal != syscall.SIGTERM.String() || string(typed.Stderr) != "about to die" {
		t.Fatalf("got signal %q and stderr %q", typed.Signal, typed.Stderr)
	}
}

func TestAChildPastTheProgramTimeoutIsTimedOut(t *testing.T) {
	t.Parallel()
	started := time.Now()
	_, err := process.Run(context.Background(), fake(t, process.Limits{Timeout: 200 * time.Millisecond, MaxStdout: 1024}, "hang"), nil)
	failed(t, err, process.KindTimedOut)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Run took %v", elapsed)
	}
}

func TestAChildPastTheContextDeadlineIsTimedOut(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := process.Run(ctx, fake(t, generous, "hang"), nil)
	failed(t, err, process.KindTimedOut)
}

func TestAContextCanceledBeforeItsDeadlineIsCanceled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	time.AfterFunc(200*time.Millisecond, cancel)
	_, err := process.Run(ctx, fake(t, generous, "hang"), nil)
	failed(t, err, process.KindCanceled)

	already, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	_, err = process.Run(already, fake(t, generous, "echo"), nil)
	failed(t, err, process.KindCanceled)
}

func TestStdoutPastTheCapIsOutputTooLarge(t *testing.T) {
	t.Parallel()
	limits := process.Limits{Timeout: 30 * time.Second, MaxStdout: 1024}
	started := time.Now()
	_, err := process.Run(context.Background(), fake(t, limits, "flood"), nil)
	typed := failed(t, err, process.KindOutputTooLarge)
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("a flooding child was not killed promptly: %v", elapsed)
	}
	if strings.Contains(typed.Error(), "\x00") {
		t.Fatal("the error carries stdout")
	}

	// One byte over, then a clean exit, is still too much; exactly the cap
	// is not.
	_, err = process.Run(context.Background(), fake(t, limits, "write", "1025"), nil)
	failed(t, err, process.KindOutputTooLarge)
	output, err := process.Run(context.Background(), fake(t, limits, "write", "1024"), nil)
	if err != nil || len(output.Stdout) != 1024 {
		t.Fatalf("exactly the cap: %d bytes, %v", len(output.Stdout), err)
	}
}

func TestOnlyTheFirst64KiBOfStderrIsKept(t *testing.T) {
	t.Parallel()
	output, err := process.Run(context.Background(), fake(t, generous, "stderr", "1048576"), nil)
	if err != nil {
		t.Fatalf("a child writing a lot of stderr failed: %v", err)
	}
	if len(output.Stderr) != process.MaxStderr || process.MaxStderr != 64<<10 || !output.StderrTruncated {
		t.Fatalf("kept %d bytes of stderr, truncated %v", len(output.Stderr), output.StderrTruncated)
	}
	if string(output.Stdout) != "done" {
		t.Fatalf("stdout %q", output.Stdout)
	}
	output, err = process.Run(context.Background(), fake(t, generous, "stderr", "65536"), nil)
	if err != nil || output.StderrTruncated || len(output.Stderr) != 65536 {
		t.Fatalf("exactly 64 KiB: %d bytes, truncated %v, %v", len(output.Stderr), output.StderrTruncated, err)
	}
}

// Whatever ends the run, a grandchild sleeping in the child's process group
// is gone after it.
func TestNothingInTheGroupOutlivesTheRun(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		limits process.Limits
		mode   []string
		kind   process.Kind
		cancel bool
	}{
		{"exits 0", generous, []string{"grandchild", "exit"}, "", false},
		{"timed out", process.Limits{Timeout: 300 * time.Millisecond, MaxStdout: 1024}, []string{"grandchild", "hang"}, process.KindTimedOut, false},
		{"canceled", generous, []string{"grandchild", "hang"}, process.KindCanceled, true},
		{"exited", generous, []string{"grandchild", "fail"}, process.KindExited, false},
		{"output too large", process.Limits{Timeout: 30 * time.Second, MaxStdout: 1024}, []string{"grandchild", "flood"}, process.KindOutputTooLarge, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if c.cancel {
				time.AfterFunc(300*time.Millisecond, cancel)
			}
			output, err := process.Run(ctx, fake(t, c.limits, c.mode...), nil)
			var stderr []byte
			if c.kind == "" {
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				stderr = output.Stderr
			} else {
				stderr = failed(t, err, c.kind).Stderr
			}
			if pid := pidIn(t, stderr); !gone(pid) {
				t.Fatalf("grandchild %d is still alive after Run returned", pid)
			}
		})
	}
}

// A grandchild that left the group survives the group kill and keeps the
// pipes open; WaitDelay is what still bounds Run.
func TestRunReturnsPromptlyWhenAnEscapedGrandchildHoldsThePipes(t *testing.T) {
	t.Parallel()
	for _, then := range []string{"hang", "exit"} {
		t.Run(then, func(t *testing.T) {
			t.Parallel()
			// For hang the kill is the timeout; for exit there is no kill, and
			// the child's own exit is what WaitDelay counts from.
			timeout := 300 * time.Millisecond
			if then == "exit" {
				timeout = 30 * time.Second
			}
			started := time.Now()
			output, err := process.Run(context.Background(), fake(t, process.Limits{Timeout: timeout, MaxStdout: 1024}, "escape", then), nil)
			elapsed := time.Since(started)
			if then == "exit" {
				// Two start-ups of the test binary under -race happen first,
				// so this bound is looser; without WaitDelay it would be an
				// hour, the grandchild's sleep.
				timeout = 4 * time.Second
			}
			var stderr []byte
			if then == "hang" {
				stderr = failed(t, err, process.KindTimedOut).Stderr
			} else {
				if err != nil {
					t.Fatalf("run: %v", err)
				}
				stderr = output.Stderr
			}
			pid := pidIn(t, stderr)
			defer func() { _ = syscall.Kill(pid, syscall.SIGKILL) }()
			if elapsed > timeout+time.Second {
				t.Fatalf("Run took %v, more than a second past the kill", elapsed)
			}
		})
	}
}

func TestAFileThatCannotBeExecutedIsStartFailed(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "not-a-program")
	if err := os.WriteFile(path, []byte("this is not a program\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := process.Run(context.Background(), process.ForTest(path, nil, nil, generous), nil)
	failed(t, err, process.KindStartFailed)
}

func TestRunsInParallelDoNotMix(t *testing.T) {
	t.Parallel()
	var group sync.WaitGroup
	errs := make(chan error, 16)
	for index := range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			input := []byte(strings.Repeat(fmt.Sprintf("run %d ", index), 1000))
			output, err := process.Run(context.Background(), fake(t, generous, "echo"), input)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(output.Stdout, input) {
				errs <- fmt.Errorf("run %d got another run's output", index)
			}
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestForTestKeepsItsOwnCopies(t *testing.T) {
	t.Parallel()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{fakeMarker, "echo"}
	program := process.ForTest(self, args, nil, generous)
	args[1] = "hang"
	output, err := process.Run(context.Background(), program, []byte("copy"))
	if err != nil || string(output.Stdout) != "copy" {
		t.Fatalf("a change to the caller's slice changed the program: %q, %v", output.Stdout, err)
	}
}
