//go:build unix

package process

import (
	"os"
	"os/exec"
	"syscall"
)

// supported says this build can start a process: a unix one, where the child
// gets a process group of its own that can be killed whole.
const supported = true

// isolate puts the child in a new process group, whose id is its pid, and
// makes a context that ends kill that whole group rather than the child
// alone.
func isolate(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		killGroup(command.Process.Pid)
		return nil
	}
}

// killGroup sends SIGKILL to every process still in the group the child led.
// An empty group answers an error, which is the outcome wanted, so it is not
// reported.
func killGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// signalOf answers the signal that ended the child, if one did.
func signalOf(state *os.ProcessState) (string, bool) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() {
		return "", false
	}
	return status.Signal().String(), true
}
