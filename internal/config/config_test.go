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
