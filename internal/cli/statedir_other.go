//go:build !unix

package cli

// tightenStateDir is a no-op where Unix permission bits and ownership do not
// apply.
func tightenStateDir(statePath string) error {
	return nil
}
