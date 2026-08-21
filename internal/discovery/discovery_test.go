package discovery

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func writeSkill(t *testing.T, dir, fm string) {
	t.Helper()
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(fm), 0o644)
}

func TestDiscoverFlattensAndSkipsBad(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "skills", "git-workflow"), "---\nname: git-workflow\ndescription: d\n---\n")
	writeSkill(t, filepath.Join(root, "docker"), "---\nname: docker\n---\n")
	writeSkill(t, filepath.Join(root, ".git", "x"), "---\nname: nope\n---\n")     // ignored dir
	writeSkill(t, filepath.Join(root, "bad"), "---\ndescription: no name\n---\n") // skipped (no name)

	got, err := Discover(root, "core")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, s := range got {
		names = append(names, s.Name)
		if s.Alias != "core" {
			t.Errorf("alias unset: %+v", s)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "docker" || names[1] != "git-workflow" {
		t.Fatalf("got %v", names)
	}
}
