package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const bundledFileName = "helper.txt"

func sourceSkillFixture(t *testing.T, name string) SourceSkill {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "origin")
	writeSkill(t, directory, name)
	if err := os.WriteFile(filepath.Join(directory, bundledFileName), []byte("bundled"), 0o644); err != nil {
		t.Fatal(err)
	}
	return SourceSkill{Name: name, Dir: directory, Identity: identityOf(t, directory)}
}

func assertExists(t *testing.T, path string, want bool) {
	t.Helper()
	_, err := os.Lstat(path)
	if want && err != nil {
		t.Fatalf("expected %s to exist: %v", path, err)
	}
	if !want && err == nil {
		t.Fatalf("expected %s to be absent", path)
	}
}

func TestWriteSkill_New(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()

	result, err := WriteSkill(skill, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written || result.Skipped != "" {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, filepath.Join(destination, "alpha", "SKILL.md"), true)
	assertExists(t, filepath.Join(destination, "alpha", bundledFileName), true)
}

func TestWriteSkill_SkipsExisting(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written || result.Skipped == "" {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, sentinel, true)
	assertExists(t, filepath.Join(target, "SKILL.md"), false)
}

func TestWriteSkill_ForceReplaces(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, sentinel, false)
	assertExists(t, filepath.Join(target, "SKILL.md"), true)
	assertExists(t, filepath.Join(target, bundledFileName), true)
}

func TestWriteSkill_DryRun(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := filepath.Join(t.TempDir(), "store")

	result, err := WriteSkill(skill, destination, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written || result.Skipped != "" {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, destination, false)
}

func TestWriteSkill_DryRunExistingSkips(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, false, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written || result.Skipped == "" {
		t.Fatalf("unexpected result %+v", result)
	}
}

func TestWriteSkill_CreatesDestDir(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := filepath.Join(t.TempDir(), "nested", "store")

	result, err := WriteSkill(skill, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, filepath.Join(destination, "alpha", "SKILL.md"), true)
}

func TestWriteSkill_RejectsUnsafeName(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b", `a\b`, "../x"} {
		skill := SourceSkill{Name: name, Dir: t.TempDir()}
		if _, err := WriteSkill(skill, t.TempDir(), true, false); err == nil {
			t.Errorf("name %q: expected error", name)
		}
	}
}

func TestWriteSkill_ReportsSkippedEntries(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	absoluteLink := filepath.Join(skill.Dir, "abs-link")
	if err := os.Symlink(outside, absoluteLink); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()

	for _, force := range []bool{false, true} {
		result, err := WriteSkill(skill, destination, force, false)
		if err != nil {
			t.Fatalf("force=%v: %v", force, err)
		}
		if !result.Written {
			t.Fatalf("force=%v: unexpected result %+v", force, result)
		}
		if len(result.Report.Skipped) != 1 || !strings.Contains(result.Report.Skipped[0], absoluteLink) {
			t.Errorf("force=%v: Report.Skipped = %v, want one line naming %s", force, result.Report.Skipped, absoluteLink)
		}
		assertExists(t, filepath.Join(destination, "alpha", "abs-link"), false)
	}
}

func readFileText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertOverlapRefusal(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an overlap refusal, got nil")
	}
	if !strings.Contains(err.Error(), "refusing to write") || !strings.Contains(err.Error(), "overlaps source") {
		t.Fatalf("unexpected error: %v", err)
	}
	// ms skips exactly this error type per skill, so it must be the typed one.
	var overlap *OverlapError
	if !errors.As(err, &overlap) {
		t.Fatalf("error %v is not an *OverlapError", err)
	}
}

func TestWriteSkill_RefusesWhenSourceInsideTarget(t *testing.T) {
	destination := t.TempDir()
	sourceDir := filepath.Join(destination, "foo")
	writeSkill(t, sourceDir, "foo")
	notes := filepath.Join(sourceDir, "notes.txt")
	if err := os.WriteFile(notes, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := WriteSkill(SourceSkill{Name: "foo", Dir: sourceDir}, destination, true, false)
	assertOverlapRefusal(t, err)
	if got := readFileText(t, notes); got != "precious" {
		t.Fatalf("source content changed: %q", got)
	}
	assertExists(t, filepath.Join(sourceDir, "SKILL.md"), true)
}

func TestWriteSkill_RefusesWhenTargetInsideSource(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "origin")
	writeSkill(t, sourceDir, "foo")
	destination := filepath.Join(sourceDir, "out")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		t.Fatal(err)
	}

	_, err := WriteSkill(SourceSkill{Name: "foo", Dir: sourceDir}, destination, true, false)
	assertOverlapRefusal(t, err)
	assertExists(t, filepath.Join(destination, "foo"), false)
}

