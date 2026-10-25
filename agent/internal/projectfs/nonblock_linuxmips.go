//go:build linux && (mips || mipsle || mips64 || mips64le)

package projectfs

// openNonblock is O_NONBLOCK on this platform. See nonblock_linux.go.
const openNonblock = 0x80

// probesFiles: see nonblock_linux.go.
const probesFiles = true
