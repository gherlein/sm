// Package cli wires the units into sm's commands.
package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/brightsign-playground/sm/internal/agents"
	"github.com/brightsign-playground/sm/internal/cache"
	"github.com/brightsign-playground/sm/internal/config"
	"github.com/brightsign-playground/sm/internal/discovery"
	"github.com/brightsign-playground/sm/internal/link"
	"github.com/brightsign-playground/sm/internal/resolve"
	"github.com/brightsign-playground/sm/internal/state"
)

type Env struct {
	Scope, Root, ManifestPath, CacheRoot, StatePath string
	Verbose                                         bool
}

// Run resolves scope + Env, then dispatches. A leading/anywhere --global or
// --project token forces scope; otherwise scope is auto (project if a project
// skills.toml is found walking up from cwd, else global). -v/--verbose, allowed
// anywhere, makes sync report each step on stderr.
func Run(args []string, stdout, stderr io.Writer) int {
	scope, verbose, rest := extractFlags(args)
	env, err := buildEnv(scope)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	env.Verbose = verbose
	return run(env, rest, stdout, stderr)
}

func extractFlags(args []string) (scope string, verbose bool, rest []string) {
	for _, a := range args {
		switch a {
		case "--global":
			scope = "global"
		case "--project":
			scope = "project"
		case "-v", "--verbose":
			verbose = true
		default:
			rest = append(rest, a)
		}
	}
	return scope, verbose, rest
}

