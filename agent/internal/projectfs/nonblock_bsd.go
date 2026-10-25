//go:build aix || darwin || dragonfly || freebsd || netbsd || openbsd

package projectfs

// openNonblock is O_NONBLOCK on this platform. See nonblock_linux.go.
const openNonblock = 0x4

// probesFiles: see nonblock_linux.go.
const probesFiles = true
