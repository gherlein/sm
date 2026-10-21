//go:build !unix

package state

// Lock is a no-op off unix, where flock is unavailable; sm supports only
// Linux and macOS, so this exists solely to keep the package compiling.
func Lock(path, holder string) (unlock func() error, err error) {
	return func() error { return nil }, nil
}