// vlog returns the writer verbose lines go to: stderr when verbose, else a sink.
func (e Env) vlog(stderr io.Writer) io.Writer {
	if e.Verbose {
		return stderr
	}
	return io.Discard
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
		fmt.Fprintln(stderr, "usage: sm [--global|--project] [-v|--verbose] <init|sync|update|link|add|remove|list> ...")
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
func freshen(env Env, m *config.Manifest, stderr io.Writer) error {
	v := env.vlog(stderr)
	var keep []string
	for _, alias := range sortedAliases(m.Skills) {
		src := m.Skills[alias]
		if src.Git == "" {
			continue
		}
		dir, err := cache.RepoDir(env.CacheRoot, src.Git)
		if err != nil {
			return err
		}
		ref := src.Ref
		if ref == "" {
			ref = "default branch"
		}
		fmt.Fprintf(v, "updating cache: %s  %s (ref %s) -> %s\n", alias, src.Git, ref, dir)
		if err := cache.Update(src, dir); err != nil {
			return err
		}
		keep = append(keep, dir)
	}
	fmt.Fprintf(v, "pruning caches not in manifest\n")
	return cache.Prune(env.CacheRoot, keep)
}

func sortedAliases(m map[string]config.Source) []string {
	out := make([]string, 0, len(m))
	for a := range m {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
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
	v := env.vlog(stderr)
	var skills []discovery.Skill
	for _, alias := range sortedAliases(m.Skills) {
		src := m.Skills[alias]
		root, err := cache.SourceRoot(src, env.CacheRoot)
		if err != nil {
			return 0, 0, fmt.Errorf("source %q: %w", alias, err)
		}
		found, err := discovery.Discover(root, alias)
		if err != nil {
			return 0, 0, fmt.Errorf("discover %q: %w", alias, err)
		}
		fmt.Fprintf(v, "discovered %d skills from %s\n", len(found), alias)
		skills = append(skills, found...)
	}
	plan, err := resolve.Resolve(skills, m.Options)
	if err != nil {
		return 0, 0, err
	}
	fmt.Fprintf(v, "resolved %d links (prefix_on_collision=%t)\n", len(plan.Links), m.Options.PrefixOnCollision)
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
		fmt.Fprintf(v, "target %s: %s\n", id, target)
		for _, l := range recon.Add {
			fmt.Fprintf(v, "  link %s -> %s\n", l.Name, l.SourceDir)
		}
		for _, name := range recon.Remove {
			fmt.Fprintf(v, "  remove stale %s\n", name)
		}
		if dryRun {
			continue
		}
		newLinks, conflicts, err := link.Apply(target, recon.Add, recon.Remove, prev)
		for _, c := range conflicts {
			fmt.Fprintf(stderr, "warning: %s/%s exists and was not created by sm; skipped\n", target, c)
		}
		if err != nil {
			// link.Apply returns its partially-updated map on error so real progress
			// on earlier agents isn't lost; persist it best-effort before reporting err.
			if newLinks != nil {
				st.Targets[target] = state.TargetLinks{Links: newLinks, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
			}
			_ = st.Save(env.StatePath)
			return installed, removed, err
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
		if err := freshen(env, m, stderr); err != nil {
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

func cmdAdd(env Env, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	as := fs.String("as", "", "alias (defaults to repo/dir name)")
	ref := fs.String("ref", "", "git ref (branch|tag|commit)")
	subdir := fs.String("subdir", "", "subdirectory within the source")
	sync := fs.Bool("sync", false, "sync after adding")
	target, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprintln(stderr, "usage: sm add <git-url|path> [--as alias] [--ref r] [--subdir d] [--sync]")
		return 2
	}
	src := config.Source{Ref: *ref, Subdir: *subdir}
	if looksLikeGit(target) {
		src.Git = target
	} else {
		src.Path = target
	}
	alias := *as
	if alias == "" {
		alias = deriveAlias(target)
	}
	m, err := loadOrEmpty(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := m.AddSource(alias, src); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := config.Save(env.ManifestPath, m); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "added %s\n", alias)
	if *sync {
		if err := freshen(env, m, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if _, _, err := place(env, m, false, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	return 0
}

func cmdRemove(env Env, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	sync := fs.Bool("sync", false, "sync after removing")
	keepCache := fs.Bool("keep-cache", false, "do not prune the cached repo")
	alias, ok := parseWithPositional(fs, args)
	if !ok {
		fmt.Fprintln(stderr, "usage: sm remove <alias> [--sync] [--keep-cache]")
		return 2
	}
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !m.RemoveSource(alias) {
		fmt.Fprintf(stderr, "no such source %q\n", alias)
		return 1
	}
	if err := config.Save(env.ManifestPath, m); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if *sync {
		if !*keepCache {
			// freshen prunes the now-unreferenced cache
			if err := freshen(env, m, stderr); err != nil {
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
		}
		// reconcile prunes the stale links
		if _, _, err := place(env, m, false, stderr); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "removed %s\n", alias)
	return 0
}

// parseWithPositional parses a flag set whose command takes exactly one
// positional argument that may appear before, between, or after flags —
// stdlib flag alone stops at the first non-flag token.
func parseWithPositional(fs *flag.FlagSet, args []string) (string, bool) {
	if fs.Parse(args) != nil {
		return "", false
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", false
	}
	positional := rest[0]
	if fs.Parse(rest[1:]) != nil {
		return "", false
	}
	return positional, fs.NArg() == 0
}

func looksLikeGit(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "file://") || strings.HasPrefix(s, "ssh://")
}

func deriveAlias(target string) string {
	return filepath.Base(strings.TrimSuffix(target, ".git"))
}

func loadOrEmpty(path string) (*config.Manifest, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &config.Manifest{Skills: map[string]config.Source{}, Agents: map[string]bool{}}, nil
	}
	return config.Load(path)
}

func cmdUpdate(env Env, stdout, stderr io.Writer) int {
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := freshen(env, m, stderr); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "updated %d source(s)\n", len(m.Skills))
	return 0
}

func cmdLink(env Env, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("link", flag.ContinueOnError)
	dry := fs.Bool("dry-run", false, "plan without changing files")
	if fs.Parse(args) != nil {
		return 2
	}
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	ins, rem, err := place(env, m, *dry, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	fmt.Fprintf(stdout, "%s link: +%d -%d\n", env.Scope, ins, rem)
	return 0
}
