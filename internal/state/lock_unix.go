//go:build unix

package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock takes an exclusive, non-blocking flock on <path>.lock so two runs can
// never interleave their load-modify-save of the file at path and lose each
// other's records. A second run fails at once instead of waiting: sm and ms are
// interactive, and a silent hang is worse than a clear message. holder names
// the tool the lock belongs to ("sm" for the state file, "ms" for its ingest
// ledger) so the contention message points at the right process. The lock file
// is opened with O_NOFOLLOW so a planted symlink cannot make the caller create
// or lock a file elsewhere. The returned unlock releases the lock and closes
// the file.
func Lock(path, holder string) (unlock func() error, err error) {
	if holder == "" {
		return nil, errors.New("lock: holder name must not be empty")
	}
	lockPath := path + lockSuffix
	if err := os.MkdirAll(filepath.Dir(lockPath), stateDirMode); err != nil {
		return nil, fmt.Errorf("create state dir: %w", err)
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, lockFileMode)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		closeErr := file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(fmt.Errorf("another %s process is running (lock %s)", holder, lockPath), closeErr)
		}
		return nil, errors.Join(fmt.Errorf("lock %s: %w", lockPath, err), closeErr)
	}
	return func() error {
		unlockErr := syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		if unlockErr != nil {
			unlockErr = fmt.Errorf("unlock %s: %w", lockPath, unlockErr)
		}
		closeErr := file.Close()
		if closeErr != nil {
			closeErr = fmt.Errorf("close lock %s: %w", lockPath, closeErr)
		}
		return errors.Join(unlockErr, closeErr)
	}, nil
}
