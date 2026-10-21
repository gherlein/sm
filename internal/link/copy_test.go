package link

import (
	"os"
	"path/filepath"
	"testing"
)

// makeStoreSkill writes a minimal skill tree with a nested file to prove the
// whole directory is copied, not just SKILL.md.
func makeStoreSkill(t *testing.T, root, name string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: "+name+"\n---\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "ref.txt"), []byte("deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCreateCopy_MaterializesTreeWithMarker(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")

	report, err := CreateCopy(agentDir, "alpha", store, pinOf(t, store))
	if err != nil {
		t.Fatalf("CreateCopy: %v", err)
	}
	if len(report.Skipped) != 0 {
		t.Errorf("unexpected skipped entries: %v", report.Skipped)
	}
	dst := filepath.Join(agentDir, "alpha")

	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("copy is a symlink, want a real directory")
	}
	if got, _ := os.ReadFile(filepath.Join(dst, "sub", "ref.txt")); string(got) != "deep\n" {
		t.Errorf("nested file = %q", got)
	}
	marker, err := os.ReadFile(filepath.Join(dst, copyMarker))
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	if string(marker) != store+"\n" {
		t.Errorf("marker = %q, want %q", marker, store+"\n")
	}
	owned, err := IsOwnedCopy(agentDir, "alpha", store)
	if err != nil || !owned {
		t.Errorf("IsOwnedCopy = %v, %v; want true", owned, err)
	}
}

func TestIsOwnedCopy_FalseForForeignEntries(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A hand-placed real directory with no marker.
	if err := os.MkdirAll(filepath.Join(agentDir, "hand"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink.
	target := makeStoreSkill(t, root, "linked")
	if err := os.Symlink(target, filepath.Join(agentDir, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"hand", "linked", "absent"} {
		owned, err := IsOwnedCopy(agentDir, name, target)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if owned {
			t.Errorf("%s reported as owned copy", name)
		}
	}
}

func TestRemoveCopy_OnlyRemovesOwned(t *testing.T) {
	root := t.TempDir()
	store := makeStoreSkill(t, root, "alpha")
	agentDir := filepath.Join(root, "agent")
	if _, err := CreateCopy(agentDir, "alpha", store, pinOf(t, store)); err != nil {
		t.Fatal(err)
	}
	// Hand-placed directory sm must never delete.
	hand := filepath.Join(agentDir, "hand")
	if err := os.MkdirAll(hand, 0o755); err != nil {
		t.Fatal(err)
	}

	removed, leftovers, err := RemoveCopy(agentDir, "alpha", store)
	if err != nil || !removed || len(leftovers) != 0 {
		t.Fatalf("RemoveCopy(alpha) = %v, %v, %v; want true, no leftovers", removed, leftovers, err)
	}
	if _, err := os.Lstat(filepath.Join(agentDir, "alpha")); !os.IsNotExist(err) {
		t.Error("owned copy not removed")
	}

	removed, _, err = RemoveCopy(agentDir, "hand", store)
	if err != nil {
		t.Fatalf("RemoveCopy(hand): %v", err)
	}
	if removed {
		t.Error("RemoveCopy removed a hand-placed directory")
	}
	if _, err := os.Lstat(hand); err != nil {
		t.Error("hand-placed directory was deleted")
	}
}

func TestSweepStages_RemovesOnlyStageDirs(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	orphan := makeOrphanStage(t, agentDir)
	// An old-format name is not provably sm's, so it must survive.
	oldFormat := filepath.Join(agentDir, "alpha.sm-stage")
	if err := os.MkdirAll(oldFormat, 0o755); err != nil {
		t.Fatal(err)
	}
	// A real skill directory must be left alone.
	store := makeStoreSkill(t, root, "beta")
	if _, err := CreateCopy(agentDir, "beta", store, pinOf(t, store)); err != nil {
		t.Fatal(err)
	}

	swept, skipped, err := SweepStages(agentDir)
	if err != nil || swept != 1 || len(skipped) != 0 {
		t.Fatalf("SweepStages = %d, %v, %v; want 1, none skipped, nil", swept, skipped, err)
	}
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Error("stage dir not swept")
	}
	if _, err := os.Lstat(oldFormat); err != nil {
		t.Errorf("old-format stage name was removed: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(agentDir, "beta")); err != nil {
		t.Error("SweepStages removed a real copy")
	}

	// Missing dir is a no-op, not an error.
	if swept, skipped, err := SweepStages(filepath.Join(root, "nope")); err != nil || swept != 0 || len(skipped) != 0 {
		t.Errorf("SweepStages(missing) = %d, %v, %v; want 0, none, nil", swept, skipped, err)
	}
}
