//go:build !unix

package catalog

import "testing"

// mkfifo skips the test off unix, which has no named pipes at a path.
func mkfifo(t *testing.T, _ string) {
	t.Helper()
	t.Skip("named pipes are unix-only")
}
