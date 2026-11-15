package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/state"
)

// mapFixture sets up temp HOME/XDG dirs, a store (at the default
// $XDG_DATA_HOME/skills) with two conforming skills and one non-conforming
// entry, and returns the store dir and a (not-yet-existing) run target dir.
func mapFixture(t *testing.T) (storeDir, runDir string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	storeDir = filepath.Join(root, "data", "skills")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, storeDir, "rebuild")
	writeSkill(t, storeDir, "lint")
	if err := os.MkdirAll(filepath.Join(storeDir, "broken"), 0o755); err != nil {
		t.Fatal(err) // a dir with no SKILL.md -> store Unusable
	}
	runDir = filepath.Join(root, "run")
	return storeDir, runDir
}

func writeProfile(t *testing.T, path string, skills ...string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "skills = [\"" + strings.Join(skills, "\", \"") + "\"]\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mappedStateTarget(t *testing.T) (string, state.Target) {
	t.Helper()
	statePath := filepath.Join(os.Getenv("XDG_DATA_HOME"), "skills-mapper", "state.json")
	current, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for dir, target := range current.Targets {
		if target.Mapped {
			return dir, target
		}
	}
	return "", state.Target{}
}

func TestMapFromPlacesSubset(t *testing.T) {
	_, runDir := mapFixture(t)
	prof := filepath.Join(t.TempDir(), "run.toml")
	writeProfile(t, prof, "rebuild")

	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--from", prof, runDir}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !isSymlink(t, filepath.Join(runDir, "rebuild")) {
		t.Errorf("rebuild not linked into %s", runDir)
	}
	if _, err := os.Lstat(filepath.Join(runDir, "lint")); err == nil {
		t.Error("lint should not have been placed")
	}
	if !strings.Contains(out.String(), "map: +1 -0") {
		t.Errorf("stdout %q lacks 'map: +1 -0'", out.String())
	}
}

func TestMapAdhocPlaces(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !isSymlink(t, filepath.Join(runDir, "rebuild")) {
		t.Error("ad-hoc rebuild not placed")
	}
}

func TestMapNamedProfile(t *testing.T) {
	_, runDir := mapFixture(t)
	profDir := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "skills-mapper", "profiles")
	writeProfile(t, filepath.Join(profDir, "dev.toml"), "rebuild", "lint")

	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--profile", "dev", runDir}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	for _, name := range []string{"rebuild", "lint"} {
		if !isSymlink(t, filepath.Join(runDir, name)) {
			t.Errorf("%s not placed from named profile", name)
		}
	}
}

func TestMapFlagAfterTarget(t *testing.T) {
	_, runDir := mapFixture(t)
	prof := filepath.Join(t.TempDir(), "run.toml")
	writeProfile(t, prof, "rebuild")

	var out, errb bytes.Buffer
	code := Run([]string{"map", "--from", prof, runDir, "--dry-run"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "map: +1 -0") {
		t.Errorf("dry-run stdout %q lacks 'map: +1 -0'", out.String())
	}
	if _, err := os.Lstat(filepath.Join(runDir, "rebuild")); err == nil {
		t.Error("dry-run must place nothing")
	}
}

func TestMapUnknownPlacesNothing(t *testing.T) {
	_, runDir := mapFixture(t)
	prof := filepath.Join(t.TempDir(), "run.toml")
	writeProfile(t, prof, "ghost")

	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--from", prof, runDir}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for unknown skill")
	}
	if _, err := os.Lstat(filepath.Join(runDir, "ghost")); err == nil {
		t.Error("nothing should have been placed")
	}
	if !strings.Contains(errb.String(), "ghost") {
		t.Errorf("stderr %q should name the unknown skill", errb.String())
	}
}

func TestMapUsableButBrokenNamedFails(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "broken"}, &out, &errb); code == 0 {
		t.Fatal("expected non-zero exit for a non-conforming named skill")
	}
	if _, err := os.Lstat(filepath.Join(runDir, "broken")); err == nil {
		t.Error("nothing should have been placed")
	}
}

func TestMapReconcileShrinks(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild", "lint"}, &out, &errb); code != 0 {
		t.Fatalf("first map exit=%d stderr=%s", code, errb.String())
	}
	if !isSymlink(t, filepath.Join(runDir, "lint")) {
		t.Fatal("lint not placed on first map")
	}
	out.Reset()
	errb.Reset()
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("second map exit=%d stderr=%s", code, errb.String())
	}
	if _, err := os.Lstat(filepath.Join(runDir, "lint")); err == nil {
		t.Error("lint should have been reconciled away")
	}
	if !isSymlink(t, filepath.Join(runDir, "rebuild")) {
		t.Error("rebuild should remain")
	}
	if !strings.Contains(out.String(), "map: +0 -1") {
		t.Errorf("second map stdout %q lacks 'map: +0 -1'", out.String())
	}
}

