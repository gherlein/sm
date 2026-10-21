package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/agents"
)

func TestRerootAgents_UnderHome(t *testing.T) {
	home := filepath.FromSlash("/home/user")
	alt := filepath.FromSlash("/srv/agent-home")
	cfg := agents.Config{Agents: []agents.Agent{
		{Name: "claude-code", Dir: filepath.Join(home, ".claude", "skills")},
		{Name: "copilot", Dir: filepath.Join(home, ".copilot", "skills")},
	}}

	got, err := rerootAgentsFromHomes(cfg, []string{home}, alt)
	if err != nil {
		t.Fatalf("rerootAgentsFromHomes: %v", err)
	}
	want := []agents.Agent{
		{Name: "claude-code", Dir: filepath.Join(alt, ".claude", "skills")},
		{Name: "copilot", Dir: filepath.Join(alt, ".copilot", "skills")},
	}
	for i, agent := range got.Agents {
		if agent != want[i] {
			t.Errorf("agent %d = %+v, want %+v", i, agent, want[i])
		}
	}
}

func TestRerootAgents_NotUnderHomeIsError(t *testing.T) {
	home := filepath.FromSlash("/home/user")
	cfg := agents.Config{Agents: []agents.Agent{
		{Name: "weird", Dir: filepath.FromSlash("/opt/weird/skills")},
	}}
	if _, err := rerootAgentsFromHomes(cfg, []string{home}, filepath.FromSlash("/srv/alt")); err == nil {
		t.Fatal("expected an error for a dir not under home, got nil")
	}
}

func TestRerootAgents_DirEqualsHome(t *testing.T) {
	home := filepath.FromSlash("/home/user")
	alt := filepath.FromSlash("/srv/alt")
	cfg := agents.Config{Agents: []agents.Agent{{Name: "odd", Dir: home}}}
	got, err := rerootAgentsFromHomes(cfg, []string{home}, alt)
	if err != nil {
		t.Fatalf("rerootAgentsFromHomes: %v", err)
	}
	if got.Agents[0].Dir != alt {
		t.Errorf("dir = %q, want %q", got.Agents[0].Dir, alt)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	return resolved
}

func TestApplyOverrides_StoreFlagTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("home: %v", err)
	}
	storeDir, _, _, err := applyOverrides("/default/store", agents.Config{}, "~/alternate-skills", "")
	if err != nil {
		t.Fatalf("applyOverrides: %v", err)
	}
	// The home directory itself may be a symlink, so compare resolved forms.
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatalf("resolve home: %v", err)
	}
	if want := filepath.Join(home, "alternate-skills"); storeDir != want && storeDir != filepath.Join(resolvedHome, "alternate-skills") {
		t.Errorf("storeDir = %q, want %q", storeDir, want)
	}
}

func TestApplyOverrides_StoreRelativeMadeAbsolute(t *testing.T) {
	storeDir, _, _, err := applyOverrides("/default/store", agents.Config{}, "relative/skills", "")
	if err != nil {
		t.Fatalf("applyOverrides: %v", err)
	}
	if !filepath.IsAbs(storeDir) {
		t.Errorf("storeDir = %q, want an absolute path", storeDir)
	}
}

func TestApplyOverrides_DoesNotExpandEnvInFlags(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Setenv("SM_TEST_BASE", "/data/root")
	home := realTempDir(t)

	storeDir, _, _, err := applyOverrides("/default/store", agents.Config{}, "$SM_TEST_BASE/skills", "")
	if err != nil {
		t.Fatalf("applyOverrides store: %v", err)
	}
	if want := filepath.Join(workingDirectory, "$SM_TEST_BASE", "skills"); storeDir != want {
		t.Errorf("storeDir = %q, want the literal relative path %q", storeDir, want)
	}

	cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(home, ".a", "skills")}}}
	t.Setenv("HOME", home)
	_, rerooted, _, err := applyOverrides("/default/store", cfg, "", "$SM_SURELY_UNSET")
	if err != nil {
		t.Fatalf("applyOverrides agent-home: %v", err)
	}
	want := filepath.Join(workingDirectory, "$SM_SURELY_UNSET", ".a", "skills")
	if rerooted.Agents[0].Dir != want {
		t.Errorf("agent dir = %q, want the literal relative path %q", rerooted.Agents[0].Dir, want)
	}
	if rerooted.Agents[0].Dir == filepath.Join(workingDirectory, ".a", "skills") {
		t.Error("unset variable expanded to empty and re-rooted into the current directory")
	}
}

func TestApplyOverrides_AgentHomeSymlinkAliasesResolveToSameDir(t *testing.T) {
	root := realTempDir(t)
	home := filepath.Join(root, "home")
	realBox := filepath.Join(root, "realbox")
	alias := filepath.Join(root, "linkbox")
	for _, dir := range []string{home, realBox} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(realBox, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(home, ".a", "skills")}}}

	_, viaAlias, _, err := applyOverrides("/default/store", cfg, "", alias)
	if err != nil {
		t.Fatalf("alias: %v", err)
	}
	_, viaReal, _, err := applyOverrides("/default/store", cfg, "", realBox)
	if err != nil {
		t.Fatalf("real: %v", err)
	}
	if viaAlias.Agents[0].Dir != viaReal.Agents[0].Dir {
		t.Errorf("alias dir %q != real dir %q", viaAlias.Agents[0].Dir, viaReal.Agents[0].Dir)
	}
	if want := filepath.Join(realBox, ".a", "skills"); viaAlias.Agents[0].Dir != want {
		t.Errorf("dir = %q, want %q", viaAlias.Agents[0].Dir, want)
	}
}

