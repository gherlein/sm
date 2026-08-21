package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

func TestApplyCreateRemoveProtect(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills")
	srcA, srcC := t.TempDir(), t.TempDir()

	installed, conflicts, err := Apply(target, []resolve.Link{{Name: "a", SourceDir: srcA}, {Name: "b", SourceDir: srcA}}, nil, map[string]string{})
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("apply1: %v %v", err, conflicts)
	}
	os.Mkdir(filepath.Join(target, "manual"), 0o755) // unmanaged real dir

	installed, conflicts, err = Apply(target,
		[]resolve.Link{{Name: "manual", SourceDir: srcC}, {Name: "c", SourceDir: srcC}},
		[]string{"b"}, installed)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0] != "manual" {
		t.Fatalf("conflict wrong: %v", conflicts)
	}
	if _, err := os.Lstat(filepath.Join(target, "b")); !os.IsNotExist(err) {
		t.Fatal("b should be removed")
	}
	if fi, _ := os.Lstat(filepath.Join(target, "manual")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("manual must stay a real dir")
	}
	if installed["manual"] != "" || installed["c"] != srcC {
		t.Fatalf("installed wrong: %+v", installed)
	}
}
