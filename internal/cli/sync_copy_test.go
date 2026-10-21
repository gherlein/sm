package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/state"
)

const copyMarkerName = ".sm-copy"

func TestDoSync_CopyMaterializesRealDirectories(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err != nil {
		t.Fatalf("doSync copy: %v", err)
	}
	if !strings.Contains(summary, "+2 ~0 -0") {
		t.Errorf("summary %q lacks +2 ~0 -0", summary)
	}
	for _, name := range []string{"alpha", "beta"} {
		entry := filepath.Join(fixture.agentDir, name)
		if isSymlink(t, entry) {
			t.Errorf("%s is a symlink, want a real copy", name)
		}
		if _, err := os.Stat(filepath.Join(entry, "SKILL.md")); err != nil {
			t.Errorf("%s SKILL.md: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(entry, copyMarkerName)); err != nil {
			t.Errorf("%s missing provenance marker: %v", name, err)
		}
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	target := saved.Targets[fixture.agentDir]
	if len(target.Copies) != 2 {
		t.Errorf("state copies = %v", target.Copies)
	}
	if len(target.Links) != 0 {
		t.Errorf("state links should be empty, got %v", target.Links)
	}
}

func TestDoSync_CopyRefreshesOnStoreChange(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	// Change the store skill, then re-sync: the copy must track the edit.
	newBody := "---\nname: alpha\ndescription: test skill\n---\nUPDATED\n"
	if err := os.WriteFile(filepath.Join(fixture.storeDir, "alpha", "SKILL.md"), []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(fixture.agentDir, "alpha", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "UPDATED") {
		t.Errorf("copy was not refreshed: %q", got)
	}
}

func TestDoSync_CopyReconcilesRemoved(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "+0 ~1 -1") {
		t.Errorf("summary %q lacks +0 ~1 -1", summary)
	}
	if _, err := os.Stat(filepath.Join(fixture.agentDir, "beta")); !os.IsNotExist(err) {
		t.Error("removed-from-store copy was not reconciled away")
	}
	if _, err := os.Stat(filepath.Join(fixture.agentDir, "alpha", "SKILL.md")); err != nil {
		t.Errorf("alpha copy should remain: %v", err)
	}
}

func TestDoSync_CopyLeavesHandPlacedDirectory(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	// Hand-place a real directory at alpha with sentinel content and no marker.
	handAlpha := filepath.Join(fixture.agentDir, "alpha")
	if err := os.MkdirAll(handAlpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(handAlpha, "MINE"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "not managed by sm") {
		t.Errorf("expected unmanaged warning, log: %q", log.String())
	}
	// alpha untouched: sentinel present, no marker added.
	if _, err := os.Stat(filepath.Join(handAlpha, "MINE")); err != nil {
		t.Errorf("hand-placed content lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(handAlpha, copyMarkerName)); !os.IsNotExist(err) {
		t.Error("sm wrote a marker into a hand-placed directory")
	}
	// beta still copied.
	if _, err := os.Stat(filepath.Join(fixture.agentDir, "beta", copyMarkerName)); err != nil {
		t.Errorf("beta copy missing: %v", err)
	}
	saved, _ := state.Load(fixture.statePath)
	if _, recorded := saved.Targets[fixture.agentDir].Copies["alpha"]; recorded {
		t.Error("sm recorded an unmanaged entry as its own copy")
	}
}

func TestDoClean_RemovesCopies(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	if _, err := doClean(fixture.cfg, fixture.statePath, "", false, &log); err != nil {
		t.Fatalf("doClean: %v", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if _, err := os.Stat(filepath.Join(fixture.agentDir, name)); !os.IsNotExist(err) {
			t.Errorf("%s copy not removed by clean", name)
		}
	}
	saved, _ := state.Load(fixture.statePath)
	if _, present := saved.Targets[fixture.agentDir]; present {
		t.Error("clean left a state entry for the agent")
	}
}

func TestDoSync_CopyDryRunReportsRefreshes(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	// A dry-run with both copies already present should report two refreshes and
	// change nothing on disk.
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true, DryRun: true}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(summary, "+0 ~2 -0") {
		t.Errorf("dry-run summary %q lacks +0 ~2 -0", summary)
	}
}

func TestDoSync_CopyWarnsOnModeSwitch(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	// First create a symlink entry, then attempt a copy sync into the same dir.
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "managed by sm in symlink mode") {
		t.Errorf("expected a mode-switch warning, got: %q", log.String())
	}
	// The symlink is left intact (not clobbered) and no copy was recorded.
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("existing symlink was disturbed by a copy sync")
	}
}

// When the refreshed copy is in place but the previous one cannot be deleted,
// the ledger must record the NEW store path (which the on-disk marker now
// names); recording the old one would orphan the copy on the next run.
func TestDoSync_CopyRefreshRecordsNewSourceWhenAsideCleanupFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user: root can delete a read-only directory, so the cleanup failure could not be provoked")
	}
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	newStore := filepath.Join(filepath.Dir(fixture.storeDir), "store-b")
	writeSkill(t, newStore, "alpha")

	agentRoot := filepath.Dir(fixture.agentDir)
	locked := filepath.Join(fixture.agentDir, "alpha", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "pinned.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		err := filepath.WalkDir(agentRoot, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o755)
			}
			return nil
		})
		if err != nil {
			t.Error(err)
		}
	})

	log.Reset()
	summary, err := doSync(fixture.cfg, newStore, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err != nil {
		t.Fatalf("doSync must succeed when only the old copy's cleanup failed: %v", err)
	}
	if !strings.Contains(summary, "~1") {
		t.Errorf("summary %q lacks ~1", summary)
	}
	if !strings.Contains(log.String(), "warning") || !strings.Contains(log.String(), ".sm-old-") {
		t.Errorf("leftover not reported as a warning: %q", log.String())
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(newStore, "alpha")
	if got := saved.Targets[fixture.agentDir].Copies["alpha"]; got != want {
		t.Errorf("ledger records %q, want the new source %q", got, want)
	}
	owned, err := link.IsOwnedCopy(fixture.agentDir, "alpha", want)
	if err != nil || !owned {
		t.Errorf("copy not owned for the recorded source: %v, %v", owned, err)
	}
}

// A refresh that fails part way must leave the previous copy, and its record,
// exactly as they were.
func TestDoSync_CopyRefreshKeepsOldWhenCopyFails(t *testing.T) {
	requireNonRoot(t, "root can read a mode-0 file, so the refresh would not fail")
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(fixture.agentDir, "alpha")
	before, err := os.ReadFile(filepath.Join(copyDir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	unreadable := filepath.Join(fixture.storeDir, "alpha", "secret.txt")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	newBody := "---\nname: alpha\ndescription: test skill\n---\nUPDATED\n"
	if err := os.WriteFile(filepath.Join(fixture.storeDir, "alpha", "SKILL.md"), []byte(newBody), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err == nil {
		t.Fatal("expected the refresh to fail on an unreadable source file")
	}
	// The failure is isolated to the skill: the run still reports its summary.
	if !strings.Contains(err.Error(), "agent fake: alpha:") {
		t.Errorf("error %q does not name the agent and skill", err)
	}
	if summary != "sync: fake +0 ~0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 ~0 -0")
	}
	after, err := os.ReadFile(filepath.Join(copyDir, "SKILL.md"))
	if err != nil {
		t.Fatalf("old copy lost: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("old copy changed: %q -> %q", before, after)
	}
	if _, err := os.Lstat(filepath.Join(copyDir, "secret.txt")); !os.IsNotExist(err) {
		t.Errorf("partial new copy is visible: %v", err)
	}
	recordedSource := filepath.Join(fixture.storeDir, "alpha")
	owned, err := link.IsOwnedCopy(fixture.agentDir, "alpha", recordedSource)
	if err != nil || !owned {
		t.Errorf("old copy no longer owned: %v, %v", owned, err)
	}
	if got := loadState(t, fixture.statePath).Targets[fixture.agentDir].Copies["alpha"]; got != recordedSource {
		t.Errorf("state records %q, want %q", got, recordedSource)
	}
	entries := listNames(t, fixture.agentDir)
	if strings.Join(entries, ",") != "alpha" {
		t.Errorf("agent dir holds %v, want only alpha (no stage or aside left behind)", entries)
	}
}

// R7: a store skill that is itself a symlink is copied as its real tree, and
// nothing (in particular the marker) is ever written through the link.
func TestDoSync_CopySymlinkedStoreSkillIsMaterializedAsRealDir(t *testing.T) {
	fixture := newSyncFixture(t)
	realSkills := t.TempDir()
	writeSkill(t, realSkills, "alpha")
	storeEntry := filepath.Join(fixture.storeDir, "alpha")
	if err := os.Symlink(filepath.Join(realSkills, "alpha"), storeEntry); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +1 ~0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +1 ~0 -0")
	}
	copyDir := filepath.Join(fixture.agentDir, "alpha")
	info, err := os.Lstat(copyDir)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is not a real directory (mode %v)", copyDir, info.Mode())
	}
	if _, err := os.Stat(filepath.Join(copyDir, "SKILL.md")); err != nil {
		t.Errorf("copied SKILL.md missing: %v", err)
	}
	owned, err := link.IsOwnedCopy(fixture.agentDir, "alpha", storeEntry)
	if err != nil || !owned {
		t.Errorf("copy not owned for the store path: %v, %v", owned, err)
	}
	if _, err := os.Lstat(filepath.Join(realSkills, "alpha", copyMarkerName)); !os.IsNotExist(err) {
		t.Errorf("a marker was written into the real source directory: %v", err)
	}
}

// Entries a copy deliberately leaves out must be named, so a skill that lost a
// file is never mistaken for a faithful copy.
func TestDoSync_CopyReportsSkippedEntries(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	escaping := filepath.Join(fixture.storeDir, "alpha", "outside")
	if err := os.Symlink("/etc/hostname", escaping); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer

	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if !strings.Contains(log.String(), "warning: fake: skipped ") || !strings.Contains(log.String(), filepath.Join("alpha", "outside")) {
		t.Errorf("skipped entry not reported: %q", log.String())
	}
	if _, err := os.Lstat(filepath.Join(fixture.agentDir, "alpha", "outside")); !os.IsNotExist(err) {
		t.Errorf("absolute symlink was copied: %v", err)
	}
}

// A desired name whose recorded copy the user replaced is both Lost and
// Unmanaged in the plan; the user must see one message for it, not two.
func TestDoSync_CopyLostAndUnmanagedWarnsOnce(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(fixture.agentDir, "alpha")
	if err := os.RemoveAll(copyDir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(copyDir, "MINE"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if count := strings.Count(log.String(), "warning:"); count != 1 {
		t.Errorf("got %d warnings, want exactly 1: %q", count, log.String())
	}
	if !strings.Contains(log.String(), "no longer an sm copy; leaving it in place") {
		t.Errorf("missing lost warning: %q", log.String())
	}
	if _, err := os.Stat(filepath.Join(copyDir, "MINE")); err != nil {
		t.Errorf("user's directory changed: %v", err)
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Copies["alpha"]; recorded {
		t.Error("record for the user-replaced copy was not dropped")
	}
}
