package link

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCreate_MakesSymlink(t *testing.T) {
	targetDir := t.TempDir()
	storePath := filepath.Join(t.TempDir(), "skill")
	if err := Create(targetDir, "skill", storePath); err != nil {
		t.Fatalf("Create: %v", err)
	}
	linkPath := filepath.Join(targetDir, "skill")
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("Lstat: %v", err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("expected symlink, got mode %v", info.Mode())
	}
	destination, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink: %v", err)
	}
	if destination != storePath {
		t.Errorf("Readlink = %q, want %q", destination, storePath)
	}
}

func TestCreate_MakesTargetDir(t *testing.T) {
	targetDir := filepath.Join(t.TempDir(), "nested", "skills")
	if err := Create(targetDir, "skill", "/store/skill"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "skill")); err != nil {
		t.Fatalf("Lstat: %v", err)
	}
}

func TestCreate_RejectsBadName(t *testing.T) {
	targetDir := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if err := Create(targetDir, name, "/store/x"); err == nil {
			t.Errorf("Create with name %q: expected error", name)
		}
	}
}

func TestProbe_SymlinkTarget(t *testing.T) {
	targetDir := t.TempDir()
	storePath := t.TempDir()
	if err := Create(targetDir, "skill", storePath); err != nil {
		t.Fatalf("Create: %v", err)
	}
	entry, err := Probe(targetDir, "skill")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := Entry{Exists: true, IsSymlink: true, Target: storePath}
	if entry != want {
		t.Errorf("Probe = %+v, want %+v", entry, want)
	}
}

// R4: a link whose store target vanished must still be seen, with the raw
// string it holds, so it can be identified and repaired.
func TestProbe_DanglingLinkReportsRawTarget(t *testing.T) {
	targetDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone", "skill")
	if err := os.Symlink(missing, filepath.Join(targetDir, "skill")); err != nil {
		t.Fatal(err)
	}
	entry, err := Probe(targetDir, "skill")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := Entry{Exists: true, IsSymlink: true, Target: missing}
	if entry != want {
		t.Errorf("Probe = %+v, want %+v", entry, want)
	}
}

// The target is the raw readlink string, never a resolved or cleaned path.
func TestProbe_RelativeTargetIsRaw(t *testing.T) {
	targetDir := t.TempDir()
	const relative = "../store/./skill"
	if err := os.Symlink(relative, filepath.Join(targetDir, "skill")); err != nil {
		t.Fatal(err)
	}
	entry, err := Probe(targetDir, "skill")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if entry.Target != relative {
		t.Errorf("Target = %q, want %q", entry.Target, relative)
	}
}

func TestProbe_RealDir(t *testing.T) {
	targetDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(targetDir, "skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := Probe(targetDir, "skill")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	want := Entry{Exists: true, IsDir: true}
	if entry != want {
		t.Errorf("Probe = %+v, want %+v", entry, want)
	}
}

// A symlink to a directory is reported as a symlink, not as a directory.
func TestProbe_SymlinkToDirIsNotDir(t *testing.T) {
	targetDir := t.TempDir()
	realDir := t.TempDir()
	if err := os.Symlink(realDir, filepath.Join(targetDir, "skill")); err != nil {
		t.Fatal(err)
	}
	entry, err := Probe(targetDir, "skill")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if entry.IsDir || !entry.IsSymlink {
		t.Errorf("Probe = %+v, want a symlink that is not a dir", entry)
	}
}

func TestProbe_Absent(t *testing.T) {
	entry, err := Probe(t.TempDir(), "missing")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if entry != (Entry{}) {
		t.Errorf("Probe = %+v, want zero Entry", entry)
	}
}

func TestProbe_RejectsBadName(t *testing.T) {
	targetDir := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := Probe(targetDir, name); err == nil {
			t.Errorf("Probe with name %q: expected error", name)
		}
	}
}

