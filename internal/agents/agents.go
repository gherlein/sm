// Package agents loads the agents.toml config (agent name -> skills directory)
// and holds the registry of well-known agents.
package agents

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/gherlein/sm/internal/pathutil"
)

const (
	configDirName  = "skills-mapper"
	configFileName = "agents.toml"
	xdgConfigEnv   = "XDG_CONFIG_HOME"
	homeConfigDir  = ".config"
	homePrefix     = "~"
	starterDirMode = 0o755
	starterMode    = 0o644
)

// Agent is an enabled agent; Dir is an absolute, expanded skills directory.
type Agent struct {
	Name string
	Dir  string
}

// Config holds the enabled agents, sorted by Name.
type Config struct {
	Agents []Agent
}

// Known is a well-known agent used for suggestions; the directories may
// contain a leading "~".
type Known struct {
	Name       string
	DefaultDir string
	ConfigDir  string
	Binary     string
}

// Registry returns the built-in well-known agents.
func Registry() []Known {
	return []Known{
		{Name: "claude-code", DefaultDir: "~/.claude/skills", ConfigDir: "~/.claude", Binary: "claude"},
		{Name: "copilot", DefaultDir: "~/.copilot/skills", ConfigDir: "~/.copilot", Binary: "copilot"},
		{Name: "hax", DefaultDir: "~/.config/hax/skills", ConfigDir: "~/.config/hax", Binary: "hax"},
		{Name: "pi", DefaultDir: "~/.pi-go/skills", ConfigDir: "~/.pi-go", Binary: "pi"},
		{Name: "oh-my-pi", DefaultDir: "~/.config/agents/skills", ConfigDir: "~/.config/agents", Binary: "omp"},
		{Name: "kit", DefaultDir: "~/.config/kit/skills", ConfigDir: "~/.config/kit", Binary: "kit"},
	}
}

// DefaultConfigPath returns $XDG_CONFIG_HOME/skills-mapper/agents.toml. A
// relative XDG_CONFIG_HOME is ignored because the XDG spec requires absolute
// paths.
func DefaultConfigPath() (string, error) {
	base := os.Getenv(xdgConfigEnv)
	if base == "" || !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(home, homeConfigDir)
	}
	return filepath.Join(base, configDirName, configFileName), nil
}

// Expand resolves a leading "~" and environment variables, and requires the
// result to be absolute and non-empty. The tilde is recognized on the string
// as written only, so a "~" that arrives inside a variable's value stays
// literal; the home directory is inserted verbatim after the variables are
// expanded, so a "$" inside the home path itself is not a variable reference
// and is never expanded again. An undefined variable is an error:
// os.ExpandEnv would silently turn "$NOPE/skills" into "/skills", pointing an
// agent at the wrong directory.
func Expand(path string) (string, error) {
	hasTilde := path == homePrefix || strings.HasPrefix(path, homePrefix+"/")
	remainder := path
	if hasTilde {
		remainder = path[len(homePrefix):]
	}
	var undefined []string
	expanded := os.Expand(remainder, func(name string) string {
		value := os.Getenv(name)
		// An empty value is as dangerous as an unset one: "$X/skills" would
		// silently become "/skills".
		if value == "" {
			undefined = append(undefined, name)
		}
		return value
	})
	if len(undefined) > 0 {
		return "", fmt.Errorf("undefined or empty environment variable $%s in %q", undefined[0], path)
	}
	if hasTilde {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		expanded = home + expanded
	}
	if expanded == "" {
		return "", fmt.Errorf("path %q expands to an empty string", path)
	}
	if !filepath.IsAbs(expanded) {
		return "", fmt.Errorf("path %q is not absolute after expansion", expanded)
	}
	return filepath.Clean(expanded), nil
}

// CheckDir refuses agent skills directories that are too broad to manage: the
// home directory itself or any ancestor of it (including the filesystem root),
// since syncing into such a directory would manage the user's whole tree.
func CheckDir(dir, home string) error {
	if pathutil.Within(dir, home) {
		return fmt.Errorf("skills directory %q is the home directory %q or contains it", dir, home)
	}
	return nil
}

type fileFormat struct {
	Agents map[string]string `toml:"agents"`
}

// Load parses the config at path. A missing file is an error.
func Load(path string) (Config, error) {
	var parsed fileFormat
	meta, err := toml.DecodeFile(path, &parsed)
	if err != nil {
		return Config{}, fmt.Errorf("load %s: %w", path, err)
	}
	// BurntSushi/toml silently accepts agents being the wrong shape (e.g.
	// `agents = 5`), decoding it to an empty map. Reject that loudly rather
	// than behaving as if no agents were configured.
	if meta.IsDefined("agents") && meta.Type("agents") != "Hash" {
		return Config{}, fmt.Errorf("load %s: \"agents\" must be a table of name = \"dir\"", path)
	}
	// A typo such as [agent] decodes to nothing and would otherwise be a
	// silent no-op.
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		return Config{}, fmt.Errorf("load %s: unknown key(s) %s (agents are declared under [agents])", path, strings.Join(keys, ", "))
	}
	// Resolved lazily so a config with no agents loads without HOME.
	home := ""
	agents := make([]Agent, 0, len(parsed.Agents))
	for name, dir := range parsed.Agents {
		expanded, err := Expand(dir)
		if err != nil {
			return Config{}, fmt.Errorf("agent %q in %s: %w", name, path, err)
		}
		if home == "" {
			home, err = os.UserHomeDir()
			if err != nil {
				return Config{}, fmt.Errorf("resolve home directory: %w", err)
			}
		}
		if err := CheckDir(expanded, home); err != nil {
			return Config{}, fmt.Errorf("agent %q in %s: %w", name, path, err)
		}
		agents = append(agents, Agent{Name: name, Dir: expanded})
	}
	sort.Slice(agents, func(left, right int) bool { return agents[left].Name < agents[right].Name })
	return Config{Agents: agents}, nil
}

// WriteStarter writes a starter config with every registry entry commented
// out, and refuses to overwrite an existing file.
func WriteStarter(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), starterDirMode); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	var content strings.Builder
	content.WriteString("# Uncomment the agents to mirror skills into.\n[agents]\n")
	for _, known := range Registry() {
		fmt.Fprintf(&content, "# %s = %q\n", known.Name, known.DefaultDir)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, starterMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if _, err := file.WriteString(content.String()); err != nil {
		// Report a Close failure alongside the write failure rather than hiding it.
		return errors.Join(fmt.Errorf("write %s: %w", path, err), closeError(path, file.Close()))
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", path, err)
	}
	return nil
}

func closeError(path string, closeErr error) error {
	if closeErr == nil {
		return nil
	}
	return fmt.Errorf("close %s: %w", path, closeErr)
}
