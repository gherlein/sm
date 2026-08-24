// SPDX-License-Identifier: MIT
// Package cache maintains durable local clones of git skill sources.
package cache

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/brightsign-playground/sm/internal/config"
)

func RepoDir(root, gitURL string) (string, error) {
	host, ownerRepo, err := parseGitURL(gitURL)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, host, ownerRepo), nil
}

func parseGitURL(u string) (host, ownerRepo string, err error) {
	s := strings.TrimSuffix(u, ".git")
	switch {
	case strings.HasPrefix(s, "git@"):
		parts := strings.SplitN(strings.TrimPrefix(s, "git@"), ":", 2)
		if len(parts) != 2 {
			return "", "", fmt.Errorf("bad ssh git url %q", u)
		}
		return parts[0], parts[1], nil
	case strings.Contains(s, "://"):
		rest := s[strings.Index(s, "://")+3:]
		i := strings.Index(rest, "/")
		if i < 0 {
			return "", "", fmt.Errorf("bad git url %q", u)
		}
		return rest[:i], rest[i+1:], nil
	default:
		return "", "", fmt.Errorf("unrecognized git url %q", u)
	}
}

func Update(src config.Source, dir string) error {
	if src.Path != "" {
		return nil
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return err
		}
		if err := git("", "clone", "--quiet", src.Git, dir); err != nil {
			return err
		}
	} else if err := git(dir, "fetch", "--quiet", "origin"); err != nil {
		return err
	}
	if src.Ref != "" {
		if err := git(dir, "checkout", "--quiet", src.Ref); err != nil {
			return err
		}
		_ = git(dir, "merge", "--quiet", "--ff-only", "origin/"+src.Ref) // ok to fail for tag/commit
	}
	return nil
}

func SourceRoot(src config.Source, cacheRoot string) (string, error) {
	if src.Path != "" {
		return expandHome(src.Path)
	}
	repo, err := RepoDir(cacheRoot, src.Git)
	if err != nil {
		return "", err
	}
	if src.Subdir != "" {
		return filepath.Join(repo, src.Subdir), nil
	}
	return repo, nil
}

// Prune removes repo dirs under root that are not in keep. keep holds absolute
// repo dirs (RepoDir outputs). Repo dirs can sit at any depth — GitLab
// subgroups and file:// remotes map deeper than host/owner/repo — so pruning
// follows the keep paths instead of assuming a fixed layout.
func Prune(root string, keep []string) error {
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[filepath.Clean(k)] = true
	}
	return pruneDir(filepath.Clean(root), keepSet)
}

// pruneDir removes every entry under dir that neither is a kept repo dir nor
// has one below it; ancestors of kept dirs are descended into, kept dirs are
// left untouched.
func pruneDir(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		switch {
		case keep[full]:
		case hasKeptDescendant(full, keep):
			if err := pruneDir(full, keep); err != nil {
				return err
			}
		default:
			if err := os.RemoveAll(full); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasKeptDescendant(dir string, keep map[string]bool) bool {
	prefix := dir + string(filepath.Separator)
	for kept := range keep {
		if strings.HasPrefix(kept, prefix) {
			return true
		}
	}
	return false
}

func DefaultRoot() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "skills-mapper", "repos"), nil
}

func expandHome(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	return p, nil
}

func git(dir string, args ...string) error {
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}
	return nil
}
