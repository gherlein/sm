package cache

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/brightsign-playground/sm/internal/config"
)

func TestRepoDir(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/core":     "/c/github.com/acme/core",
		"https://github.com/acme/core.git": "/c/github.com/acme/core",
		"git@github.com:bob/handy.git":     "/c/github.com/bob/handy",
	} {
		if got, err := RepoDir("/c", url); err != nil || got != want {
			t.Errorf("RepoDir(%q)=%q,%v want %q", url, got, err, want)
		}
	}
}

func gitInit(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "."}, {"commit", "-q", "-m", "i"}} {
		if args[0] == "add" {
			os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: x\n---\n"), 0o644)
		}
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

func TestUpdateCloneThenFetch(t *testing.T) {
	origin := t.TempDir()
	gitInit(t, origin)
	dst := filepath.Join(t.TempDir(), "clone")
	src := config.Source{Git: "file://" + origin, Ref: "main"}
	if err := Update(src, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatal("expected clone")
	}
	if err := Update(src, dst); err != nil { // fetch path
		t.Fatal(err)
	}
}

func TestSourceRootAndPrune(t *testing.T) {
	p := t.TempDir()
	if r, _ := SourceRoot(config.Source{Path: p}, "/c"); r != p {
		t.Fatalf("path root wrong: %q", r)
	}
	if r, _ := SourceRoot(config.Source{Git: "https://github.com/a/b", Subdir: "s"}, "/c"); r != "/c/github.com/a/b/s" {
		t.Fatalf("subdir root wrong: %q", r)
	}
	root := t.TempDir()
	keep := filepath.Join(root, "github.com", "a", "keep")
	drop := filepath.Join(root, "github.com", "a", "drop")
	os.MkdirAll(keep, 0o755)
	os.MkdirAll(drop, 0o755)
	if err := Prune(root, []string{keep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(drop); !os.IsNotExist(err) {
		t.Fatal("drop should be pruned")
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("keep should survive")
	}
}

// Repo dirs deeper than host/owner/repo (GitLab subgroups, file:// remotes)
// must survive a prune that keeps them.
func TestPruneKeepsDeepRepoDirs(t *testing.T) {
	root := t.TempDir()
	deepKeep := filepath.Join(root, "gitlab.com", "group", "subgroup", "team", "keep")
	deepDrop := filepath.Join(root, "gitlab.com", "group", "subgroup", "team", "drop")
	shallowDrop := filepath.Join(root, "gitlab.com", "group", "other")
	for _, d := range []string{deepKeep, deepDrop, shallowDrop} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(deepKeep, "SKILL.md"), []byte("x"), 0o644)
	if err := Prune(root, []string{deepKeep}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(deepKeep, "SKILL.md")); err != nil {
		t.Fatalf("deep keep should survive with contents: %v", err)
	}
	for _, d := range []string{deepDrop, shallowDrop} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Fatalf("%s should be pruned", d)
		}
	}
}
