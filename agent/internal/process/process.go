// Package process is the one place in the agent that may start a process
// (#37). It runs only Program values, every one of which outside a test comes
// from the table in programs.go: a bare executable name looked up on PATH, a
// fixed argument list, the environment variable names passed through, a
// timeout and a stdout cap. Run takes nothing else from its caller but a
// context and the bytes written to the child's stdin, so no path or other
// request value can become an argument, an environment value, a working
// directory or the executable. There is no shell. Every run is bounded by a
// timeout, a stdout cap and a retained-stderr cap, and ends with a kill of the
// child's whole process group, so nothing it started outlives it.
//
// agent/internal/projectfs/boundary_test.go holds these rules for the whole
// module: only this package may import os/exec or syscall, and only the
// packages on its importer list may import this one.
package process

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// MaxStderr is how much of a child's stderr is kept. The rest is read and
// discarded, so a child that writes more is never blocked or killed for it.
const MaxStderr = 64 << 10

// waitDelay bounds how long Run waits, after the child is killed or has
// exited, for pipes a stray grandchild still holds open. It is what keeps Run
// returning within a second of the kill even when a grandchild left the
// process group and kept stdout.
const waitDelay = 250 * time.Millisecond

// Limits bound one run of a program.
type Limits struct {
	// Timeout is how long the child may run. A context with an earlier
	// deadline wins.
	Timeout time.Duration
	// MaxStdout is the most stdout the child may write. One byte more kills
	// it and the run is outputTooLarge.
	MaxStdout int64
}

// Program is one thing Run may start. Its fields are unexported, so another
// package can only write the zero Program, which Run refuses; every non-test
// Program comes from the table in programs.go.
type Program struct {
	executable string
	args       []string
	env        []string
	limits     Limits
}

// Output is what a child that exited 0 left behind.
type Output struct {
	Stdout []byte
	// Stderr is the first MaxStderr bytes the child wrote to stderr.
	Stderr          []byte
	StderrTruncated bool
}

// Kind is why a run failed. The set is fixed, so a caller can switch on it.
type Kind string

// Every Kind an Error can have.
const (
	KindInvalidProgram Kind = "invalidProgram"
	KindNotFound       Kind = "notFound"
	KindExited         Kind = "exited"
	KindSignaled       Kind = "signaled"
	KindTimedOut       Kind = "timedOut"
	KindCanceled       Kind = "canceled"
	KindOutputTooLarge Kind = "outputTooLarge"
	KindUnsupported    Kind = "unsupported"
	KindStartFailed    Kind = "startFailed"
)

// sentences are the fixed words each Kind is said in.
var sentences = map[Kind]string{
	KindInvalidProgram: "the program is not one from the program table",
	KindNotFound:       "the program is not installed on PATH",
	KindExited:         "the program exited with a non-zero code",
	KindSignaled:       "the program was killed by a signal",
	KindTimedOut:       "the program ran past its time limit",
	KindCanceled:       "the run was canceled",
	KindOutputTooLarge: "the program wrote more output than it may",
	KindUnsupported:    "starting a process is not supported on this system",
	KindStartFailed:    "the program could not be started",
}

// Error is every error Run returns.
type Error struct {
	Kind Kind
	// Program is the executable's name as the program table gives it.
	Program string
	// ExitCode is set for KindExited.
	ExitCode int
	// Signal is set for KindSignaled: the signal's description as the
	// operating system gives it, such as "killed" or "terminated".
	Signal string
	// Stderr is what was kept of the child's stderr, for every kind where a
	// child ran.
	Stderr          []byte
	StderrTruncated bool
	// Err is the underlying cause where there is one, for a log.
	Err error
}

func (e *Error) Error() string {
	sentence := sentences[e.Kind]
	switch e.Kind {
	case KindExited:
		sentence = fmt.Sprintf("%s (%d)", sentence, e.ExitCode)
	case KindSignaled:
		sentence = fmt.Sprintf("%s (%s)", sentence, e.Signal)
	}
	if e.Program == "" {
		return "process: " + sentence
	}
	return fmt.Sprintf("process %s: %s", e.Program, sentence)
}

func (e *Error) Unwrap() error { return e.Err }

// KindOf answers the Kind of a process error, or "" for any other error.
func KindOf(err error) Kind {
	var typed *Error
	if errors.As(err, &typed) {
		return typed.Kind
	}
	return ""
}

var (
	errProgramTimedOut = errors.New("the program's timeout passed")
	errStdoutTooLarge  = errors.New("the program wrote more stdout than its cap")
)

