//go:build unix

package secrets

// The umask is a unix notion, so this test is unix-only; it was in
// fs_test.go until #37 asked for GOOS=windows go vet ./... to pass.

import (
	"syscall"
	"testing"
)

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