func TestWriteSkill_RefusesAncestorTarget(t *testing.T) {
	destination := t.TempDir()
	sourceDir := filepath.Join(destination, "x", "y", "src")
	writeSkill(t, sourceDir, "x")

	_, err := WriteSkill(SourceSkill{Name: "x", Dir: sourceDir}, destination, true, false)
	assertOverlapRefusal(t, err)
	assertExists(t, filepath.Join(sourceDir, "SKILL.md"), true)
}

func TestWriteSkill_OverlapRefusedUnderDryRun(t *testing.T) {
	destination := t.TempDir()
	sourceDir := filepath.Join(destination, "foo")
	writeSkill(t, sourceDir, "foo")

	for _, force := range []bool{false, true} {
		_, err := WriteSkill(SourceSkill{Name: "foo", Dir: sourceDir}, destination, force, true)
		assertOverlapRefusal(t, err)
	}
	assertExists(t, filepath.Join(sourceDir, "SKILL.md"), true)
}

func TestWriteSkill_ForceKeepsOldSkillWhenCopyFails(t *testing.T) {
	requireNonRoot(t)
	skill := sourceSkillFixture(t, "alpha")
	unreadable := filepath.Join(skill.Dir, "secret.txt")
	if err := os.WriteFile(unreadable, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSkill(skill, destination, true, false); err == nil {
		t.Fatal("expected the copy to fail")
	}
	if got := readFileText(t, sentinel); got != "old" {
		t.Fatalf("old skill content changed: %q", got)
	}
	assertExists(t, filepath.Join(target, "SKILL.md"), false)
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected only the old skill in %s, found %d entries", destination, len(entries))
	}
}

func TestWriteSkill_ForceSwapsContent(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, bundledFileName), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written || len(result.Report.Leftovers) != 0 {
		t.Fatalf("unexpected result %+v", result)
	}
	if got := readFileText(t, filepath.Join(target, bundledFileName)); got != "bundled" {
		t.Fatalf("content not swapped: %q", got)
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("swap left extra entries in %s: %d", destination, len(entries))
	}
}

func TestWriteSkill_DryRunForceExistingDoesNotRemove(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	target := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written || result.Skipped != "" {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, sentinel, true)
	assertExists(t, filepath.Join(target, "SKILL.md"), false)
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("dry run left extra entries: %d", len(entries))
	}
}

func TestWriteSkill_RefusesWorldWritableDest(t *testing.T) {
	requireNonRoot(t)
	skill := sourceSkillFixture(t, "alpha")
	destination := t.TempDir()
	if err := os.Chmod(destination, 0o777); err != nil {
		t.Fatal(err)
	}

	_, err := WriteSkill(skill, destination, false, false)
	if err == nil || !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("expected a world-writable refusal, got %v", err)
	}
	assertExists(t, filepath.Join(destination, "alpha"), false)
}

