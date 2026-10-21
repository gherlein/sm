//go:build !unix

package link

import (
	"io/fs"
	"os"
)

// readOnlyNoBlockFlags has no non-blocking variant off unix; FIFOs are a unix
// concept, so plain read-only is equivalent there.
const readOnlyNoBlockFlags = os.O_RDONLY

// ownedByCurrentUser cannot inspect POSIX ownership off unix, so it accepts
// every entry; the world-writable and identity checks still apply.
func ownedByCurrentUser(info fs.FileInfo) (bool, error) {
	return true, nil
}
