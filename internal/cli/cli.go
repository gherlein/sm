// Package cli wires the units into sm's commands.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/gherlein/skills-mapper/internal/agents"
	"github.com/gherlein/skills-mapper/internal/cache"
	"github.com/gherlein/skills-mapper/internal/config"
	"github.com/gherlein/skills-mapper/internal/discovery"
	"github.com/gherlein/skills-mapper/internal/link"
	"github.com/gherlein/skills-mapper/internal/resolve"
	"github.com/gherlein/skills-mapper/internal/state"
)

type Env struct {
	Scope, Root, ManifestPath, CacheRoot, StatePath string
}

// Run resolves scope + Env, then dispatches. A leading/anywhere --global or
// --project token forces scope; otherwise scope is auto (project if a project
// skills.toml is found walking up from cwd, else global).
func Run(args []string, stdout, stderr io.Writer) int {
	scope, rest := extractScope(args)
	env, err := buildEnv(scope)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return run(env, rest, stdout, stderr)
}

func extractScope(args []string) (string, []string) {
	scope := ""
	var rest []string
	for _, a := range args {
		switch a {
		case "--global":
			scope = "global"
		case "--project":
			scope = "project"
		default:
			rest = append(rest, a)
		}
	}
	return scope, rest
}

func buildEnv(scope string) (Env, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Env{}, err
	}
	cr, err := cache.DefaultRoot()
	if err != nil {
		return Env{}, err
	}
	sp, err := state.DefaultPath()
	if err != nil {
		return Env{}, err
	}
	cwd, _ := os.Getwd()
	if scope == "" { // auto
		if _, ok := config.ProjectManifestPath(cwd, home); ok {
			scope = "project"
		} else {
			scope = "global"
		}
	}
	env := Env{Scope: scope, CacheRoot: cr, StatePath: sp}
	if scope == "project" {
		mp, ok := config.ProjectManifestPath(cwd, home)
		if !ok {
			return Env{}, fmt.Errorf("no project skills.toml found above %s", cwd)
		}
		env.ManifestPath = mp
		env.Root = filepath.Dir(mp)
	} else {
		mp, err := config.DefaultPath()
		if err != nil {
			return Env{}, err
		}
		env.ManifestPath = mp
		env.Root = home
	}
	return env, nil
}

func run(env Env, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sm [--global|--project] <init|sync|update|link|add|remove|list> ...")
		return 2
	}
	switch args[0] {
	case "init":
		return cmdInit(env, stdout, stderr)
	case "sync":
		return cmdSync(env, args[1:], stdout, stderr)
	case "update":
		return cmdUpdate(env, stdout, stderr)
	case "link":
		return cmdLink(env, args[1:], stdout, stderr)
	case "add":
		return cmdAdd(env, args[1:], stdout, stderr)
	case "remove":
		return cmdRemove(env, args[1:], stdout, stderr)
	case "list":
		return cmdList(env, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		return 2
	}
}

// freshen updates all git caches and prunes caches not in the manifest.
func freshen(env Env, m *config.Manifest) error {
	var keep []string
	for _, src := range m.Skills {
		if src.Git == "" {
			continue
		}
		dir, err := cache.RepoDir(env.CacheRoot, src.Git)
		if err != nil {
			return err
		}
		if err := cache.Update(src, dir); err != nil {
			return err
		}
		keep = append(keep, dir)
	}
	return cache.Prune(env.CacheRoot, keep)
}

// place discovers, resolves, and reconciles links into enabled agents.
func place(env Env, m *config.Manifest, dryRun bool, stderr io.Writer) (int, int, error) {
	enabled, err := agents.Enabled(m)
	if err != nil {
		return 0, 0, err
	}
	if len(enabled) == 0 {
		return 0, 0, fmt.Errorf("no agents enabled in [agents]")
	}
	var skills []discovery.Skill
	for alias, src := range m.Skills {
		root, err := cache.SourceRoot(src, env.CacheRoot)
		if err != nil {
			return 0, 0, fmt.Errorf("source %q: %w", alias, err)
		}
		found, err := discovery.Discover(root, alias)
		if err != nil {
			return 0, 0, fmt.Errorf("discover %q: %w", alias, err)
		}
		skills = append(skills, found...)
	}
	plan, err := resolve.Resolve(skills, m.Options)
	if err != nil {
		return 0, 0, err
	}
	st, err := state.Load(env.StatePath)
	if err != nil {
		return 0, 0, err
	}
	installed, removed := 0, 0
	for _, id := range enabled {
		target, err := agents.TargetDir(id, env.Scope, env.Root)
		if err != nil {
			return 0, 0, err
		}
		prev := st.Targets[target].Links
		recon := state.Plan(prev, plan.Links)
		installed += len(recon.Add)
		removed += len(recon.Remove)
		if dryRun {
			continue
		}
		newLinks, conflicts, err := link.Apply(target, recon.Add, recon.Remove, prev)
		if err != nil {
			return 0, 0, err
		}
		for _, c := range conflicts {
			fmt.Fprintf(stderr, "warning: %s/%s exists and was not created by sm; skipped\n", target, c)
		}
		st.Targets[target] = state.TargetLinks{Links: newLinks, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	}
	if !dryRun {
		if err := st.Save(env.StatePath); err != nil {
			return 0, 0, err
		}
	}
	return installed, removed, nil
}

func cmdSync(env Env, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "plan without changing files")
	if fs.Parse(args) != nil {
		return 2
	}
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !*dry {
		if err := freshen(env, m); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	ins, rem, err := place(env, m, *dry, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s sync: +%d -%d\n", env.Scope, ins, rem)
	return 0
}

func cmdInit(env Env, stdout, stderr io.Writer) int {
	if _, err := os.Stat(env.ManifestPath); err == nil {
		fmt.Fprintf(stderr, "manifest already exists: %s\n", env.ManifestPath)
		return 1
	}
	starter := "[skills]\n# core = { git = \"https://github.com/you/skills\", ref = \"main\" }\n\n[agents]\nclaude-code = true\ncopilot = true\nhax = false\npi = false\noh-my-pi = false\n\n[options]\nprefix_on_collision = false\n"
	if err := os.MkdirAll(filepath.Dir(env.ManifestPath), 0o755); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := os.WriteFile(env.ManifestPath, []byte(starter), 0o644); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "wrote %s\n", env.ManifestPath)
	return 0
}

func cmdList(env Env, stdout, stderr io.Writer) int {
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	for alias, src := range m.Skills {
		where := src.Git
		if where == "" {
			where = src.Path
		}
		fmt.Fprintf(stdout, "%s\t%s\t%s\n", alias, where, src.Ref)
	}
	return 0
}

// TODO(task-10): replace with the real add/remove implementation.
func cmdAdd(env Env, args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "not implemented")
	return 2
}

// TODO(task-10): replace with the real add/remove implementation.
func cmdRemove(env Env, args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "not implemented")
	return 2
}

// TODO(task-11): replace with the real update/link implementation.
func cmdUpdate(env Env, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "not implemented")
	return 2
}

// TODO(task-11): replace with the real update/link implementation.
func cmdLink(env Env, args []string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stderr, "not implemented")
	return 2
}
