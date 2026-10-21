package link

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seamTimeout bounds a call that used to recurse without end (a source that
// contains its own destination); finishing at all is part of the assertion.
const seamTimeout = 10 * time.Second

// replaceSeam swaps a test seam for the duration of one test.
func replaceSeam[T any](t *testing.T, seam *T, replacement T) {
	t.Helper()
	original := *seam
	*seam = replacement
	t.Cleanup(func() { *seam = original })
}

// swapOnce returns a seam body that, on its first call only, moves the entry
// at the seam's path into a graveyard directory and renames replacement into
// its place, the way a concurrent writer would win the race at that point.
func swapOnce(t *testing.T, replacement string) func(path string) {
	t.Helper()
	graveyard := t.TempDir()
	done := false
	return func(path string) {
		if done {
			return
		}
		done = true
		if err := os.Rename(path, filepath.Join(graveyard, filepath.Base(path))); err != nil {
			t.Errorf("seam: move %s away: %v", path, err)
			return
		}
		if err := os.Rename(replacement, path); err != nil {
			t.Errorf("seam: move %s into %s: %v", replacement, path, err)
		}
	}
}

// makeUserDir writes a directory standing in for a user's own data and
// returns it with a snapshot of its content.
func makeUserDir(t *testing.T) (string, map[string]string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "user-data")
	writeTestFile(t, filepath.Join(dir, "mine.txt"), "precious")
	writeTestFile(t, filepath.Join(dir, "nested", "deep.txt"), "also precious")
	return dir, treeSnapshot(t, dir)
}

// finishWithin runs call and fails the test if it does not return in time.
func finishWithin(t *testing.T, label string, call func() error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- call() }()
	select {
	case err := <-done:
		return err
	case <-time.After(seamTimeout):
		t.Fatalf("%s did not finish within %s", label, seamTimeout)
		return nil
	}
}

// identityOf is the identity a validating caller records for path.
func identityOf(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}

// pinOf is what store enumeration records for the skill at path: its
// directory and the SKILL.md file it validated (followed through a link).
func pinOf(t *testing.T, path string) StorePin {
	t.Helper()
	return StorePin{Dir: identityOf(t, path), Manifest: identityOf(t, filepath.Join(path, skillFileName))}
}

// The wrappers below keep the new tests independent of how a source's
// validated identity is handed to the copy functions.

func testCopyDir(t *testing.T, src, dst string) (CopyReport, error) {
	t.Helper()
	return CopyDir(src, identityOf(t, src), dst, CopyOptions{})
}

func testReplaceDir(t *testing.T, src, dst string) (CopyReport, error) {
	t.Helper()
	return ReplaceDir(src, identityOf(t, src), dst, CopyOptions{})
}

func testCreateCopy(t *testing.T, targetDir, name, storePath string) (CopyReport, error) {
	t.Helper()
	return CreateCopy(targetDir, name, storePath, pinOf(t, storePath))
}

func testRefreshCopy(t *testing.T, targetDir, name, storePath, recordedSource string) (CopyReport, error) {
	t.Helper()
	return RefreshCopy(targetDir, name, storePath, pinOf(t, storePath), recordedSource)
}
