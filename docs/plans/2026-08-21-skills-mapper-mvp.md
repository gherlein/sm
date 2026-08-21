# skills-mapper (`sm`) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `sm`: read a `skills.toml` manifest, keep git skill-repos fresh in a durable cache, and symlink their `SKILL.md` skills into five agents (claude-code, copilot, hax, pi, oh-my-pi) at global or project scope — safely reconciled — via `init`/`sync`/`update`/`link`/`add`/`remove`/`list`.

**Architecture:** A small Go CLI. Pure cores (`config`, `discovery`, `resolve`, `state`) hold logic and are unit-tested in isolation; thin I/O units (`cache` for git, `link` for symlinks) get hermetic integration tests; `cli` wires them. Placement is discover → resolve → link → reconcile; freshness (`update`) and placement (`link`) are separable halves that `sync` runs together.

**Tech Stack:** Go (stdlib `flag`), `github.com/BurntSushi/toml`, `gopkg.in/yaml.v3`, `os/exec` for git.

**Spec:** `docs/DESIGN.md` (and `VISION.md`) in this repo.

## Global Constraints

- Go **1.23+**; module `github.com/gherlein/skills-mapper`. Deps limited to `github.com/BurntSushi/toml` and `gopkg.in/yaml.v3`. Binary **`sm`** (from `cmd/sm`).
- Sources: `git` (URL + optional `ref`, `subdir`) and local `path` only. **No** Claude-plugin/registry/auth.
- **Agent registry** (id → global dir / project dir, relative to home or repo root):

  | id | global | project |
  | --- | --- | --- |
  | `claude-code` | `.claude/skills` | `.claude/skills` |
  | `copilot` | `.copilot/skills` | `.github/skills` |
  | `hax` | `.config/hax/skills` | `.agents/skills` |
  | `pi` | `.pi-go/skills` | `.pi/skills` |
  | `oh-my-pi` | `.config/agents/skills` | `.agents/skills` |

- **Scope**: global (manifest `~/.config/skills-mapper/skills.toml`, install under `$HOME`) or project (nearest `skills.toml` walking up from cwd, stop at `$HOME`, install under repo root). Auto = project if a project manifest is found, else global; `--global`/`--project` force.
- Cache: `<XDG_DATA_HOME|~/.local/share>/skills-mapper/repos/<host>/<owner>/<repo>`. State: `<XDG_DATA_HOME|~/.local/share>/skills-mapper/state.json`, keyed by absolute target dir (both scopes coexist).
- Collision policy: clean unprefixed names; **duplicate skill name across sources fails loud**; opt-in `prefix_on_collision`. Placement = **whole-directory symlinks**; only ever remove links `sm` recorded; never touch unmanaged targets.
- Run `gofmt`; comments explain WHY not WHAT.

## File Structure

```
skills-mapper/
  go.mod  Makefile  README.md
  cmd/sm/main.go
  internal/config/     config.go      config_test.go      # types, Parse/Load/Save, Add/RemoveSource, project discovery
  internal/discovery/  discovery.go   discovery_test.go   # Skill, Discover, frontmatter
  internal/resolve/    resolve.go     resolve_test.go     # Link/Plan, Resolve, CollisionError
  internal/agents/     agents.go      agents_test.go      # scope-aware registry, TargetDir, Enabled
  internal/state/      state.go       state_test.go       # State, Load/Save, Reconcile Plan
  internal/cache/      cache.go       cache_test.go       # RepoDir, Update, SourceRoot, Prune, DefaultRoot
  internal/link/       link.go        link_test.go        # Apply (safe symlinks)
  internal/cli/        cli.go         cli_test.go         # Env, Run, run, freshen/place, all commands
```

---

### Task 1: Scaffold + config parsing (read)

**Files:** Create `go.mod`, `Makefile`, `internal/config/config.go`; Test `internal/config/config_test.go`

**Interfaces — Produces:**
- `type Source struct { Git, Ref, Subdir, Path string }`
- `type Options struct { PrefixOnCollision bool }`
- `type Manifest struct { Skills map[string]Source; Agents map[string]bool; Options Options }`
- `func Parse(data []byte) (*Manifest, error)`, `func Load(path string) (*Manifest, error)`, `func DefaultPath() (string, error)`

- [ ] **Step 1: Initialize module + Makefile**

```bash
cd ~/src/brightsign-playground/skills-mapper
go mod init github.com/gherlein/skills-mapper
go get github.com/BurntSushi/toml@latest gopkg.in/yaml.v3@latest
```

`Makefile`:
```makefile
BIN := sm
build:
	go build -o $(BIN) ./cmd/sm
test:
	go test ./...
lint:
	gofmt -l . && go vet ./...
.PHONY: build test lint
```

- [ ] **Step 2: Write the failing test**

`internal/config/config_test.go`:
```go
package config

import "testing"

func TestParseValid(t *testing.T) {
	m, err := Parse([]byte(`