func TestMapProtectsUnmanaged(t *testing.T) {
	_, runDir := mapFixture(t)
	real := filepath.Join(runDir, "rebuild")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	Run([]string{"map", runDir, "rebuild"}, &out, &errb)
	if isSymlink(t, real) {
		t.Error("sm map overwrote a real directory it did not create")
	}
	if !strings.Contains(errb.String(), "not managed by sm") {
		t.Errorf("stderr %q lacks the unmanaged-target warning", errb.String())
	}
}

func TestMapLiteralTarget(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !isSymlink(t, filepath.Join(runDir, "rebuild")) {
		t.Error("rebuild should be written directly into the target")
	}
	if _, err := os.Lstat(filepath.Join(runDir, ".agents", "skills", "rebuild")); err == nil {
		t.Error("target must be literal; no .agents/skills should be appended")
	}
}

func TestMapExclusiveSources(t *testing.T) {
	_, runDir := mapFixture(t)
	prof := filepath.Join(t.TempDir(), "run.toml")
	writeProfile(t, prof, "rebuild")
	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--from", prof, "--profile", "dev", runDir}, &out, &errb); code != 2 {
		t.Fatalf("expected usage exit 2 for two selection sources, got %d", code)
	}
}

func TestMapRequiresSelection(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir}, &out, &errb); code != 2 {
		t.Fatalf("expected usage exit 2 for no selection, got %d", code)
	}
}

func TestMapMarksTargetMapped(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	dir, target := mappedStateTarget(t)
	if dir == "" {
		t.Fatal("no state target marked Mapped")
	}
	if _, ok := target.Links["rebuild"]; !ok {
		t.Errorf("mapped target %s missing rebuild link: %v", dir, target.Links)
	}
}

func TestMapCopyMode(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--copy", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	placed := filepath.Join(runDir, "rebuild")
	if isSymlink(t, placed) {
		t.Error("--copy should materialize a real directory, not a symlink")
	}
	if _, err := os.Stat(filepath.Join(placed, "SKILL.md")); err != nil {
		t.Errorf("copied skill missing SKILL.md: %v", err)
	}
}

func TestMapProfileNameRejectsPath(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	code := Run([]string{"map", "--profile", "../escape", runDir}, &out, &errb)
	if code != exitError {
		t.Fatalf("exit=%d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "invalid profile name") {
		t.Errorf("stderr %q lacks 'invalid profile name'", errb.String())
	}
	if _, err := os.Lstat(runDir); err == nil {
		t.Error("a rejected profile name must place nothing")
	}
}

func TestMapWarnsOnDuplicateNames(t *testing.T) {
	_, runDir := mapFixture(t)
	prof := filepath.Join(t.TempDir(), "dup.toml")
	writeProfile(t, prof, "rebuild", "lint", "rebuild")

	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--from", prof, runDir}, &out, &errb); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(errb.String(), "sm map: warning: profile lists more than once: rebuild") {
		t.Errorf("stderr %q lacks the duplicate warning", errb.String())
	}
	if !strings.Contains(out.String(), "map: +2 -0") {
		t.Errorf("stdout %q lacks 'map: +2 -0'", out.String())
	}
}

func TestUnmapRemovesPlacedEntriesAndRecord(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild", "lint"}, &out, &errb); code != 0 {
		t.Fatalf("map exit=%d stderr=%s", code, errb.String())
	}
	handPlaced := filepath.Join(runDir, "notes.txt")
	if err := os.WriteFile(handPlaced, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	errb.Reset()
	if code := Run([]string{"unmap", runDir}, &out, &errb); code != 0 {
		t.Fatalf("unmap exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "unmap: -2") {
		t.Errorf("stdout %q lacks 'unmap: -2'", out.String())
	}
	for _, name := range []string{"rebuild", "lint"} {
		if _, err := os.Lstat(filepath.Join(runDir, name)); err == nil {
			t.Errorf("%s still present after unmap", name)
		}
	}
	if _, err := os.Stat(handPlaced); err != nil {
		t.Errorf("hand-placed file removed by unmap: %v", err)
	}
	if _, err := os.Stat(runDir); err != nil {
		t.Errorf("unmap removed the folder itself: %v", err)
	}
	statePath := filepath.Join(os.Getenv("XDG_DATA_HOME"), "skills-mapper", "state.json")
	after, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Targets) != 0 {
		t.Errorf("state still records targets after unmap: %v", after.Targets)
	}
}

