package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const (
	directoryPermissions = 0o755
	filePermissions      = 0o644
)

func writeSkill(t *testing.T, storeDir, directoryName, frontmatterName string) {
	t.Helper()
	skillDir := filepath.Join(storeDir, directoryName)
	if err := os.MkdirAll(skillDir, directoryPermissions); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + frontmatterName + "\ndescription: test\n---\n# body\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), filePermissions); err != nil {
		t.Fatal(err)
	}
}

func TestEnumerate_ConformingSkills(t *testing.T) {
	storeDir := t.TempDir()
	writeSkill(t, storeDir, "git-ops", "git-ops")
	writeSkill(t, storeDir, "clean-comments", "clean-comments")

	listing, err := Enumerate(storeDir)
	skills, warnings := listing.Skills, listing.Warnings
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	want := []Skill{
		{Name: "clean-comments", Dir: filepath.Join(storeDir, "clean-comments")},
		{Name: "git-ops", Dir: filepath.Join(storeDir, "git-ops")},
	}
	if len(skills) != len(want) {
		t.Fatalf("got %v, want %v", skills, want)
	}
	for index := range want {
		got := skills[index]
		if got.Name != want[index].Name || got.Dir != want[index].Dir {
			t.Fatalf("skill %d = %v, want %v", index, got, want[index])
		}
		// Identity pins a later copy to the validated directory.
		info, err := os.Stat(want[index].Dir)
		if err != nil {
			t.Fatal(err)
		}
		if got.Identity == nil || !os.SameFile(got.Identity, info) {
			t.Fatalf("skill %s identity %v does not describe %s", got.Name, got.Identity, want[index].Dir)
		}
	}
}

func TestRequireNotWorldWritable(t *testing.T) {
	for _, testCase := range []struct {
		mode    os.FileMode
		refused bool
	}{
		{0o755, false},
		{0o775, false},
		{0o777, true},
		{0o777 | os.ModeSticky, true},
	} {
		storeDir := t.TempDir()
		if err := os.Chmod(storeDir, testCase.mode); err != nil {
			t.Fatal(err)
		}
		err := RequireNotWorldWritable(storeDir)
		if testCase.refused {
			want := "store " + storeDir + " is world-writable; refusing"
			if err == nil || err.Error() != want {
				t.Errorf("mode %o: err = %v, want %q", testCase.mode, err, want)
			}
			continue
		}
		if err != nil {
			t.Errorf("mode %o: unexpected refusal: %v", testCase.mode, err)
		}
	}
	if err := RequireNotWorldWritable(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("a missing store must be an error")
	}
}

func TestEnumerate_NonConforming(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, storeDir string)
		wantWarning string
	}{
		{
			name: "MissingSkillMd",
			setup: func(t *testing.T, storeDir string) {
				if err := os.MkdirAll(filepath.Join(storeDir, "broken"), directoryPermissions); err != nil {
					t.Fatal(err)
				}
			},
			wantWarning: "broken",
		},
		{
			name: "NameMismatch",
			setup: func(t *testing.T, storeDir string) {
				writeSkill(t, storeDir, "foo", "bar")
			},
			wantWarning: "mismatch",
		},
		{
			name: "IllegalName",
			setup: func(t *testing.T, storeDir string) {
				writeSkill(t, storeDir, "Foo_Bar", "Foo_Bar")
			},
			wantWarning: "Foo_Bar",
		},
		{
			name: "NotNested",
			setup: func(t *testing.T, storeDir string) {
				writeSkill(t, storeDir, filepath.Join("parent", "child"), "child")
			},
			wantWarning: "parent",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			storeDir := t.TempDir()
			testCase.setup(t, storeDir)

			listing, err := Enumerate(storeDir)
			skills, warnings := listing.Skills, listing.Warnings
			if err != nil {
				t.Fatal(err)
			}
			if len(skills) != 0 {
				t.Fatalf("expected no skills, got %v", skills)
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0], testCase.wantWarning) {
				t.Fatalf("warnings %v do not mention %q exactly once", warnings, testCase.wantWarning)
			}
		})
	}
}

func TestEnumerate_IgnoresRootFiles(t *testing.T) {
	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(storeDir, "README"), []byte("x"), filePermissions); err != nil {
		t.Fatal(err)
	}
	listing, err := Enumerate(storeDir)
	skills, warnings := listing.Skills, listing.Warnings
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 || len(warnings) != 0 {
		t.Fatalf("got skills %v warnings %v", skills, warnings)
	}
}

func TestEnumerate_MissingStore(t *testing.T) {
	listing, err := Enumerate(filepath.Join(t.TempDir(), "absent"))
	skills := listing.Skills
	if err == nil {
		t.Fatal("expected error")
	}
	if skills != nil {
		t.Fatalf("expected nil skills, got %v", skills)
	}
}

func TestDefaultPath_XDGDefault(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/.local/share/skills") {
		t.Fatalf("got %q", path)
	}
}

func TestDefaultPath_XDGRelativeIgnored(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "relative/dir")
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(path, "/.local/share/skills") {
		t.Fatalf("got %q", path)
	}
}

func TestDefaultPath_XDGSet(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/data")
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if path != "/data/skills" {
		t.Fatalf("got %q", path)
	}
}

func TestEnumerate_IgnoresDotEntries(t *testing.T) {
	storeDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(storeDir, ".git"), directoryPermissions); err != nil {
		t.Fatal(err)
	}
	listing, err := Enumerate(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Warnings) != 0 || len(listing.Unusable) != 0 || len(listing.Skills) != 0 {
		t.Fatalf("dot entry must be ignored, got %+v", listing)
	}
}

func TestEnumerate_UnusableSetIncludesBrokenAndNonConforming(t *testing.T) {
	storeDir := t.TempDir()
	writeSkill(t, storeDir, "good", "good")
	if err := os.Symlink(filepath.Join(storeDir, "nowhere"), filepath.Join(storeDir, "dangling")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(storeDir, "nofile"), directoryPermissions); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, storeDir, "mismatch", "other")
	writeSkill(t, storeDir, "locked", "locked")
	lockedFile := filepath.Join(storeDir, "locked", "SKILL.md")
	if err := os.Chmod(lockedFile, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(lockedFile, filePermissions); err != nil {
			t.Error(err)
		}
	})

	listing, err := Enumerate(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	wantUnusable := []string{"dangling", "locked", "mismatch", "nofile"}
	if !reflect.DeepEqual(listing.Unusable, wantUnusable) {
		t.Fatalf("Unusable = %v, want %v", listing.Unusable, wantUnusable)
	}
	if len(listing.Skills) != 1 || listing.Skills[0].Name != "good" {
		t.Fatalf("Skills = %v, want only good", listing.Skills)
	}
	if len(listing.Warnings) != len(wantUnusable) {
		t.Fatalf("Warnings = %v, want one per unusable entry", listing.Warnings)
	}
}

func TestEnumerate_DanglingSymlinkWarns(t *testing.T) {
	storeDir := t.TempDir()
	if err := os.Symlink(filepath.Join(storeDir, "nowhere"), filepath.Join(storeDir, "dangling")); err != nil {
		t.Fatal(err)
	}
	listing, err := Enumerate(storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Warnings) != 1 || !strings.Contains(listing.Warnings[0], "dangling") {
		t.Fatalf("warnings %v do not mention dangling exactly once", listing.Warnings)
	}
}