[skills]
core = { git = "https://github.com/acme/core", ref = "main" }
mine = { path = "~/dev/skills" }
[agents]
claude-code = true
copilot = false
[options]
prefix_on_collision = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Skills["core"].Git != "https://github.com/acme/core" || m.Skills["core"].Ref != "main" {
		t.Errorf("core wrong: %+v", m.Skills["core"])
	}
	if m.Skills["mine"].Path != "~/dev/skills" {
		t.Errorf("mine wrong: %+v", m.Skills["mine"])
	}
	if !m.Agents["claude-code"] || m.Agents["copilot"] || !m.Options.PrefixOnCollision {
		t.Errorf("agents/options wrong: %+v %+v", m.Agents, m.Options)
	}
}

func TestParseRejectsBothOrNeither(t *testing.T) {
	if _, err := Parse([]byte("[skills]\nb = { git=\"x\", path=\"y\" }\n")); err == nil {
		t.Error("expected error for both git and path")
	}
	if _, err := Parse([]byte("[skills]\nb = { ref=\"main\" }\n")); err == nil {
		t.Error("expected error for neither git nor path")
	}
}
```

- [ ] **Step 3: Run test to verify it fails** — `go test ./internal/config/ -v` → FAIL (undefined).

- [ ] **Step 4: Write minimal implementation**

`internal/config/config.go`:
```go
// Package config parses, validates, and writes the skills-mapper manifest.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/BurntSushi/toml"
)

type Source struct {
	Git    string `toml:"git,omitempty"`
	Ref    string `toml:"ref,omitempty"`
	Subdir string `toml:"subdir,omitempty"`
	Path   string `toml:"path,omitempty"`
}

type Options struct {
	PrefixOnCollision bool `toml:"prefix_on_collision"`
}

type Manifest struct {
	Skills  map[string]Source `toml:"skills"`
	Agents  map[string]bool   `toml:"agents"`
	Options Options           `toml:"options"`
}

// Parse decodes and validates: each source has exactly one of git or path.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := toml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse skills.toml: %w", err)
	}
	if m.Skills == nil {
		m.Skills = map[string]Source{}
	}
	if m.Agents == nil {
		m.Agents = map[string]bool{}
	}
	for _, alias := range sortedKeys(m.Skills) {
		s := m.Skills[alias]
		if (s.Git != "") == (s.Path != "") {
			return nil, fmt.Errorf("source %q: specify exactly one of git or path", alias)
		}
	}
	return &m, nil
}

func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "skills-mapper", "skills.toml"), nil
}

func sortedKeys(m map[string]Source) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
```

- [ ] **Step 5: Run tests** — `go test ./internal/config/ -v` → PASS.
- [ ] **Step 6: Commit** — `git add go.mod go.sum Makefile internal/config/ && git commit -m "feat(config): parse and validate skills.toml"`

---

### Task 2: Config write + project-manifest discovery

**Files:** Modify `internal/config/config.go`; add tests to `internal/config/config_test.go`

**Interfaces — Produces:**
- `func Save(path string, m *Manifest) error`
- `func (m *Manifest) AddSource(alias string, s Source) error` (error if alias exists or invalid)
- `func (m *Manifest) RemoveSource(alias string) bool`
- `func ProjectManifestPath(startDir, home string) (string, bool)` (walk up for `skills.toml`, stop at `home`)

- [ ] **Step 1: Write the failing test** (append to `config_test.go`):
```go
import (
	"os"
	"path/filepath"
)

func TestAddRemoveAndSaveRoundTrip(t *testing.T) {
	m := &Manifest{Skills: map[string]Source{}, Agents: map[string]bool{"claude-code": true}}
	if err := m.AddSource("core", Source{Git: "https://github.com/a/b", Ref: "main"}); err != nil {
		t.Fatal(err)
	}
	if err := m.AddSource("core", Source{Path: "/x"}); err == nil {
		t.Fatal("expected duplicate alias error")
	}
	p := filepath.Join(t.TempDir(), "skills.toml")
	if err := Save(p, m); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil || got.Skills["core"].Git != "https://github.com/a/b" {
		t.Fatalf("round trip lost data: %+v %v", got, err)
	}
	if !m.RemoveSource("core") || m.RemoveSource("core") {
		t.Fatal("RemoveSource semantics wrong")
	}
}

