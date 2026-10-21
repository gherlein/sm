//go:build unix

package skillmeta

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReadName_RejectsNonRegular(t *testing.T) {
	directory := t.TempDir()

	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	fifoLink := filepath.Join(directory, "link-to-fifo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(fifoLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fifo, fifoLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadName(fifoLink); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("fifo symlink: err = %v, want not a regular file", err)
	}

	directoryNamedSkill := filepath.Join(directory, "dir", "SKILL.md")
	if err := os.MkdirAll(directoryNamedSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadName(directoryNamedSkill); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: err = %v, want not a regular file", err)
	}
}
