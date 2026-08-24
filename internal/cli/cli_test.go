package cli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brightsign-playground/sm/internal/config"
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

func TestExtractFlags(t *testing.T) {
	scope, verbose, rest := extractFlags([]string{"--global", "-v", "sync", "--dry-run"})
	if scope != "global" || !verbose {
		t.Fatalf("scope=%q verbose=%v, want global,true", scope, verbose)
	}
	if len(rest) != 2 || rest[0] != "sync" || rest[1] != "--dry-run" {
		t.Fatalf("rest=%v, want [sync --dry-run]", rest)
	}
	if _, v, _ := extractFlags([]string{"--verbose", "list"}); !v {
		t.Fatal("--verbose not recognized")
	}
	if _, v, _ := extractFlags([]string{"list"}); v {
		t.Fatal("verbose should default off")
	}
}

func TestSyncVerbose(t *testing.T) {
	origin := gitRepoWithSkill(t)
	newEnv := func() (Env, string) {
		home := t.TempDir()
		mp := filepath.Join(home, "skills.toml")
		os.WriteFile(mp, []byte("[skills]\ncore = { git = \"file://"+origin+"\", ref = \"main\" }\n[agents]\nclaude-code = true\n"), 0o644)
		return Env{Scope: "global", Root: home, ManifestPath: mp, CacheRoot: filepath.Join(home, "cache"), StatePath: filepath.Join(home, "state.json")}, home
	}

	verboseEnv, _ := newEnv()
	verboseEnv.Verbose = true
	var out, errOut bytes.Buffer
	if code := run(verboseEnv, []string{"sync"}, &out, &errOut); code != 0 {
		t.Fatalf("verbose sync exit %d: %s", code, errOut.String())
	}
	for _, want := range []string{"discovered", "link git-workflow", "target claude-code"} {
		if !strings.Contains(errOut.String(), want) {
			t.Fatalf("verbose output missing %q; got:\n%s", want, errOut.String())
		}
	}

	quietEnv, _ := newEnv()
	var qOut, qErr bytes.Buffer
	if code := run(quietEnv, []string{"sync"}, &qOut, &qErr); code != 0 {
		t.Fatalf("quiet sync exit %d: %s", code, qErr.String())
	}
	if qErr.Len() != 0 {
		t.Fatalf("default sync should emit nothing on stderr; got:\n%s", qErr.String())
	}
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

func TestAddThenRemove(t *testing.T) {
	origin := gitRepoWithSkill(t)
	home := t.TempDir()
	mp := filepath.Join(home, "skills.toml")
	os.WriteFile(mp, []byte("[agents]\nclaude-code = true\n"), 0o644)
	env := Env{Scope: "global", Root: home, ManifestPath: mp, CacheRoot: filepath.Join(home, "cache"), StatePath: filepath.Join(home, "state.json")}
	var out, errOut bytes.Buffer

	if code := run(env, []string{"add", "file://" + origin, "--as", "core", "--ref", "main", "--sync"}, &out, &errOut); code != 0 {
		t.Fatalf("add exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills", "git-workflow")); err != nil {
		t.Fatalf("add --sync should link: %v", err)
	}
	m, _ := config.Load(mp)
	if m.Skills["core"].Git != "file://"+origin {
		t.Fatalf("manifest not updated: %+v", m.Skills)
	}
	if code := run(env, []string{"remove", "core", "--sync"}, &out, &errOut); code != 0 {
		t.Fatalf("remove exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills", "git-workflow")); !os.IsNotExist(err) {
		t.Fatal("remove --sync should prune links")
	}
}