func TestWriteSkill_DoesNotCarryRootMarker(t *testing.T) {
	skill := sourceSkillFixture(t, "alpha")
	if err := os.WriteFile(filepath.Join(skill.Dir, ".sm-copy"), []byte("/some/store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()

	result, err := WriteSkill(skill, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	assertExists(t, filepath.Join(destination, "alpha", ".sm-copy"), false)
	if len(result.Report.Skipped) != 1 {
		t.Fatalf("expected the marker to be reported as skipped, got %v", result.Report.Skipped)
	}
}

func writeTextFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertRealDirectory(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a real directory, mode %v", path, info.Mode())
	}
}

func TestWriteSkill_ForceDoesNotTouchSymlinkReferent(t *testing.T) {
	skill := sourceSkillFixture(t, "foo")
	destination := t.TempDir()
	referent := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(referent, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTextFile(t, filepath.Join(referent, "notes.txt"), "PRECIOUS")
	entry := filepath.Join(destination, "foo")
	if err := os.Symlink(referent, entry); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written {
		t.Fatalf("unexpected result %+v", result)
	}
	if got := readFileText(t, filepath.Join(referent, "notes.txt")); got != "PRECIOUS" {
		t.Fatalf("referent content changed: %q", got)
	}
	assertExists(t, filepath.Join(referent, "SKILL.md"), false)
	assertRealDirectory(t, entry)
	assertExists(t, filepath.Join(entry, "SKILL.md"), true)
}

func TestWriteSkill_ForceDoesNotOverwriteAnotherSkillThroughSymlink(t *testing.T) {
	skill := sourceSkillFixture(t, "foo")
	destination := t.TempDir()
	other := filepath.Join(destination, "bar")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTextFile(t, filepath.Join(other, "SKILL.md"), "bar skill")
	if err := os.Symlink(other, filepath.Join(destination, "foo")); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSkill(skill, destination, true, false); err != nil {
		t.Fatal(err)
	}
	if got := readFileText(t, filepath.Join(other, "SKILL.md")); got != "bar skill" {
		t.Fatalf("another skill was overwritten: %q", got)
	}
	assertExists(t, filepath.Join(other, bundledFileName), false)
	assertRealDirectory(t, filepath.Join(destination, "foo"))
}

func TestWriteSkill_ExistingSymlinkEntrySkippedWithoutForce(t *testing.T) {
	skill := sourceSkillFixture(t, "foo")
	destination := t.TempDir()
	referent := filepath.Join(t.TempDir(), "unrelated")
	if err := os.MkdirAll(referent, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTextFile(t, filepath.Join(referent, "notes.txt"), "PRECIOUS")
	entry := filepath.Join(destination, "foo")
	if err := os.Symlink(referent, entry); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Written || !strings.Contains(result.Skipped, entry) {
		t.Fatalf("expected a skip naming %s, got %+v", entry, result)
	}
	if strings.Contains(result.Skipped, referent) {
		t.Fatalf("skip reason names the referent: %q", result.Skipped)
	}
	linkTarget, err := os.Readlink(entry)
	if err != nil || linkTarget != referent {
		t.Fatalf("entry changed: %q, %v", linkTarget, err)
	}
	if got := readFileText(t, filepath.Join(referent, "notes.txt")); got != "PRECIOUS" {
		t.Fatalf("referent content changed: %q", got)
	}
}

func TestWriteSkill_DanglingSymlinkEntryIsReplacedNotFollowed(t *testing.T) {
	skill := sourceSkillFixture(t, "foo")
	destination := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	entry := filepath.Join(destination, "foo")
	if err := os.Symlink(missing, entry); err != nil {
		t.Fatal(err)
	}

	result, err := WriteSkill(skill, destination, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Written {
		t.Fatalf("unexpected result %+v", result)
	}
	assertExists(t, missing, false)
	assertRealDirectory(t, entry)
	assertExists(t, filepath.Join(entry, "SKILL.md"), true)
}

func TestWriteSkill_RefusesWhenDestDirIsSymlinkIntoSourceParent(t *testing.T) {
	root := t.TempDir()
	realParent := filepath.Join(root, "real")
	sourceDir := filepath.Join(realParent, "foo")
	writeSkill(t, sourceDir, "foo")
	writeTextFile(t, filepath.Join(sourceDir, "notes.txt"), "precious")
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Fatal(err)
	}

	for _, dryRun := range []bool{false, true} {
		_, err := WriteSkill(SourceSkill{Name: "foo", Dir: sourceDir}, alias, true, dryRun)
		assertOverlapRefusal(t, err)
	}
	if got := readFileText(t, filepath.Join(sourceDir, "notes.txt")); got != "precious" {
		t.Fatalf("source content changed: %q", got)
	}
	assertExists(t, filepath.Join(sourceDir, "SKILL.md"), true)
}

func TestWriteSkill_RefusesWhenSourceReachedThroughAlias(t *testing.T) {
	destination := t.TempDir()
	sourceDir := filepath.Join(destination, "foo")
	writeSkill(t, sourceDir, "foo")
	writeTextFile(t, filepath.Join(sourceDir, "notes.txt"), "precious")
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(sourceDir, alias); err != nil {
		t.Fatal(err)
	}

	_, err := WriteSkill(SourceSkill{Name: "foo", Dir: alias}, destination, true, false)
	assertOverlapRefusal(t, err)
	if got := readFileText(t, filepath.Join(sourceDir, "notes.txt")); got != "precious" {
		t.Fatalf("source content changed: %q", got)
	}
	assertExists(t, filepath.Join(sourceDir, "SKILL.md"), true)
}

func TestWriteSkill_RefusesWhenDestEntryIsSymlinkToSource(t *testing.T) {
	for _, mode := range []struct {
		name          string
		force, dryRun bool
	}{
		{"plain", false, false},
		{"force", true, false},
		{"dry-run", false, true},
		{"force dry-run", true, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			skill := sourceSkillFixture(t, "foo")
			destination := t.TempDir()
			entry := filepath.Join(destination, "foo")
			if err := os.Symlink(skill.Dir, entry); err != nil {
				t.Fatal(err)
			}
			skillBefore := readFileText(t, filepath.Join(skill.Dir, "SKILL.md"))

			_, err := WriteSkill(skill, destination, mode.force, mode.dryRun)
			assertOverlapRefusal(t, err)

			if got := readFileText(t, filepath.Join(skill.Dir, "SKILL.md")); got != skillBefore {
				t.Errorf("source SKILL.md changed: %q", got)
			}
			if got := readFileText(t, filepath.Join(skill.Dir, bundledFileName)); got != "bundled" {
				t.Errorf("source bundled file changed: %q", got)
			}
			target, err := os.Readlink(entry)
			if err != nil || target != skill.Dir {
				t.Errorf("dest entry no longer the original symlink: target %q, err %v", target, err)
			}
		})
	}
}
