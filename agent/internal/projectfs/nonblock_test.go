//go:build unix

package projectfs

import (
	"syscall"
	"testing"
)

// The flag is written down as a number because the agent may not import
// syscall, so this is what keeps the number right: on every platform the
// suite runs on, it is the platform's own O_NONBLOCK.
func TestTheNonblockingFlagIsThePlatformOne(t *testing.T) {
	t.Parallel()
	if openNonblock != syscall.O_NONBLOCK {
		t.Fatalf("openNonblock is %#x, but O_NONBLOCK here is %#x", openNonblock, syscall.O_NONBLOCK)
	}
	if !probesFiles {
		t.Fatal("a platform with O_NONBLOCK should probe files")
	}
}
