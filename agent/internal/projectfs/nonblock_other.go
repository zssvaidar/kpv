//go:build !unix

package projectfs

import "runtime"

// openNonblock is zero where there is no O_NONBLOCK. A directory is then
// opened as "name/." instead; see openToRead.
const openNonblock = 0

// probesFiles is true only on Windows, where opening a file for reading does
// not wait on a writer. Elsewhere a file's readability is not probed, and a
// file is reported unreadable only when it cannot be described at all.
const probesFiles = runtime.GOOS == "windows"
