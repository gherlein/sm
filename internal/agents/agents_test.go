package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestExpand_Tilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	got, err := Expand("~/x")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if want := filepath.Join(home, "x"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExpand_EnvVar(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_DATA_HOME", base)
	got, err := Expand("$XDG_DATA_HOME/x")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if want := base + "/x"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestExpand_RelativeIsError(t *testing.T) {
	if _, err := Expand("rel/path"); err == nil {
		t.Fatal("expected error for relative path")
	}
}

func TestLoad_Valid(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	path := writeConfig(t, "[agents]\ncopilot = \"~/.copilot/skills\"\nclaude-code = \"~/.claude/skills\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []Agent{
		{Name: "claude-code", Dir: filepath.Join(home, ".claude/skills")},
		{Name: "copilot", Dir: filepath.Join(home, ".copilot/skills")},
	}
	if len(cfg.Agents) != len(want) {
		t.Fatalf("got %v want %v", cfg.Agents, want)
	}
	for index := range want {
		if cfg.Agents[index] != want[index] {
			t.Fatalf("index %d: got %v want %v", index, cfg.Agents[index], want[index])
		}
	}
}

func TestLoad_CommentsIgnored(t *testing.T) {
	path := writeConfig(t, "# top comment\n[agents]\n# pi = \"~/.pi-go/skills\"\nkit = \"/abs/kit\"\n")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Agents) != 1 || cfg.Agents[0].Name != "kit" {
		t.Fatalf("got %v", cfg.Agents)
	}
}

func TestLoad_NonAbsoluteValue(t *testing.T) {
	path := writeConfig(t, "[agents]\nweird = \"rel\"\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "weird") {
		t.Fatalf("error should name agent: %v", err)
	}
}

func TestLoad_Missing(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.toml")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestWriteStarter_CreatesThenRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "agents.toml")
	if err := WriteStarter(path); err != nil {
		t.Fatalf("first WriteStarter: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "[agents]") {
		t.Fatalf("missing header: %s", data)
	}
	if !strings.Contains(string(data), `# claude-code = "~/.claude/skills"`) {
		t.Fatalf("missing example: %s", data)
	}
	if err := WriteStarter(path); err == nil {
		t.Fatal("second WriteStarter should fail")
	}
}

func TestDefaultConfigPath(t *testing.T) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	got, err := DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if want := filepath.Join(base, "skills-mapper", "agents.toml"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	t.Setenv("XDG_CONFIG_HOME", "relative")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	got, err = DefaultConfigPath()
	if err != nil {
		t.Fatalf("DefaultConfigPath: %v", err)
	}
	if want := filepath.Join(home, ".config", "skills-mapper", "agents.toml"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRegistry_HasKnownAgents(t *testing.T) {
	found := false
	for _, known := range Registry() {
		if known.Name == "claude-code" {
			found = true
			if known.DefaultDir != "~/.claude/skills" || known.ConfigDir != "~/.claude" || known.Binary != "claude" {
				t.Fatalf("unexpected claude-code entry: %+v", known)
			}
		}
	}
	if !found {
		t.Fatal("claude-code missing from registry")
	}
	if len(Registry()) < 6 {
		t.Fatalf("registry too small: %d", len(Registry()))
	}
}

func TestExpand_UndefinedVarIsError(t *testing.T) {
	_, err := Expand("$SM_TEST_SURELY_UNSET_VAR/skills")
	if err == nil {
		t.Fatal("expected an error for an undefined variable")
	}
	if !strings.Contains(err.Error(), "undefined or empty environment variable $SM_TEST_SURELY_UNSET_VAR") {
		t.Errorf("error %q does not name the undefined variable", err)
	}
}

func TestExpand_BracedUndefinedVarIsError(t *testing.T) {
	if _, err := Expand("${SM_TEST_SURELY_UNSET_VAR}/skills"); err == nil {
		t.Fatal("expected an error for an undefined braced variable")
	}
}

func TestExpand_EmptyResultIsError(t *testing.T) {
	t.Setenv("SM_TEST_EMPTY_VAR", "")
	_, err := Expand("$SM_TEST_EMPTY_VAR")
	if err == nil {
		t.Fatal("expected an error for an empty expansion")
	}
}

func TestLoad_UnknownTopLevelKeyIsError(t *testing.T) {
	path := writeConfig(t, "[agents]\nkit = \"/abs/kit\"\n\n[extra]\nx = 1\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for an unknown top-level key")
	}
	if !strings.Contains(err.Error(), "extra") {
		t.Errorf("error %q does not list the unknown key", err)
	}
}

func TestLoad_AgentTypoTableIsError(t *testing.T) {
	path := writeConfig(t, "[agent]\nkit = \"/abs/kit\"\n")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected an error for the [agent] typo")
	}
	if !strings.Contains(err.Error(), "agent") {
		t.Errorf("error %q does not list the typo key", err)
	}
}

func TestLoad_RejectsRootAndHomeDirs(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	for _, dir := range []string{"/", "~", home} {
		path := writeConfig(t, "[agents]\nbad = \""+dir+"\"\n")
		if _, err := Load(path); err == nil {
			t.Errorf("Load with dir %q: expected an error", dir)
		}
	}
}

func TestLoad_UndefinedVarIsError(t *testing.T) {
	path := writeConfig(t, "[agents]\nbad = \"$SM_TEST_SURELY_UNSET_VAR/skills\"\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected an error: an unset variable must not become /skills")
	}
}

func TestCheckDir(t *testing.T) {
	home := "/home/user"
	cases := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{"root", "/", true},
		{"home itself", "/home/user", true},
		{"home with trailing slash", "/home/user/", true},
		{"below home", "/home/user/.claude/skills", false},
		{"elsewhere", "/opt/skills", false},
		{"parent of home", "/home", true},
		{"grandparent of home", "/", true},
		{"sibling of home", "/home/other", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := CheckDir(testCase.dir, home)
			if (err != nil) != testCase.wantErr {
				t.Errorf("CheckDir(%q, %q) error = %v, wantErr %v", testCase.dir, home, err, testCase.wantErr)
			}
		})
	}
}