func TestUnmapCopyMode(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", "--copy", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("map exit=%d stderr=%s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"unmap", runDir}, &out, &errb); code != 0 {
		t.Fatalf("unmap exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "unmap: -1") {
		t.Errorf("stdout %q lacks 'unmap: -1'", out.String())
	}
	if _, err := os.Lstat(filepath.Join(runDir, "rebuild")); err == nil {
		t.Error("copied skill still present after unmap")
	}
}

func TestUnmapDeletedFolderDropsRecord(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("map exit=%d stderr=%s", code, errb.String())
	}
	if err := os.RemoveAll(runDir); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"unmap", runDir}, &out, &errb); code != 0 {
		t.Fatalf("unmap exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "unmap: -0") {
		t.Errorf("stdout %q lacks 'unmap: -0'", out.String())
	}
	if dir, _ := mappedStateTargetOrEmpty(t); dir != "" {
		t.Errorf("record for %s survived unmap of a deleted folder", dir)
	}
}

func TestUnmapRefusesUnrecordedFolder(t *testing.T) {
	_, runDir := mapFixture(t)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	if code := Run([]string{"unmap", runDir}, &out, &errb); code != exitError {
		t.Fatalf("exit=%d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "not a mapped target") {
		t.Errorf("stderr %q lacks 'not a mapped target'", errb.String())
	}
}

// TestUnmapRefusesSyncTarget proves unmap cannot strip a directory sm sync
// manages: a recorded target that is not marked Mapped is refused untouched.
func TestUnmapRefusesSyncTarget(t *testing.T) {
	storeDir, runDir := mapFixture(t)
	statePath := filepath.Join(os.Getenv("XDG_DATA_HOME"), "skills-mapper", "state.json")
	agentDir := filepath.Join(filepath.Dir(runDir), "agent-skills")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	linkTarget := filepath.Join(storeDir, "rebuild")
	if err := os.Symlink(linkTarget, filepath.Join(agentDir, "rebuild")); err != nil {
		t.Fatal(err)
	}
	current, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	current.Targets[agentDir] = state.Target{Links: map[string]string{"rebuild": linkTarget}}
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := current.Save(statePath); err != nil {
		t.Fatal(err)
	}

	var out, errb bytes.Buffer
	if code := Run([]string{"unmap", agentDir}, &out, &errb); code != exitError {
		t.Fatalf("exit=%d, want %d", code, exitError)
	}
	if !strings.Contains(errb.String(), "use sm clean") {
		t.Errorf("stderr %q lacks 'use sm clean'", errb.String())
	}
	if !isSymlink(t, filepath.Join(agentDir, "rebuild")) {
		t.Error("unmap removed a sync-managed link")
	}
}

func TestUnmapUsage(t *testing.T) {
	mapFixture(t)
	for _, args := range [][]string{{"unmap"}, {"unmap", "a", "b"}, {"unmap", "--bogus", "a"}} {
		var out, errb bytes.Buffer
		if code := Run(args, &out, &errb); code != exitUsage {
			t.Errorf("%v: exit=%d, want %d", args, code, exitUsage)
		}
	}
}

func TestStatusHintsForMappedTargets(t *testing.T) {
	_, runDir := mapFixture(t)
	var out, errb bytes.Buffer
	if code := Run([]string{"init"}, &out, &errb); code != 0 {
		t.Fatalf("init exit=%d stderr=%s", code, errb.String())
	}
	if code := Run([]string{"map", runDir, "rebuild"}, &out, &errb); code != 0 {
		t.Fatalf("map exit=%d stderr=%s", code, errb.String())
	}
	out.Reset()
	if code := Run([]string{"status"}, &out, &errb); code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "undo with: sm unmap") {
		t.Errorf("status %q lacks the unmap hint", out.String())
	}
	if err := os.RemoveAll(runDir); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if code := Run([]string{"status"}, &out, &errb); code != 0 {
		t.Fatalf("status exit=%d stderr=%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "folder gone; drop the record with: sm clean --orphans") {
		t.Errorf("status %q lacks the folder-gone hint", out.String())
	}
}

func mappedStateTargetOrEmpty(t *testing.T) (string, state.Target) {
	t.Helper()
	statePath := filepath.Join(os.Getenv("XDG_DATA_HOME"), "skills-mapper", "state.json")
	current, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	for dir, target := range current.Targets {
		if target.Mapped {
			return dir, target
		}
	}
	return "", state.Target{}
}
