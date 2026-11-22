//go:build unix

package process_test

// The fake child: this test binary run again through process.ForTest, with
// fakeMarker as its first argument and a mode after it. TestMain hands such a
// run to fakeChild before any test flag is parsed, so the binary behaves as
// the mode says and exits.

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zssvaidar/kpv/agent/internal/process"
)

const fakeMarker = "kpv-fake-child"

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == fakeMarker {
		os.Exit(fakeChild(os.Args[2:]))
	}
	os.Exit(m.Run())
}

func fakeChild(args []string) int {
	mode := args[0]
	switch mode {
	case "echo":
		_, _ = io.Copy(os.Stdout, os.Stdin)
	case "env":
		_, _ = os.Stdout.WriteString(strings.Join(os.Environ(), "\n"))
	case "cwd":
		directory, _ := os.Getwd()
		_, _ = os.Stdout.WriteString(directory)
	case "ignore-stdin":
	case "exit":
		code, _ := strconv.Atoi(args[1])
		_, _ = os.Stderr.WriteString(args[2])
		_, _ = os.Stdout.WriteString("never returned")
		return code
	case "signal":
		_, _ = os.Stderr.WriteString("about to die")
		_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
		time.Sleep(time.Hour)
	case "hang", "sleep":
		time.Sleep(time.Hour)
	case "flood":
		chunk := make([]byte, 4096)
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				return 0
			}
		}
	case "write":
		n, _ := strconv.Atoi(args[1])
		_, _ = os.Stdout.Write(make([]byte, n))
	case "stderr":
		n, _ := strconv.Atoi(args[1])
		_, _ = os.Stderr.Write([]byte(strings.Repeat("e", n)))
		_, _ = os.Stdout.WriteString("done")
	case "grandchild", "escape":
		self, err := os.Executable()
		if err != nil {
			return 90
		}
		grandchild := exec.Command(self, fakeMarker, "sleep")
		grandchild.Stdout = os.Stdout
		grandchild.Stderr = os.Stderr
		if mode == "escape" {
			// Its own group, so the group kill misses it and it keeps the
			// pipes open.
			grandchild.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		}
		if err := grandchild.Start(); err != nil {
			return 91
		}
		_, _ = os.Stderr.WriteString(strconv.Itoa(grandchild.Process.Pid) + "\n")
		switch args[1] {
		case "hang":
			time.Sleep(time.Hour)
		case "fail":
			return 3
		case "flood":
			return fakeChild([]string{"flood"})
		}
	default:
		return 99
	}
	return 0
}

// fake is a Program that runs this binary in the given mode.
func fake(t *testing.T, limits process.Limits, mode ...string) process.Program {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	return process.ForTest(self, append([]string{fakeMarker}, mode...), nil, limits)
}

// generous are limits no test means to reach.
var generous = process.Limits{Timeout: 30 * time.Second, MaxStdout: 64 << 20}

// gone reports whether pid is dead within a couple of seconds. A zombie is
// dead: it runs nothing and is only waiting for whoever adopted it to reap it.
func gone(pid int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			if syscall.Kill(pid, 0) != nil {
				return true
			}
		} else if fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:])); len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// pidIn reads the grandchild's pid off the first line the fake wrote to
// stderr.
func pidIn(t *testing.T, stderr []byte) int {
	t.Helper()
	line, _, _ := strings.Cut(string(stderr), "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || pid <= 0 {
		t.Fatalf("the fake child reported no grandchild pid on stderr: %q", stderr)
	}
	return pid
}
