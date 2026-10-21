//go:build unix

package link

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// specialFileTimeout bounds how long a copy containing a FIFO may take; the
// old implementation blocked forever opening the pipe.
const specialFileTimeout = 10 * time.Second

func TestCopyDir_SkipsSpecialFiles(t *testing.T) {
	root := t.TempDir()
	src := makeStoreSkill(t, root, "alpha")
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(root, "dst")

	type outcome struct {
		report CopyReport
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		report, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{})
		done <- outcome{report, err}
	}()
	var result outcome
	select {
	case result = <-done:
	case <-time.After(specialFileTimeout):
		t.Fatal("CopyDir hung on a FIFO")
	}
	if result.err != nil {
		t.Fatalf("CopyDir: %v", result.err)
	}
	assertReported(t, result.report, "pipe")
	assertAbsent(t, filepath.Join(dst, "pipe"))
	if got, err := os.ReadFile(filepath.Join(dst, "sub", "ref.txt")); err != nil || string(got) != "deep\n" {
		t.Errorf("rest of tree not copied: %q, %v", got, err)
	}
}