func TestProjectManifestPathWalksUp(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "proj")
	sub := filepath.Join(repo, "a", "b")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(repo, "skills.toml"), []byte("[agents]\n"), 0o644)
	got, ok := ProjectManifestPath(sub, home)
	if !ok || got != filepath.Join(repo, "skills.toml") {
		t.Fatalf("walk-up failed: %q %v", got, ok)
	}
	if _, ok := ProjectManifestPath(home, home); ok {
		t.Fatal("should not find a manifest at/above home")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/config/ -run 'AddRemove|ProjectManifest' -v` → FAIL.

- [ ] **Step 3: Write minimal implementation** (append to `config.go`; add `"strings"` to imports):
```go
var invalidAliasChars = "/\\.:"

func (m *Manifest) AddSource(alias string, s Source) error {
	if alias == "" || strings.ContainsAny(alias, invalidAliasChars) {
		return fmt.Errorf("invalid alias %q (no / \\ . :)", alias)
	}
	if _, ok := m.Skills[alias]; ok {
		return fmt.Errorf("source %q already exists", alias)
	}
	if (s.Git != "") == (s.Path != "") {
		return fmt.Errorf("source %q: specify exactly one of git or path", alias)
	}
	if m.Skills == nil {
		m.Skills = map[string]Source{}
	}
	m.Skills[alias] = s
	return nil
}

func (m *Manifest) RemoveSource(alias string) bool {
	if _, ok := m.Skills[alias]; !ok {
		return false
	}
	delete(m.Skills, alias)
	return true
}

func Save(path string, m *Manifest) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(m)
}

