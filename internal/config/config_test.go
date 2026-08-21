package config

import (
	"os"
	"path/filepath"
	"testing"
)

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
