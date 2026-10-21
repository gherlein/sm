package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/probe"
)

func fakeHomeWithClaude(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func neverOnPath(string) bool { return false }

func TestDoProbe_AddsConfirmed(t *testing.T) {
	home := fakeHomeWithClaude(t)
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	var out bytes.Buffer

	added, err := doProbe(cfgPath, home, neverOnPath, func(probe.Found) bool { return true }, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) == 0 {
		t.Fatal("expected agents to be added")
	}
	cfg, err := agents.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	foundClaude := false
	for _, agent := range cfg.Agents {
		if agent.Name == "claude-code" {
			foundClaude = true
		}
	}
	if !foundClaude {
		t.Fatalf("claude-code missing from config: %+v", cfg.Agents)
	}
	if !strings.Contains(out.String(), "claude-code") {
		t.Fatalf("output should mention added agent: %q", out.String())
	}
}

func TestDoProbe_DryRunNoWrite(t *testing.T) {
	home := fakeHomeWithClaude(t)
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	var out bytes.Buffer

	added, err := doProbe(cfgPath, home, neverOnPath, func(probe.Found) bool { return false }, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none", added)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config must not be created, stat err = %v", err)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"bogus"}, &stdout, &stderr); code == 0 {
		t.Fatal("expected non-zero exit")
	}
	assertUsageListsAllCommands(t, stderr.String())
}

func TestRun_NoArgsPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(nil, &stdout, &stderr); code == 0 {
		t.Fatal("expected non-zero exit")
	}
	assertUsageListsAllCommands(t, stderr.String())
}

func assertUsageListsAllCommands(t *testing.T, usage string) {
	t.Helper()
	for _, name := range []string{"sync", "status", "probe", "clean", "init"} {
		if !strings.Contains(usage, name) {
			t.Errorf("usage missing %q: %q", name, usage)
		}
	}
}

func TestDoProbeDryRun_ReportsWithoutWriting(t *testing.T) {
	home := fakeHomeWithClaude(t)
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	var out bytes.Buffer

	wouldAdd, err := doProbeDryRun(cfgPath, home, neverOnPath, &out)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(wouldAdd, "claude-code") {
		t.Fatalf("wouldAdd = %v, want claude-code", wouldAdd)
	}
	if !strings.Contains(out.String(), "would add claude-code") || !strings.Contains(out.String(), "dry run: ") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if strings.Contains(out.String(), "no new agents added") {
		t.Fatalf("contradictory output: %q", out.String())
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("config must not be created, stat err = %v", err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