// ProjectManifestPath returns the nearest skills.toml at or below the repo,
// walking up from startDir and stopping before home (home itself excluded).
func ProjectManifestPath(startDir, home string) (string, bool) {
	dir := startDir
	for {
		if dir == home || dir == filepath.Dir(dir) {
			return "", false
		}
		p := filepath.Join(dir, "skills.toml")
		if _, err := os.Stat(p); err == nil {
			return p, true
		}
		dir = filepath.Dir(dir)
	}
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/config/ -v` → PASS.
- [ ] **Step 5: Commit** — `git add internal/config/ && git commit -m "feat(config): manifest write + project discovery"`

---

### Task 3: Frontmatter + skill discovery

*(unchanged core — a skill is any dir with a SKILL.md, flattening all layouts.)*

**Files:** Create `internal/discovery/discovery.go`; Test `internal/discovery/discovery_test.go`

**Interfaces — Produces:** `type Skill struct { Alias, Name, SourceDir, Description string }`; `func Discover(root, alias string) ([]Skill, error)`

- [ ] **Step 1: Write the failing test**
```go
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
	writeSkill(t, filepath.Join(root, ".git", "x"), "---\nname: nope\n---\n")   // ignored dir
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
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/discovery/ -v` → FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/discovery/discovery.go`:
```go
// Package discovery finds skills (dirs with a SKILL.md) in a source tree.
package discovery

import (
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Skill struct {
	Alias, Name, SourceDir, Description string
}

var ignored = map[string]bool{
	".git": true, "node_modules": true, ".next": true, ".turbo": true,
	"vendor": true, "dist": true, "build": true, "__pycache__": true,
}

func Discover(root, alias string) ([]Skill, error) {
	var out []Skill
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && ignored[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "SKILL.md" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, desc := parseFrontmatter(data)
		if name == "" {
			return nil // not standard-compliant; skip
		}
		dir := filepath.Dir(path)
		out = append(out, Skill{Alias: alias, Name: filepath.Base(dir), SourceDir: dir, Description: desc})
		return nil
	})
	return out, err
}

func parseFrontmatter(b []byte) (name, description string) {
	s := string(b)
	if !strings.HasPrefix(s, "---\n") {
		return "", ""
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", ""
	}
	var fm struct {
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
	}
	if yaml.Unmarshal([]byte(rest[:end]), &fm) != nil {
		return "", ""
	}
	return fm.Name, fm.Description
}
```

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `git add internal/discovery/ && git commit -m "feat(discovery): find SKILL.md skills across layouts"`

---

### Task 4: Collision resolution → install plan

**Files:** Create `internal/resolve/resolve.go`; Test `internal/resolve/resolve_test.go`

**Interfaces — Consumes:** `discovery.Skill`, `config.Options`. **Produces:** `type Link struct { Name, SourceDir, Alias string }`; `type Plan struct { Links []Link }`; `type CollisionError struct { Name string; Aliases []string }`; `func Resolve([]discovery.Skill, config.Options) (*Plan, error)`

- [ ] **Step 1: Write the failing test**
```go
package resolve

import (
	"errors"
	"testing"

	"github.com/gherlein/skills-mapper/internal/config"
	"github.com/gherlein/skills-mapper/internal/discovery"
)

func TestResolveCleanAndCollision(t *testing.T) {
	ok := []discovery.Skill{{Alias: "core", Name: "a", SourceDir: "/c/a"}}
	if p, err := Resolve(ok, config.Options{}); err != nil || len(p.Links) != 1 || p.Links[0].Name != "a" {
		t.Fatalf("clean resolve wrong: %+v %v", p, err)
	}
	dup := []discovery.Skill{
		{Alias: "core", Name: "a", SourceDir: "/c/a"},
		{Alias: "handy", Name: "a", SourceDir: "/h/a"},
	}
	var ce *CollisionError
	if _, err := Resolve(dup, config.Options{}); !errors.As(err, &ce) || ce.Name != "a" {
		t.Fatalf("expected collision, got %v", err)
	}
	p, err := Resolve(dup, config.Options{PrefixOnCollision: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, l := range p.Links {
		names[l.Name] = true
	}
	if !names["core-a"] || !names["handy-a"] {
		t.Fatalf("prefix wrong: %+v", p.Links)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/resolve/resolve.go`:
```go
// Package resolve turns discovered skills into an install plan and enforces the
// collision policy.
package resolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/gherlein/skills-mapper/internal/config"
	"github.com/gherlein/skills-mapper/internal/discovery"
)

type Link struct{ Name, SourceDir, Alias string }
type Plan struct{ Links []Link }

type CollisionError struct {
	Name    string
	Aliases []string
}

func (e *CollisionError) Error() string {
	return fmt.Sprintf("duplicate skill %q from sources %s (rename, drop one, or set prefix_on_collision)",
		e.Name, strings.Join(e.Aliases, ", "))
}

func Resolve(skills []discovery.Skill, opts config.Options) (*Plan, error) {
	if !opts.PrefixOnCollision {
		byName := map[string][]string{}
		for _, s := range skills {
			byName[s.Name] = append(byName[s.Name], s.Alias)
		}
		for name, aliases := range byName {
			if len(aliases) > 1 {
				sort.Strings(aliases)
				return nil, &CollisionError{Name: name, Aliases: aliases}
			}
		}
	}
	var links []Link
	for _, s := range skills {
		name := s.Name
		if opts.PrefixOnCollision {
			name = s.Alias + "-" + s.Name
		}
		links = append(links, Link{Name: name, SourceDir: s.SourceDir, Alias: s.Alias})
	}
	sort.Slice(links, func(i, j int) bool { return links[i].Name < links[j].Name })
	return &Plan{Links: links}, nil
}
```

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `git add internal/resolve/ && git commit -m "feat(resolve): collision policy and install plan"`

---

### Task 5: Scope-aware agent registry (5 agents)

**Files:** Create `internal/agents/agents.go`; Test `internal/agents/agents_test.go`

**Interfaces — Consumes:** `config.Manifest`. **Produces:**
- `var Known = []string{"claude-code", "copilot", "hax", "oh-my-pi", "pi"}`
- `func TargetDir(id, scope, root string) (string, error)` — `scope` is `"global"` or `"project"`; `root` is `$HOME` (global) or the repo root (project).
- `func Enabled(m *config.Manifest) ([]string, error)` — sorted enabled ids; error on unknown id.

- [ ] **Step 1: Write the failing test**
```go
package agents

import (
	"testing"

	"github.com/gherlein/skills-mapper/internal/config"
)

func TestTargetDirScopes(t *testing.T) {
	cases := []struct{ id, scope, want string }{
		{"claude-code", "global", "/h/.claude/skills"},
		{"claude-code", "project", "/r/.claude/skills"},
		{"copilot", "global", "/h/.copilot/skills"},
		{"copilot", "project", "/r/.github/skills"},
		{"hax", "global", "/h/.config/hax/skills"},
		{"hax", "project", "/r/.agents/skills"},
		{"pi", "global", "/h/.pi-go/skills"},
		{"pi", "project", "/r/.pi/skills"},
		{"oh-my-pi", "global", "/h/.config/agents/skills"},
		{"oh-my-pi", "project", "/r/.agents/skills"},
	}
	for _, c := range cases {
		root := "/h"
		if c.scope == "project" {
			root = "/r"
		}
		got, err := TargetDir(c.id, c.scope, root)
		if err != nil || got != c.want {
			t.Errorf("TargetDir(%s,%s)=%q,%v want %q", c.id, c.scope, got, err, c.want)
		}
	}
	if _, err := TargetDir("nope", "global", "/h"); err == nil {
		t.Error("expected unknown-agent error")
	}
	if _, err := TargetDir("pi", "weird", "/h"); err == nil {
		t.Error("expected unknown-scope error")
	}
}

func TestEnabled(t *testing.T) {
	m := &config.Manifest{Agents: map[string]bool{"hax": true, "copilot": false}}
	got, err := Enabled(m)
	if err != nil || len(got) != 1 || got[0] != "hax" {
		t.Fatalf("enabled wrong: %v %v", got, err)
	}
	if _, err := Enabled(&config.Manifest{Agents: map[string]bool{"weird": true}}); err == nil {
		t.Fatal("expected unknown-agent error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/agents/agents.go`:
```go
// Package agents maps (agent, scope) to the directory the agent reads skills from.
package agents

import (
	"fmt"
	"path/filepath"
	"sort"

	"github.com/gherlein/skills-mapper/internal/config"
)

var Known = []string{"claude-code", "copilot", "hax", "oh-my-pi", "pi"}

type dirs struct{ global, project []string }

var registry = map[string]dirs{
	"claude-code": {[]string{".claude", "skills"}, []string{".claude", "skills"}},
	"copilot":     {[]string{".copilot", "skills"}, []string{".github", "skills"}},
	"hax":         {[]string{".config", "hax", "skills"}, []string{".agents", "skills"}},
	"pi":          {[]string{".pi-go", "skills"}, []string{".pi", "skills"}},
	"oh-my-pi":    {[]string{".config", "agents", "skills"}, []string{".agents", "skills"}},
}

func TargetDir(id, scope, root string) (string, error) {
	d, ok := registry[id]
	if !ok {
		return "", fmt.Errorf("unknown agent %q (known: %v)", id, Known)
	}
	var parts []string
	switch scope {
	case "global":
		parts = d.global
	case "project":
		parts = d.project
	default:
		return "", fmt.Errorf("unknown scope %q", scope)
	}
	return filepath.Join(append([]string{root}, parts...)...), nil
}

func Enabled(m *config.Manifest) ([]string, error) {
	var out []string
	for id, on := range m.Agents {
		if _, ok := registry[id]; !ok {
			return nil, fmt.Errorf("unknown agent %q in [agents] (known: %v)", id, Known)
		}
		if on {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
```

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `git add internal/agents/ && git commit -m "feat(agents): scope-aware five-agent registry"`

---

### Task 6: State model + reconcile planner

**Files:** Create `internal/state/state.go`; Test `internal/state/state_test.go`

**Interfaces — Consumes:** `resolve.Link`. **Produces:** `type TargetLinks struct { Links map[string]string; UpdatedAt string }`; `type State struct { Version int; Targets map[string]TargetLinks }`; `func Load(string) (*State, error)`; `func (*State) Save(string) error`; `type Recon struct { Add []resolve.Link; Remove []string }`; `func Plan(prev map[string]string, desired []resolve.Link) Recon`; `func DefaultPath() (string, error)`

- [ ] **Step 1: Write the failing test**
```go
package state

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

func TestPlanAddRemoveAndSourceChange(t *testing.T) {
	prev := map[string]string{"a": "/src/a", "b": "/src/b", "d": "/old/d"}
	desired := []resolve.Link{
		{Name: "b", SourceDir: "/src/b"}, // keep
		{Name: "c", SourceDir: "/src/c"}, // add
		{Name: "d", SourceDir: "/new/d"}, // re-add (source changed)
	}
	r := Plan(prev, desired)
	add := []string{}
	for _, l := range r.Add {
		add = append(add, l.Name)
	}
	sort.Strings(add)
	sort.Strings(r.Remove)
	if len(add) != 2 || add[0] != "c" || add[1] != "d" {
		t.Fatalf("add wrong: %v", add)
	}
	if len(r.Remove) != 1 || r.Remove[0] != "a" {
		t.Fatalf("remove wrong: %v", r.Remove)
	}
}

func TestSaveLoadAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.json")
	got, _ := Load(p) // missing -> empty
	if got.Version != 1 || len(got.Targets) != 0 {
		t.Fatalf("missing not empty: %+v", got)
	}
	got.Targets["/t"] = TargetLinks{Links: map[string]string{"a": "/s/a"}, UpdatedAt: "now"}
	if err := got.Save(p); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(p)
	if back.Targets["/t"].Links["a"] != "/s/a" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/state/state.go`:
```go
// Package state persists sm-created links per target and computes reconcile plans.
package state

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

const version = 1

type TargetLinks struct {
	Links     map[string]string `json:"links"`
	UpdatedAt string            `json:"updated_at"`
}
type State struct {
	Version int                    `json:"version"`
	Targets map[string]TargetLinks `json:"targets"`
}
type Recon struct {
	Add    []resolve.Link
	Remove []string
}

func Plan(prev map[string]string, desired []resolve.Link) Recon {
	want := map[string]bool{}
	var r Recon
	for _, l := range desired {
		want[l.Name] = true
		if src, ok := prev[l.Name]; !ok || src != l.SourceDir {
			r.Add = append(r.Add, l)
		}
	}
	for name := range prev {
		if !want[name] {
			r.Remove = append(r.Remove, name)
		}
	}
	return r
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Version: version, Targets: map[string]TargetLinks{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Targets == nil {
		s.Targets = map[string]TargetLinks{}
	}
	if s.Version == 0 {
		s.Version = version
	}
	return &s, nil
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func DefaultPath() (string, error) {
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "skills-mapper", "state.json"), nil
}
```

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `git add internal/state/ && git commit -m "feat(state): reconcile planner and persistence"`

---

### Task 7: Symlink apply (link)

**Files:** Create `internal/link/link.go`; Test `internal/link/link_test.go`

**Interfaces — Consumes:** `resolve.Link`. **Produces:** `func Apply(targetDir string, add []resolve.Link, remove []string, prev map[string]string) (installed map[string]string, conflicts []string, err error)`

- [ ] **Step 1: Write the failing test**
```go
package link

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

func TestApplyCreateRemoveProtect(t *testing.T) {
	target := filepath.Join(t.TempDir(), "skills")
	srcA, srcC := t.TempDir(), t.TempDir()

	installed, conflicts, err := Apply(target, []resolve.Link{{Name: "a", SourceDir: srcA}, {Name: "b", SourceDir: srcA}}, nil, map[string]string{})
	if err != nil || len(conflicts) != 0 {
		t.Fatalf("apply1: %v %v", err, conflicts)
	}
	os.Mkdir(filepath.Join(target, "manual"), 0o755) // unmanaged real dir

	installed, conflicts, err = Apply(target,
		[]resolve.Link{{Name: "manual", SourceDir: srcC}, {Name: "c", SourceDir: srcC}},
		[]string{"b"}, installed)
	if err != nil {
		t.Fatal(err)
	}
	if len(conflicts) != 1 || conflicts[0] != "manual" {
		t.Fatalf("conflict wrong: %v", conflicts)
	}
	if _, err := os.Lstat(filepath.Join(target, "b")); !os.IsNotExist(err) {
		t.Fatal("b should be removed")
	}
	if fi, _ := os.Lstat(filepath.Join(target, "manual")); fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("manual must stay a real dir")
	}
	if installed["manual"] != "" || installed["c"] != srcC {
		t.Fatalf("installed wrong: %+v", installed)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/link/link.go`:
```go
// Package link realizes an install plan as whole-directory symlinks, touching
// only links sm created.
package link

import (
	"os"
	"path/filepath"

	"github.com/gherlein/skills-mapper/internal/resolve"
)

func Apply(targetDir string, add []resolve.Link, remove []string, prev map[string]string) (map[string]string, []string, error) {
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return nil, nil, err
	}
	installed := map[string]string{}
	for k, v := range prev {
		installed[k] = v
	}
	for _, name := range remove {
		p := filepath.Join(targetDir, name)
		if isSymlink(p) {
			if err := os.Remove(p); err != nil {
				return nil, nil, err
			}
		}
		delete(installed, name)
	}
	var conflicts []string
	for _, l := range add {
		p := filepath.Join(targetDir, l.Name)
		fi, err := os.Lstat(p)
		switch {
		case err == nil && fi.Mode()&os.ModeSymlink == 0:
			conflicts = append(conflicts, l.Name) // protect unmanaged real entry
			continue
		case err == nil:
			if err := os.Remove(p); err != nil {
				return nil, nil, err
			}
		}
		if err := os.Symlink(l.SourceDir, p); err != nil {
			return nil, nil, err
		}
		installed[l.Name] = l.SourceDir
	}
	return installed, conflicts, nil
}

func isSymlink(p string) bool {
	fi, err := os.Lstat(p)
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}
```

- [ ] **Step 4: Run tests** — PASS.
- [ ] **Step 5: Commit** — `git add internal/link/ && git commit -m "feat(link): safe symlink apply"`

---

### Task 8: Git cache (clone/pull/prune)

**Files:** Create `internal/cache/cache.go`; Test `internal/cache/cache_test.go`

**Interfaces — Consumes:** `config.Source`. **Produces:** `func RepoDir(root, gitURL string) (string, error)`; `func Update(src config.Source, dir string) error`; `func SourceRoot(src config.Source, cacheRoot string) (string, error)`; `func Prune(root string, keep []string) error`; `func DefaultRoot() (string, error)`

- [ ] **Step 1: Write the failing test**
```go
package cache

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/gherlein/skills-mapper/internal/config"
)

func TestRepoDir(t *testing.T) {
	for url, want := range map[string]string{
		"https://github.com/acme/core":    "/c/github.com/acme/core",
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
```

- [ ] **Step 2: Run test to verify it fails** — FAIL.

- [ ] **Step 3: Write minimal implementation**

`internal/cache/cache.go`:
```go
// Package cache maintains durable local clones of git skill sources.
package cache

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/gherlein/skills-mapper/internal/config"
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
// repo dirs (RepoDir outputs). Only descends two levels (host/owner/repo).
func Prune(root string, keep []string) error {
	keepSet := map[string]bool{}
	for _, k := range keep {
		keepSet[filepath.Clean(k)] = true
	}
	hosts, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, h := range hosts {
		owners, err := os.ReadDir(filepath.Join(root, h.Name()))
		if err != nil {
			continue
		}
		for _, o := range owners {
			repos, err := os.ReadDir(filepath.Join(root, h.Name(), o.Name()))
			if err != nil {
				continue
			}
			for _, r := range repos {
				full := filepath.Join(root, h.Name(), o.Name(), r.Name())
				if !keepSet[full] {
					if err := os.RemoveAll(full); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
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
```

- [ ] **Step 4: Run tests** — `go test ./internal/cache/ -v` → PASS (needs `git`).
- [ ] **Step 5: Commit** — `git add internal/cache/ && git commit -m "feat(cache): durable git cache with prune"`

---

### Task 9: CLI core — Env, scope, `init`/`sync`/`list`, freshen/place helpers

**Files:** Create `internal/cli/cli.go`, `cmd/sm/main.go`; Test `internal/cli/cli_test.go`

**Interfaces — Produces:**
- `type Env struct { Scope, Root, ManifestPath, CacheRoot, StatePath string }`
- `func Run(args []string, stdout, stderr io.Writer) int` (resolves scope + Env, dispatches)
- `func run(env Env, args []string, stdout, stderr io.Writer) int`
- `func freshen(env Env, m *config.Manifest) error` (update + prune caches)
- `func place(env Env, m *config.Manifest, dryRun bool, stderr io.Writer) (installed, removed int, err error)`

- [ ] **Step 1: Write the failing test (end-to-end sync, both a git and a project scope)**
```go
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
```

- [ ] **Step 2: Run test to verify it fails** — FAIL (undefined).

- [ ] **Step 3: Write minimal implementation**

`internal/cli/cli.go`:
```go
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
```

`cmd/sm/main.go`:
```go
package main

import (
	"os"

	"github.com/gherlein/skills-mapper/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
```

*(Note: `cmdUpdate`, `cmdLink`, `cmdAdd`, `cmdRemove` are referenced here and implemented in Tasks 10–11. To keep this task compiling on its own, add temporary stubs returning `2` with "not implemented", then replace them in the later tasks. Stub example:)*
```go
func cmdUpdate(env Env, stdout, stderr io.Writer) int { fmt.Fprintln(stderr, "not implemented"); return 2 }
func cmdLink(env Env, args []string, stdout, stderr io.Writer) int { fmt.Fprintln(stderr, "not implemented"); return 2 }
func cmdAdd(env Env, args []string, stdout, stderr io.Writer) int { fmt.Fprintln(stderr, "not implemented"); return 2 }
func cmdRemove(env Env, args []string, stdout, stderr io.Writer) int { fmt.Fprintln(stderr, "not implemented"); return 2 }
```

- [ ] **Step 4: Run tests + build** — `go test ./internal/cli/ -v` → PASS; `make build` → succeeds.
- [ ] **Step 5: Commit** — `git add internal/cli/ cmd/sm/ && git commit -m "feat(cli): scope-aware init/sync/list with freshen/place"`

---

### Task 10: `add` and `remove` commands

**Files:** Modify `internal/cli/cli.go` (replace the `cmdAdd`/`cmdRemove` stubs); Test `internal/cli/cli_test.go`

**Interfaces — Consumes:** `config.AddSource/RemoveSource/Save`, `freshen`, `place`.

- [ ] **Step 1: Write the failing test** (append):
```go
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
```

- [ ] **Step 2: Run test to verify it fails** — FAIL (stubs return "not implemented").

- [ ] **Step 3: Write minimal implementation** (replace the `cmdAdd`/`cmdRemove` stubs; add `"strings"` to imports):
```go
func cmdAdd(env Env, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	as := fs.String("as", "", "alias (defaults to repo/dir name)")
	ref := fs.String("ref", "", "git ref (branch|tag|commit)")
	subdir := fs.String("subdir", "", "subdirectory within the source")
	sync := fs.Bool("sync", false, "sync after adding")
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: sm add <git-url|path> [--as alias] [--ref r] [--subdir d] [--sync]")
		return 2
	}
	target := fs.Arg(0)
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
		if err := freshen(env, m); err != nil {
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
	if fs.Parse(args) != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: sm remove <alias> [--sync] [--keep-cache]")
		return 2
	}
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !m.RemoveSource(fs.Arg(0)) {
		fmt.Fprintf(stderr, "no such source %q\n", fs.Arg(0))
		return 1
	}
	if err := config.Save(env.ManifestPath, m); err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if *sync {
		if !*keepCache {
			if err := freshen(env, m); err != nil { // prunes now-unreferenced cache
				fmt.Fprintln(stderr, "error:", err)
				return 1
			}
		}
		if _, _, err := place(env, m, false, stderr); err != nil { // reconcile prunes stale links
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "removed %s\n", fs.Arg(0))
	return 0
}

func looksLikeGit(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "file://") || strings.HasPrefix(s, "ssh://")
}

func deriveAlias(target string) string {
	base := filepath.Base(strings.TrimSuffix(target, ".git"))
	return base
}

func loadOrEmpty(path string) (*config.Manifest, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &config.Manifest{Skills: map[string]config.Source{}, Agents: map[string]bool{}}, nil
	}
	return config.Load(path)
}
```

- [ ] **Step 4: Run tests** — `go test ./internal/cli/ -v` → PASS.
- [ ] **Step 5: Commit** — `git add internal/cli/ && git commit -m "feat(cli): add and remove commands"`

---

### Task 11: `update` and `link` commands

**Files:** Modify `internal/cli/cli.go` (replace the `cmdUpdate`/`cmdLink` stubs); Test `internal/cli/cli_test.go`

**Interfaces — Consumes:** `freshen`, `place`.

- [ ] **Step 1: Write the failing test** (append):
```go
func TestUpdateThenLinkSeparately(t *testing.T) {
	origin := gitRepoWithSkill(t)
	home := t.TempDir()
	mp := filepath.Join(home, "skills.toml")
	os.WriteFile(mp, []byte("[skills]\ncore = { git = \"file://"+origin+"\", ref = \"main\" }\n[agents]\nclaude-code = true\n"), 0o644)
	env := Env{Scope: "global", Root: home, ManifestPath: mp, CacheRoot: filepath.Join(home, "cache"), StatePath: filepath.Join(home, "state.json")}
	var out, errOut bytes.Buffer

	// update: caches the repo, but creates no links yet
	if code := run(env, []string{"update"}, &out, &errOut); code != 0 {
		t.Fatalf("update exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills", "git-workflow")); !os.IsNotExist(err) {
		t.Fatal("update must not create links")
	}
	// link: places from the existing cache
	if code := run(env, []string{"link"}, &out, &errOut); code != 0 {
		t.Fatalf("link exit %d: %s", code, errOut.String())
	}
	if _, err := os.Lstat(filepath.Join(home, ".claude/skills", "git-workflow")); err != nil {
		t.Fatalf("link must create the symlink: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails** — FAIL (stubs).

- [ ] **Step 3: Write minimal implementation** (replace `cmdUpdate`/`cmdLink` stubs):
```go
func cmdUpdate(env Env, stdout, stderr io.Writer) int {
	m, err := config.Load(env.ManifestPath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if err := freshen(env, m); err != nil {
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
```

- [ ] **Step 4: Run tests** — `go test ./internal/cli/ -v` → PASS.
- [ ] **Step 5: Commit** — `git add internal/cli/ && git commit -m "feat(cli): update and link commands"`

---

### Task 12: Full-suite gate + README

**Files:** Create `README.md`; whole suite.

- [ ] **Step 1: Gate** — `gofmt -l .` (no output), `go vet ./...` (clean), `go test ./...` (all PASS).

- [ ] **Step 2: Smoke test the binary**
```bash
make build
./sm --global init && cat ~/.config/skills-mapper/skills.toml
./sm --global list
```
Expected: build ok; `init` writes the starter; `list` runs (empty).

- [ ] **Step 3: Write `README.md`** — cover: one-line what/why; install (`go build -o sm ./cmd/sm`); a minimal `skills.toml`; the seven commands and `--global`/`--project`; the five agents and their dirs; where manifest/cache/state live; link to `docs/DESIGN.md`. Short. No "works for me" disclaimer (repo remote is `brightsign-playground/all-repos-report`, not gherlein/emergingrobotics).

- [ ] **Step 4: Commit** — `git add README.md && git commit -m "docs: add README"`

---

## Self-Review

**Spec coverage:** git+path sources (Tasks 1, 8); no plugins/registry/auth (config knows only git/path). Config write + project discovery (Task 2). Discovery across layouts (Task 3). Collision fail-loud + opt-in prefix (Task 4). **Five agents, scope-aware** (Task 5). State + safe reconcile (Task 6). Whole-dir symlinks + unmanaged-target protection (Task 7). Durable cache + prune (Task 8). **Scope (global/project + `--global`/`--project`)** and `init`/`sync`/`list` (Task 9). **`add`/`remove`** (Task 10). **`update`/`link` split** (Task 11). Copilot `.github/skills` = the `copilot` project dir in the Task 5 registry. Gate + README (Task 12).

**Placeholder scan:** No TBD/TODO. Task 9 introduces temporary stubs for `cmdUpdate/Link/Add/Remove` **only so the package compiles**; Tasks 10–11 replace them with real code — this is explicit, not a placeholder. Task 12 Step 3 (README) enumerates required contents.

**Type consistency:** `config.{Source,Options,Manifest,Parse,Load,Save,AddSource,RemoveSource,ProjectManifestPath}`, `discovery.{Skill,Discover}`, `resolve.{Link,Plan,CollisionError,Resolve}`, `agents.{Known,TargetDir(id,scope,root),Enabled}`, `state.{TargetLinks,State,Recon,Plan,Load,Save}`, `cache.{RepoDir,Update,SourceRoot,Prune,DefaultRoot}`, `link.Apply(target,add,remove,prev)->(map,[]string,error)`, and `cli.{Env(Scope,Root,ManifestPath,CacheRoot,StatePath),Run,run,freshen,place}` are used identically across producing and consuming tasks. `place` returns `(installed, removed int, err error)` and is consumed that way in sync/link/add/remove.

---

## Execution Handoff

Choose after review.
