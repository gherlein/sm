//go:build !unix

package skillmeta

import "os"

// ReadOnlyNonBlocking falls back to a plain read-only open where O_NONBLOCK is
// unavailable; FIFOs are a unix concept.
const ReadOnlyNonBlocking = os.O_RDONLY

func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, ReadOnlyNonBlocking, 0)
}