func TestApplyOverrides_DuplicateAgentDirsCollapsed(t *testing.T) {
	root := realTempDir(t)
	realDir := filepath.Join(root, "shared")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Fatal(err)
	}
	cfg := agents.Config{Agents: []agents.Agent{
		{Name: "alpha", Dir: realDir},
		{Name: "beta", Dir: alias},
		{Name: "gamma", Dir: filepath.Join(root, "own")},
		{Name: "delta", Dir: realDir},
	}}
	_, got, warnings, err := applyOverrides(filepath.Join(root, "store"), cfg, "", "")
	if err != nil {
		t.Fatalf("applyOverrides: %v", err)
	}
	if len(got.Agents) != 2 || got.Agents[0].Name != "alpha" || got.Agents[1].Name != "gamma" {
		t.Fatalf("agents = %+v, want alpha and gamma only", got.Agents)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want 2", warnings)
	}
	for index, collapsed := range []string{"beta", "delta"} {
		if !strings.Contains(warnings[index], collapsed) || !strings.Contains(warnings[index], "alpha") {
			t.Errorf("warning %q should name %q and %q", warnings[index], collapsed, "alpha")
		}
	}
}

func TestApplyOverrides_RejectsAgentDirOverlappingStore(t *testing.T) {
	root := realTempDir(t)
	store := filepath.Join(root, "store")
	cases := []struct {
		name     string
		agentDir string
	}{
		{"agent dir inside store", filepath.Join(store, "agent")},
		{"store inside agent dir", root},
		{"agent dir equals store", store},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: testCase.agentDir}}}
			_, _, _, err := applyOverrides(store, cfg, "", "")
			if err == nil {
				t.Fatal("expected an overlap error")
			}
			if !strings.Contains(err.Error(), testCase.agentDir) || !strings.Contains(err.Error(), store) {
				t.Errorf("error %q should name both paths", err)
			}
		})
	}
}

func TestApplyOverrides_RejectsOverlapThroughSymlink(t *testing.T) {
	root := realTempDir(t)
	store := filepath.Join(root, "store")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(store, alias); err != nil {
		t.Fatal(err)
	}
	cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(alias, "agent")}}}
	if _, _, _, err := applyOverrides(store, cfg, "", ""); err == nil {
		t.Fatal("expected an overlap error through a symlink alias")
	}
}

func TestApplyOverrides_RejectsAgentDirThatIsHomeOrRoot(t *testing.T) {
	root := realTempDir(t)
	t.Setenv("HOME", root)
	for _, dir := range []string{"/", root} {
		cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: dir}}}
		if _, _, _, err := applyOverrides(filepath.Join(t.TempDir(), "store"), cfg, "", ""); err == nil {
			t.Errorf("dir %q: expected an error", dir)
		}
	}
}

func TestApplyOverrides_SiblingStorePrefixIsNotOverlap(t *testing.T) {
	root := realTempDir(t)
	store := filepath.Join(root, "store")
	sibling := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(root, "store2")}}}
	if _, _, _, err := applyOverrides(store, sibling, "", ""); err != nil {
		t.Errorf("store2 next to store must be allowed: %v", err)
	}
	inside := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(store, "x")}}}
	if _, _, _, err := applyOverrides(store, inside, "", ""); err == nil {
		t.Error("agent dir inside store must be refused")
	}
}

func TestApplyOverrides_RejectsSymlinkAliasOfHome(t *testing.T) {
	root := realTempDir(t)
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "homealias")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	store := filepath.Join(root, "store")
	for _, dir := range []string{alias, filepath.Join(home, "sub", ".."), root} {
		cfg := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: dir}}}
		if _, _, _, err := applyOverrides(store, cfg, "", ""); err == nil {
			t.Errorf("agent dir %q resolves to or above home: expected an error", dir)
		}
	}
}

func TestApplyOverrides_SymlinkedHomeIsHandled(t *testing.T) {
	root := realTempDir(t)
	realHome := filepath.Join(root, "realhome")
	if err := os.Mkdir(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedHome := filepath.Join(root, "linkedhome")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", linkedHome)
	store := filepath.Join(root, "store")
	// The real directory is HOME; the symlinked spelling is what HOME holds.
	if _, _, _, err := applyOverrides(store, agents.Config{Agents: []agents.Agent{{Name: "a", Dir: realHome}}}, "", ""); err == nil {
		t.Error("real spelling of a symlinked HOME must be refused")
	}
	ok := agents.Config{Agents: []agents.Agent{{Name: "a", Dir: filepath.Join(linkedHome, ".a", "skills")}}}
	if _, _, _, err := applyOverrides(store, ok, "", ""); err != nil {
		t.Errorf("a dir below a symlinked HOME must be allowed: %v", err)
	}
}

func TestApplyOverrides_AgentHomeAgreesForBothHomeSpellings(t *testing.T) {
	root := realTempDir(t)
	realHome := filepath.Join(root, "realhome")
	if err := os.Mkdir(realHome, 0o755); err != nil {
		t.Fatal(err)
	}
	linkedHome := filepath.Join(root, "linkedhome")
	if err := os.Symlink(realHome, linkedHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", linkedHome)
	agentHome := filepath.Join(root, "agenthome")
	if err := os.Mkdir(agentHome, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, spelling := range []string{linkedHome, realHome} {
		cfg := agents.Config{Agents: []agents.Agent{{Name: "claude-code", Dir: filepath.Join(spelling, ".claude", "skills")}}}
		_, effective, _, err := applyOverrides(filepath.Join(root, "store"), cfg, "", agentHome)
		if err != nil {
			t.Fatalf("spelling %s: %v", spelling, err)
		}
		if want := filepath.Join(agentHome, ".claude", "skills"); effective.Agents[0].Dir != want {
			t.Errorf("spelling %s: dir = %q, want %q", spelling, effective.Agents[0].Dir, want)
		}
	}
}
