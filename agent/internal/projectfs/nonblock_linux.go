//go:build linux && !mips && !mipsle && !mips64 && !mips64le

package projectfs

// openNonblock is O_NONBLOCK, which the os package does not export and the
// agent may not import syscall to reach. nonblock_test.go holds it to
// syscall.O_NONBLOCK on every platform the tests run on. See openToRead.
const openNonblock = 0x800

// probesFiles says whether a file may be opened to find out if it is
// readable: only where that open cannot block.
const probesFiles = true