func TestRemoveIfTarget_RemovesMatchingSymlink(t *testing.T) {
	targetDir := t.TempDir()
	storePath := filepath.Join(t.TempDir(), "skill")
	if err := Create(targetDir, "skill", storePath); err != nil {
		t.Fatalf("Create: %v", err)
	}
	result, err := RemoveIfTarget(targetDir, "skill", storePath)
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != Removed {
		t.Errorf("result = %v, want Removed", result)
	}
	if _, err := os.Lstat(filepath.Join(targetDir, "skill")); !os.IsNotExist(err) {
		t.Errorf("link still present, Lstat err = %v", err)
	}
	assertOnlyEntries(t, targetDir)
}

// R3: a symlink the user put at a recorded name is theirs, not sm's.
func TestRemoveIfTarget_NotOursForUserReplacedSymlink(t *testing.T) {
	targetDir := t.TempDir()
	recorded := filepath.Join(t.TempDir(), "skill")
	usersTarget := t.TempDir()
	linkPath := filepath.Join(targetDir, "skill")
	if err := os.Symlink(usersTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveIfTarget(targetDir, "skill", recorded)
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != NotOurs {
		t.Errorf("result = %v, want NotOurs", result)
	}
	destination, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("user's link was removed: %v", err)
	}
	if destination != usersTarget {
		t.Errorf("user's link now points at %q, want %q", destination, usersTarget)
	}
	assertOnlyEntries(t, targetDir, "skill")
}

func TestRemoveIfTarget_NotOursForRealDirectory(t *testing.T) {
	targetDir := t.TempDir()
	realDir := filepath.Join(targetDir, "skill")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "SKILL.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveIfTarget(targetDir, "skill", "/store/skill")
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != NotOurs {
		t.Errorf("result = %v, want NotOurs", result)
	}
	content, err := os.ReadFile(filepath.Join(realDir, "SKILL.md"))
	if err != nil || string(content) != "mine" {
		t.Errorf("real directory disturbed: %q %v", content, err)
	}
	assertOnlyEntries(t, targetDir, "skill")
}

func TestRemoveIfTarget_NotOursForRegularFile(t *testing.T) {
	targetDir := t.TempDir()
	filePath := filepath.Join(targetDir, "skill")
	if err := os.WriteFile(filePath, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveIfTarget(targetDir, "skill", "/store/skill")
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != NotOurs {
		t.Errorf("result = %v, want NotOurs", result)
	}
	if _, err := os.Stat(filePath); err != nil {
		t.Errorf("file removed: %v", err)
	}
}

func TestRemoveIfTarget_Absent(t *testing.T) {
	targetDir := t.TempDir()
	result, err := RemoveIfTarget(targetDir, "missing", "/store/missing")
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != Absent {
		t.Errorf("result = %v, want Absent", result)
	}
	assertOnlyEntries(t, targetDir)
}

// A dangling link sm made is still identified by its raw target and removed.
func TestRemoveIfTarget_RemovesDanglingMatchingSymlink(t *testing.T) {
	targetDir := t.TempDir()
	missing := filepath.Join(t.TempDir(), "gone")
	if err := os.Symlink(missing, filepath.Join(targetDir, "skill")); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveIfTarget(targetDir, "skill", missing)
	if err != nil {
		t.Fatalf("RemoveIfTarget: %v", err)
	}
	if result != Removed {
		t.Errorf("result = %v, want Removed", result)
	}
}

func TestRemoveIfTarget_RejectsBadInput(t *testing.T) {
	targetDir := t.TempDir()
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := RemoveIfTarget(targetDir, name, "/store/x"); err == nil {
			t.Errorf("RemoveIfTarget with name %q: expected error", name)
		}
	}
	if _, err := RemoveIfTarget(targetDir, "skill", ""); err == nil {
		t.Error("RemoveIfTarget with empty wantTarget: expected error")
	}
}

// assertOnlyEntries fails unless dir holds exactly names, so a leaked
// .sm-old-* aside directory is caught.
func assertOnlyEntries(t *testing.T, dir string, names ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if len(got) != len(names) {
		t.Fatalf("entries in %s = %v, want %v", dir, got, names)
	}
	for index, name := range names {
		if got[index] != name {
			t.Fatalf("entries in %s = %v, want %v", dir, got, names)
		}
	}
}