// Run starts program, writes stdin to it and closes it, and waits for it to
// end. It answers the child's stdout and retained stderr when the child exits
// 0, and a *Error otherwise. The executable is looked up on PATH on every
// call; the child runs in / with only PATH and the program's own listed
// variables from the agent's environment. Whatever the outcome, every process
// left in the child's process group is killed before Run returns. Run is safe
// to call concurrently.
func Run(ctx context.Context, program Program, stdin []byte) (Output, error) {
	if program.executable == "" || program.limits.Timeout <= 0 || program.limits.MaxStdout <= 0 {
		return Output{}, &Error{Kind: KindInvalidProgram}
	}
	if !supported {
		return Output{}, &Error{Kind: KindUnsupported, Program: program.executable}
	}
	path, found := resolve(program.executable)
	if !found {
		return Output{}, &Error{Kind: KindNotFound, Program: program.executable}
	}

	timed, stopTimer := context.WithTimeoutCause(ctx, program.limits.Timeout, errProgramTimedOut)
	defer stopTimer()
	running, stop := context.WithCancelCause(timed)
	defer stop(nil)

	stdout := &cappedBuffer{limit: program.limits.MaxStdout, exceeded: func() { stop(errStdoutTooLarge) }}
	stderr := &retainedBuffer{}

	command := exec.CommandContext(running, path, program.args...)
	command.Args[0] = program.executable
	command.Env = environment(program.env)
	command.Dir = "/"
	command.Stdin = bytes.NewReader(stdin)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = waitDelay
	// The child leads a process group of its own, and a context that ends
	// kills that whole group rather than the child alone.
	command.SysProcAttr = processGroup()
	command.Cancel = func() error {
		killGroup(command.Process.Pid)
		return nil
	}

	if err := command.Start(); err != nil {
		if running.Err() != nil {
			return Output{}, interrupted(ctx, running, program, stderr)
		}
		return Output{}, &Error{Kind: KindStartFailed, Program: program.executable, Err: err}
	}
	waited := command.Wait()
	killGroup(command.Process.Pid)

	if stdout.overflowed() {
		return Output{}, failure(KindOutputTooLarge, program, stderr, nil)
	}
	state := command.ProcessState
	if state != nil && state.Success() {
		// ErrWaitDelay after a successful exit means a grandchild still held a
		// pipe; the child itself succeeded, and the group is killed above. A
		// child that exited 0 succeeded even if the deadline passed while its
		// pipes were being drained.
		if waited == nil || errors.Is(waited, exec.ErrWaitDelay) {
			kept, truncated := stderr.snapshot()
			return Output{Stdout: stdout.bytes(), Stderr: kept, StderrTruncated: truncated}, nil
		}
	}
	if waited != nil && running.Err() != nil {
		return Output{}, interrupted(ctx, running, program, stderr)
	}
	if state != nil {
		if signal, signaled := signalOf(state); signaled {
			failed := failure(KindSignaled, program, stderr, waited)
			failed.Signal = signal
			return Output{}, failed
		}
		if code := state.ExitCode(); code > 0 {
			failed := failure(KindExited, program, stderr, waited)
			failed.ExitCode = code
			return Output{}, failed
		}
	}
	return Output{}, failure(KindStartFailed, program, stderr, waited)
}

// interrupted classifies a run whose context ended before the child did.
func interrupted(ctx context.Context, running context.Context, program Program, stderr *retainedBuffer) *Error {
	switch cause := context.Cause(running); {
	case errors.Is(cause, errStdoutTooLarge):
		return failure(KindOutputTooLarge, program, stderr, nil)
	case errors.Is(cause, errProgramTimedOut), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return failure(KindTimedOut, program, stderr, nil)
	default:
		return failure(KindCanceled, program, stderr, nil)
	}
}

func failure(kind Kind, program Program, stderr *retainedBuffer, err error) *Error {
	kept, truncated := stderr.snapshot()
	return &Error{Kind: kind, Program: program.executable, Stderr: kept, StderrTruncated: truncated, Err: err}
}

// resolve looks the executable up on PATH as PATH is now, so a toolchain
// installed after the agent started is found. A name found only through a
// relative PATH entry, the current directory included, is not found: the
// agent's working directory is not a place programs come from. An absolute
// path is taken as it is; only ForTest can write one.
func resolve(executable string) (string, bool) {
	if strings.ContainsRune(executable, '/') && !filepath.IsAbs(executable) {
		return "", false
	}
	path, err := exec.LookPath(executable)
	if err != nil || !filepath.IsAbs(path) {
		return "", false
	}
	return path, true
}

// environment is PATH plus the program's own listed names, with the agent's
// values. A listed name the agent does not have is left out.
func environment(names []string) []string {
	out := []string{"PATH=" + os.Getenv("PATH")}
	for _, name := range names {
		if name == "PATH" {
			continue
		}
		if value, set := os.LookupEnv(name); set {
			out = append(out, name+"="+value)
		}
	}
	return out
}

// cappedBuffer keeps stdout up to its limit. The first byte past it calls
// exceeded, which kills the child, and everything after is discarded so the
// child is never blocked writing.
type cappedBuffer struct {
	mu       sync.Mutex
	limit    int64
	buffer   bytes.Buffer
	over     bool
	exceeded func()
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.over {
		return len(p), nil
	}
	if int64(b.buffer.Len())+int64(len(p)) > b.limit {
		b.over = true
		b.buffer = bytes.Buffer{}
		b.exceeded()
		return len(p), nil
	}
	b.buffer.Write(p)
	return len(p), nil
}

func (b *cappedBuffer) overflowed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.over
}

func (b *cappedBuffer) bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buffer.Bytes())
}

// retainedBuffer keeps the first MaxStderr bytes and discards the rest.
type retainedBuffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	truncated bool
}

func (b *retainedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	room := MaxStderr - b.buffer.Len()
	if len(p) > room {
		b.buffer.Write(p[:room])
		b.truncated = true
		return len(p), nil
	}
	b.buffer.Write(p)
	return len(p), nil
}

func (b *retainedBuffer) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Clone(b.buffer.Bytes()), b.truncated
}
