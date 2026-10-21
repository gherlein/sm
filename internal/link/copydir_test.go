package link

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyDir_FaithfulTree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeTestFile(t, filepath.Join(src, "SKILL.md"), "skill body")
	writeTestFile(t, filepath.Join(src, "nested", "deep", "a.txt"), "nested content")
	writeTestFile(t, filepath.Join(src, "extra.txt"), "extra")

	report, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{})
	if err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("unexpected skipped entries: %v", report.Skipped)
	}

	for rel, want := range map[string]string{
		"SKILL.md":          "skill body",
		"nested/deep/a.txt": "nested content",
		"extra.txt":         "extra",
	} {
		got, err := os.ReadFile(filepath.Join(dst, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	if _, err := os.Lstat(filepath.Join(dst, copyMarker)); !os.IsNotExist(err) {
		t.Errorf("marker %s must not exist in dst, lstat err = %v", copyMarker, err)
	}
}

func TestCopyDir_RefusesExisting(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeTestFile(t, filepath.Join(src, "SKILL.md"), "new")
	writeTestFile(t, filepath.Join(dst, "keep.txt"), "original")

	if _, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{}); err == nil {
		t.Fatal("CopyDir over a non-empty dst must fail")
	}
	assertNoScratchDirs(t, root)

	got, err := os.ReadFile(filepath.Join(dst, "keep.txt"))
	if err != nil || string(got) != "original" {
		t.Errorf("dst changed: content %q, err %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("dst gained SKILL.md, lstat err = %v", err)
	}

	// An empty existing directory is also refused, not silently replaced.
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyDir(src, identityOf(t, src), empty, CopyOptions{}); err == nil {
		t.Fatal("CopyDir over an existing empty dst must fail")
	}
	if _, err := os.Lstat(filepath.Join(empty, "SKILL.md")); !os.IsNotExist(err) {
		t.Errorf("empty dst gained SKILL.md, lstat err = %v", err)
	}
}

func TestCopyDir_NoStageLeftOnSuccess(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	writeTestFile(t, filepath.Join(src, "SKILL.md"), "body")

	if _, err := CopyDir(src, identityOf(t, src), dst, CopyOptions{}); err != nil {
		t.Fatalf("CopyDir: %v", err)
	}
	if _, err := os.Lstat(dst + ".sm-stage"); !os.IsNotExist(err) {
		t.Errorf("stage dir remains, lstat err = %v", err)
	}
	assertNoScratchDirs(t, root)
	if _, err := os.Lstat(filepath.Join(dst, stageSentinel)); !os.IsNotExist(err) {
		t.Errorf("stage sentinel left in dst, lstat err = %v", err)
	}
}
