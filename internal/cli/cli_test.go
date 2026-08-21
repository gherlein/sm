package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitRepoWithSkill(t *testing.T) string {
	t.Helper()
	origin := t.TempDir()
	sk := filepath.Join(origin, "skills", "git-workflow")
	os.MkdirAll(sk, 0o755)
	os.WriteFile(filepath.Join(sk, "SKILL.md"), []byte("---\nname: git-workflow\ndescription: d\n---\n"), 0o644)
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "i"}} {
		c := exec.Command("git", args...)
		c.Dir = origin
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return origin
}

func TestSyncGlobalEndToEnd(t *testing.T) {
	origin := gitRepoWithSkill(t)
	home := t.TempDir()
	mp := filepath.Join(home, "skills.toml")
	os.WriteFile(mp, []byte("[skills]\ncore = { git = \"file://"+origin+"\", ref = \"main\" }\n[agents]\nclaude-code = true\nhax = true\n"), 0o644)
	env := Env{Scope: "global", Root: home, ManifestPath: mp, CacheRoot: filepath.Join(home, "cache"), StatePath: filepath.Join(home, "state.json")}
	var out, errOut bytes.Buffer
	if code := run(env, []string{"sync"}, &out, &errOut); code != 0 {
		t.Fatalf("sync exit %d: %s", code, errOut.String())
	}
	for _, target := range []string{".claude/skills", ".config/hax/skills"} {
		l := filepath.Join(home, target, "git-workflow")
		if fi, err := os.Lstat(l); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("expected symlink %s: %v", l, err)
		}
	}
	// removing the source and re-syncing prunes the links
	os.WriteFile(mp, []byte("[agents]\nclaude-code = true\n"), 0o644)
	if code := run(env, []string{"sync"}, &out, &errOut); code != 0 {
		t.Fatalf("resync exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills", "git-workflow")); !os.IsNotExist(err) {
		t.Fatal("link should be pruned after source removed")
	}
}
