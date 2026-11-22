//go:build unix

package catalog

import (
	"syscall"
	"testing"
)

// mkfifo makes a named pipe at path. It is in a unix-only file, beside a
// stub for other systems, so GOOS=windows go vet ./... passes (#37).
func mkfifo(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Fatal(err)
	}
}
