//go:build !unix

package process

import (
	"os"
	"os/exec"
)

// supported is false: without process groups there is no way to kill
// everything a child started, so no child is started at all and Run answers
// unsupported. Job objects on Windows are #95.
const supported = false

func isolate(*exec.Cmd) {}

func killGroup(int) {}

func signalOf(*os.ProcessState) (string, bool) { return "", false }