func TestExpand_EmptyVariableInsidePathIsError(t *testing.T) {
	t.Setenv("SM_TEST_EMPTY_VAR", "")
	for _, path := range []string{"$SM_TEST_EMPTY_VAR/skills", "${SM_TEST_EMPTY_VAR}/skills"} {
		_, err := Expand(path)
		if err == nil {
			t.Errorf("Expand(%q): expected an error: an empty variable must not become /skills", path)
			continue
		}
		if !strings.Contains(err.Error(), "undefined or empty environment variable $SM_TEST_EMPTY_VAR") {
			t.Errorf("Expand(%q) error %q does not name the variable", path, err)
		}
	}
}

func TestLoad_EmptyAgentsTableNeedsNoHome(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(path, []byte("[agents]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", "")
	config, err := Load(path)
	if err != nil {
		t.Fatalf("Load with HOME unset: %v", err)
	}
	if len(config.Agents) != 0 {
		t.Errorf("want no agents, got %v", config.Agents)
	}
}

// A "$" inside the home directory's own path is not a variable reference:
// variables are expanded first and the leading "~" only afterwards, so the
// home path is inserted verbatim.
func TestExpand_DollarInsideHomeIsNotReexpanded(t *testing.T) {
	home := filepath.Join(t.TempDir(), "dol$lar")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	got, err := Expand("~/.claude/skills")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if want := filepath.Join(home, ".claude", "skills"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// A "~" is the home directory only when the user wrote it: one that arrives
// from a variable's value is literal text, so the path stays relative and is
// refused instead of silently pointing into the home directory.
func TestExpand_TildeFromVariableIsNotExpanded(t *testing.T) {
	t.Setenv("SM_TEST_TILDE_VAR", "~/s")
	if got, err := Expand("$SM_TEST_TILDE_VAR"); err == nil {
		t.Fatalf("Expand($SM_TEST_TILDE_VAR) = %q, want an error: a tilde from a variable must stay literal", got)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	t.Setenv("SM_TEST_SUBDIR_VAR", "sub")
	got, err := Expand("~/$SM_TEST_SUBDIR_VAR/skills")
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if want := filepath.Join(home, "sub", "skills"); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
