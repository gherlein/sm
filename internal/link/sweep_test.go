package link

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSweepStages_RemovesOnlyStageDirectories(t *testing.T) {
	targetDir := t.TempDir()
	orphan := makeOrphanStage(t, targetDir)
	// The pre-hardening name is no longer swept: it is not provably sm's.
	oldFormat := filepath.Join(targetDir, "alpha.sm-stage")
	if err := os.MkdirAll(oldFormat, 0o755); err != nil {
		t.Fatal(err)
	}
	realSkill := filepath.Join(targetDir, "beta")
	if err := os.MkdirAll(realSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, "SKILL.md"), []byte("keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, skipped, err := SweepStages(targetDir)
	if err != nil {
		t.Fatalf("SweepStages: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped stages: %v", skipped)
	}
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Errorf("orphan stage still present (err=%v)", err)
	}
	if _, err := os.Lstat(oldFormat); err != nil {
		t.Errorf("old-format stage name was removed: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(realSkill, "SKILL.md"))
	if err != nil || string(got) != "keep\n" {
		t.Errorf("beta damaged: %q, %v", got, err)
	}
}

func TestSweepStages_NoStagesChangesNothing(t *testing.T) {
	targetDir := t.TempDir()
	realSkill := filepath.Join(targetDir, "beta")
	if err := os.MkdirAll(realSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	removed, skipped, err := SweepStages(targetDir)
	if err != nil {
		t.Fatalf("SweepStages: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped stages: %v", skipped)
	}
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "beta" {
		t.Errorf("target dir changed: %v", entries)
	}
}

func TestSweepStages_MissingTargetDir(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	removed, skipped, err := SweepStages(missing)
	if err != nil {
		t.Fatalf("missing dir should be tolerated, got error: %v", err)
	}
	if removed != 0 {
		t.Errorf("removed = %d, want 0", removed)
	}
	if len(skipped) != 0 {
		t.Errorf("unexpected skipped stages: %v", skipped)
	}
	if _, statErr := os.Stat(missing); !os.IsNotExist(statErr) {
		t.Errorf("sweep must not create the dir (err=%v)", statErr)
	}
}
