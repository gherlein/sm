//go:build unix

package skillmeta

import (
	"os"
	"syscall"
)

// ReadOnlyNonBlocking opens a SKILL.md without blocking, so a path swapped to
// a FIFO after a pre-check cannot hang the open; the caller then verifies the
// handle is a regular file (ReadNameFromFile does).
const ReadOnlyNonBlocking = os.O_RDONLY | syscall.O_NONBLOCK

func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, ReadOnlyNonBlocking, 0)
}
