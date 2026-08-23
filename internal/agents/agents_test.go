package agents

import (
	"testing"

	"github.com/brightsign-playground/sm/internal/config"
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
