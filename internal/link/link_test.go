package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brightsign-playground/sm/internal/resolve"
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

func TestApplyReplaceSymlink(t *testing.T) {
	// Test the REPLACE branch: updating an existing sm-created symlink.
	target := filepath.Join(t.TempDir(), "skills")
	src1, src2 := t.TempDir(), t.TempDir()

	// Create initial symlink to src1.
	installed, conflicts, err := Apply(target, []resolve.Link{{Name: "link", SourceDir: src1}}, nil, map[string]string{})
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("initial apply: %v %v", err, conflicts)
	}
	if installed["link"] != src1 {
		t.Fatalf("link not created: %+v", installed)
	}

	// Replace with symlink to src2.
	installed, conflicts, err = Apply(target, []resolve.Link{{Name: "link", SourceDir: src2}}, nil, installed)
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("replace apply: %v %v", err, conflicts)
	}
	if installed["link"] != src2 {
		t.Fatalf("link not updated: %+v", installed)
	}

	// Verify it's still a symlink and points to src2.
	fi, _ := os.Lstat(filepath.Join(target, "link"))
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("link is not a symlink")
	}
	target2, _ := os.Readlink(filepath.Join(target, "link"))
	if target2 != src2 {
		t.Fatalf("link points to %s, expected %s", target2, src2)
	}
}

func TestApplyUnsafeName(t *testing.T) {
	// Test that unsafe names in add are treated as conflicts and create nothing.
	target := filepath.Join(t.TempDir(), "skills")
	src := t.TempDir()

	installed, conflicts, err := Apply(target,
		[]resolve.Link{{Name: "..", SourceDir: src}, {Name: "good", SourceDir: src}},
		nil, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0] != ".." {
		t.Fatalf("unsafe name not reported as conflict: %v", conflicts)
	}
	if installed["good"] != src || installed[".."] != "" {
		t.Fatalf("installed wrong: %+v", installed)
	}
	// Verify ".." entry was never created inside target (list directory).
	entries, _ := os.ReadDir(target)
	for _, e := range entries {
		if e.Name() == ".." {
			t.Fatal("unsafe name was created")
		}
	}
	// Verify "good" was created.
	if fi, _ := os.Lstat(filepath.Join(target, "good")); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("good symlink was not created")
	}
}
