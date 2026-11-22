//go:build unix

package process

import (
	"os"
	"syscall"
)

// supported says this build can start a process: a unix one, where the child
// gets a process group of its own that can be killed whole.
const supported = true

// processGroup is the child's attributes: a new process group, whose id is
// its pid, so killGroup can reach everything the child starts.
func processGroup() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
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
