package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// singleAgentEnv builds a hermetic setup with one agent "claude-code" at ~/.claude/skills.
func singleAgentEnv(t *testing.T) (testEnv, string) {
	t.Helper()
	fixture := newEnv(t)
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills"})
	return fixture, filepath.Join(fixture.home, ".claude", "skills")
}

func TestInit(t *testing.T) {
	t.Run("writes starter config", func(t *testing.T) {
		fixture := newEnv(t)
		r := mustRun(t, "init")
		if !strings.Contains(r.stdout, fixture.configPath) {
			t.Fatalf("stdout should name the config path %s\n%s", fixture.configPath, r)
		}
		content, err := os.ReadFile(fixture.configPath)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(content), "[agents]") {
			t.Fatalf("starter config lacks [agents] table: %q", content)
		}
		for _, agent := range []string{"claude-code", "copilot", "hax", "pi", "oh-my-pi", "kit"} {
			if !strings.Contains(string(content), agent) {
				t.Errorf("starter config does not mention known agent %q", agent)
			}
		}
	})

	t.Run("starter config is accepted by sync", func(t *testing.T) {
		fixture := newEnv(t)
		addSkill(t, fixture.storeDir, "alpha")
		mustRun(t, "init")
		// Known agents are commented out, so no agent is enabled.
		expectCode(t, run("sync"), exitOK)
	})

	t.Run("second init fails and does not clobber", func(t *testing.T) {
		fixture := newEnv(t)
		mustRun(t, "init")
		custom := "[agents]\nmine = \"~/.mine/skills\"\n"
		writeFile(t, fixture.configPath, custom)
		r := run("init")
		if r.code == exitOK {
			t.Fatalf("second init must fail\n%s", r)
		}
		if r.stderr == "" {
			t.Fatalf("second init must explain on stderr\n%s", r)
		}
		content, err := os.ReadFile(fixture.configPath)
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != custom {
			t.Fatalf("config was clobbered: %q", content)
		}
	})
}

func TestSyncCreatesLinks(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha", "beta", "gamma")

	r := mustRun(t, "sync")
	if r.stdout != "sync: claude-code +3 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertStderrEmpty(t, r)
	assertAgentLinks(t, agentDir, fixture.storeDir, "alpha", "beta", "gamma")
	assertStateLinks(t, fixture, agentDir, expectedLinks(fixture.storeDir, "alpha", "beta", "gamma"))

	t.Run("links resolve to store content", func(t *testing.T) {
		content, err := os.ReadFile(filepath.Join(agentDir, "alpha", "SKILL.md"))
		if err != nil {
			t.Fatal(err)
		}
		if string(content) != skillContent("alpha") {
			t.Fatalf("unexpected content %q", content)
		}
	})

	t.Run("second sync is idempotent", func(t *testing.T) {
		before := snapshot(t, agentDir)
		second := mustRun(t, "sync")
		if second.stdout != "sync: claude-code +0 -0\n" {
			t.Fatalf("summary mismatch\n%s", second)
		}
		assertSnapshotsEqual(t, before, snapshot(t, agentDir))
		assertStateLinks(t, fixture, agentDir, expectedLinks(fixture.storeDir, "alpha", "beta", "gamma"))
	})
}

func TestSyncCreatesMissingAgentDirAndParents(t *testing.T) {
	fixture := newEnv(t)
	deep := filepath.Join(fixture.home, "a", "b", "c", "skills")
	writeAgents(t, fixture, map[string]string{"deep": deep})
	addSkill(t, fixture.storeDir, "alpha")
	mustRun(t, "sync")
	assertAgentLinks(t, deep, fixture.storeDir, "alpha")
}

func TestSyncSkipsNonConformingStoreEntries(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	store := fixture.storeDir
	addSkill(t, store, "good")
	addSkill(t, store, "also-good-1")
	addSkill(t, store, "a")
	writeFile(t, filepath.Join(store, "README"), "readme")
	writeFile(t, filepath.Join(store, "mismatch", "SKILL.md"), skillContent("other"))
	mkdir(t, filepath.Join(store, "empty-dir"))
	writeFile(t, filepath.Join(store, "Upper", "SKILL.md"), skillContent("Upper"))
	writeFile(t, filepath.Join(store, "trail-", "SKILL.md"), skillContent("trail-"))
	writeFile(t, filepath.Join(store, "-lead", "SKILL.md"), skillContent("-lead"))
	writeFile(t, filepath.Join(store, "snake_case", "SKILL.md"), skillContent("snake_case"))
	writeFile(t, filepath.Join(store, "dot.name", "SKILL.md"), skillContent("dot.name"))
	writeFile(t, filepath.Join(store, "nofront", "SKILL.md"), "# no frontmatter\n")
	// A skill nested inside a skill is bundled content, never a separate skill.
	writeFile(t, filepath.Join(store, "good", "nested", "SKILL.md"), skillContent("nested"))

	r := mustRun(t, "sync")
	if r.stdout != "sync: claude-code +3 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertAgentLinks(t, agentDir, store, "a", "also-good-1", "good")
	assertStateLinks(t, fixture, agentDir, expectedLinks(store, "a", "also-good-1", "good"))
	for _, skipped := range []string{"mismatch", "empty-dir", "Upper", "trail-", "-lead", "snake_case", "dot.name", "nofront"} {
		if !strings.Contains(r.stderr, skipped) {
			t.Errorf("stderr should warn about skipped entry %q\n%s", skipped, r)
		}
	}
}

func TestSyncFrontmatterAllowsExtraKeys(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	writeFile(t, filepath.Join(fixture.storeDir, "extra", "SKILL.md"),
		"---\nname: extra\nlicense: MIT\nmetadata:\n  anything: goes\n---\nbody\n")
	mustRun(t, "sync")
	assertAgentLinks(t, agentDir, fixture.storeDir, "extra")
}

func TestSyncReconcileRemovesStaleLink(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha", "beta", "gamma")
	mustRun(t, "sync")

	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}
	r := mustRun(t, "sync")
	if r.stdout != "sync: claude-code +0 -1\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertAgentLinks(t, agentDir, fixture.storeDir, "alpha", "gamma")
	assertStateLinks(t, fixture, agentDir, expectedLinks(fixture.storeDir, "alpha", "gamma"))
}

func TestSyncEmptyStorePrunesEverything(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha", "beta")
	mustRun(t, "sync")
	for _, name := range []string{"alpha", "beta"} {
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, name)); err != nil {
			t.Fatal(err)
		}
	}
	stateBefore := readStateBytes(t, fixture)
	refused := run("sync")
	expectCode(t, refused, exitError)
	if !strings.Contains(refused.stderr, "--allow-empty") {
		t.Fatalf("refusal should mention --allow-empty\n%s", refused)
	}
	assertAgentLinks(t, agentDir, fixture.storeDir, "alpha", "beta")
	if string(readStateBytes(t, fixture)) != string(stateBefore) {
		t.Fatal("a refused sync modified state")
	}

	r := mustRun(t, "sync", "--allow-empty")
	if r.stdout != "sync: claude-code +0 -2\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertNames(t, agentDir)
	assertStateLinks(t, fixture, agentDir, nil)
}

func TestSyncUnmanagedProtection(t *testing.T) {
	t.Run("real directory is not overwritten", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		handPlaced := filepath.Join(agentDir, "alpha")
		writeFile(t, filepath.Join(handPlaced, "SKILL.md"), "hand placed\n")

		r := mustRun(t, "sync")
		if r.stdout != "sync: claude-code +1 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		if !strings.Contains(r.stderr, "alpha") {
			t.Fatalf("expected a warning naming alpha on stderr\n%s", r)
		}
		assertRealDir(t, handPlaced)
		content, err := os.ReadFile(filepath.Join(handPlaced, "SKILL.md"))
		if err != nil || string(content) != "hand placed\n" {
			t.Fatalf("hand placed content changed: %q %v", content, err)
		}
		assertLink(t, filepath.Join(agentDir, "beta"), filepath.Join(fixture.storeDir, "beta"))
		assertStateLinks(t, fixture, agentDir, expectedLinks(fixture.storeDir, "beta"))
	})

	t.Run("unmanaged stays unrecorded across repeated sync and clean", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		handPlaced := filepath.Join(agentDir, "alpha")
		writeFile(t, filepath.Join(handPlaced, "SKILL.md"), "hand placed\n")
		mustRun(t, "sync")
		mustRun(t, "sync")
		mustRun(t, "clean")
		assertRealDir(t, handPlaced)
		assertStateLinks(t, fixture, agentDir, nil)
	})

	t.Run("foreign symlink is not overwritten", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		foreignTarget := filepath.Join(fixture.root, "elsewhere")
		mkdir(t, foreignTarget)
		mkdir(t, agentDir)
		foreignLink := filepath.Join(agentDir, "alpha")
		if err := os.Symlink(foreignTarget, foreignLink); err != nil {
			t.Fatal(err)
		}
		r := mustRun(t, "sync")
		assertLink(t, foreignLink, foreignTarget)
		assertStateLinks(t, fixture, agentDir, nil)
		if r.stdout != "sync: claude-code +0 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
	})

	t.Run("regular file at wanted name is not overwritten", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		filePath := filepath.Join(agentDir, "alpha")
		writeFile(t, filePath, "just a file")
		mustRun(t, "sync")
		content, err := os.ReadFile(filePath)
		if err != nil || string(content) != "just a file" {
			t.Fatalf("file was clobbered: %q %v", content, err)
		}
		assertStateLinks(t, fixture, agentDir, nil)
	})

	t.Run("unrelated entries in agent dir survive reconcile", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		writeFile(t, filepath.Join(agentDir, "mine", "SKILL.md"), "mine")
		mustRun(t, "sync")
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, "alpha")); err != nil {
			t.Fatal(err)
		}
		// The store is now empty, which sync refuses to prune without the flag.
		mustRun(t, "sync", "--allow-empty")
		assertNames(t, agentDir, "mine")
	})
}

func TestSyncDanglingOwnedLinkIsPruned(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha")
	mustRun(t, "sync")
	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	addSkill(t, fixture.storeDir, "beta")
	mustRun(t, "sync")
	assertAgentLinks(t, agentDir, fixture.storeDir, "beta")
}

func TestSyncDryRun(t *testing.T) {
	t.Run("fresh run writes nothing", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		r := mustRun(t, "sync", "--dry-run")
		if r.stdout != "sync: claude-code +2 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertAbsent(t, filepath.Join(agentDir, "alpha"))
		assertAbsent(t, filepath.Join(agentDir, "beta"))
		assertAbsent(t, fixture.statePath)
	})

	t.Run("with existing state reports changes but makes none", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
			t.Fatal(err)
		}
		addSkill(t, fixture.storeDir, "gamma")
		before := snapshot(t, fixture.root)
		r := mustRun(t, "sync", "--dry-run")
		if r.stdout != "sync: claude-code +1 -1\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertSnapshotsEqual(t, before, snapshot(t, fixture.root))
		assertLink(t, filepath.Join(agentDir, "beta"), filepath.Join(fixture.storeDir, "beta"))
	})

	t.Run("does not create the agent dir", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkill(t, fixture.storeDir, "alpha")
		mustRun(t, "sync", "--dry-run")
		assertAbsent(t, agentDir)
	})
}

func TestSyncMissingStoreChangesNothing(t *testing.T) {
	t.Run("default store removed after a good sync", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")
		targets := expectedLinks(fixture.storeDir, "alpha", "beta")
		if err := os.RemoveAll(fixture.storeDir); err != nil {
			t.Fatal(err)
		}
		stateBefore := readStateBytes(t, fixture)

		r := run("sync")
		if r.code == exitOK {
			t.Fatalf("expected non-zero exit for missing store\n%s", r)
		}
		if r.stderr == "" {
			t.Fatalf("expected an error on stderr\n%s", r)
		}
		assertNames(t, agentDir, "alpha", "beta")
		for name, target := range targets {
			assertLink(t, filepath.Join(agentDir, name), target)
		}
		if string(readStateBytes(t, fixture)) != string(stateBefore) {
			t.Fatal("state changed despite missing store")
		}
	})

	t.Run("--store nonexistent dir", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		mustRun(t, "sync")
		stateBefore := readStateBytes(t, fixture)
		missing := filepath.Join(fixture.root, "no-such-store")

		r := run("sync", "--store", missing)
		if r.code == exitOK {
			t.Fatalf("expected non-zero exit\n%s", r)
		}
		assertAgentLinks(t, agentDir, fixture.storeDir, "alpha")
		if string(readStateBytes(t, fixture)) != string(stateBefore) {
			t.Fatal("state changed despite missing store")
		}
		assertAbsent(t, missing)
	})

	t.Run("never-existing store creates nothing", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		r := run("sync")
		if r.code == exitOK {
			t.Fatalf("expected non-zero exit\n%s", r)
		}
		assertAbsent(t, agentDir)
		assertAbsent(t, fixture.statePath)
	})
}

func TestSyncMultipleAgents(t *testing.T) {
	fixture := newEnv(t)
	firstDir := filepath.Join(fixture.home, ".claude", "skills")
	secondDir := filepath.Join(fixture.home, ".config", "hax", "skills")
	writeAgents(t, fixture, map[string]string{
		"claude-code": "~/.claude/skills",
		"hax":         "~/.config/hax/skills",
	})
	addSkills(t, fixture.storeDir, "alpha", "beta")
	r := mustRun(t, "sync")
	if r.stdout != "sync: claude-code +2 -0   hax +2 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertAgentLinks(t, firstDir, fixture.storeDir, "alpha", "beta")
	assertAgentLinks(t, secondDir, fixture.storeDir, "alpha", "beta")
	want := expectedLinks(fixture.storeDir, "alpha", "beta")
	assertStateLinks(t, fixture, firstDir, want)
	assertStateLinks(t, fixture, secondDir, want)

	t.Run("reconcile hits both agents independently", func(t *testing.T) {
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, "alpha")); err != nil {
			t.Fatal(err)
		}
		addSkill(t, fixture.storeDir, "delta")
		writeFile(t, filepath.Join(secondDir, "delta", "SKILL.md"), "mine")
		r := mustRun(t, "sync")
		if r.stdout != "sync: claude-code +1 -1   hax +0 -1\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertAgentLinks(t, firstDir, fixture.storeDir, "beta", "delta")
		assertNames(t, secondDir, "beta", "delta")
		assertRealDir(t, filepath.Join(secondDir, "delta"))
	})
}

func TestSyncAgentConfigValues(t *testing.T) {
	t.Run("variable expansion", func(t *testing.T) {
		fixture := newEnv(t)
		t.Setenv("SM_TEST_AGENT_ROOT", filepath.Join(fixture.root, "varroot"))
		writeAgents(t, fixture, map[string]string{"custom": "$SM_TEST_AGENT_ROOT/skills"})
		addSkill(t, fixture.storeDir, "alpha")
		mustRun(t, "sync")
		assertAgentLinks(t, filepath.Join(fixture.root, "varroot", "skills"), fixture.storeDir, "alpha")
	})

	t.Run("custom unknown agent name is valid", func(t *testing.T) {
		fixture := newEnv(t)
		writeAgents(t, fixture, map[string]string{"myagent": "~/.config/myagent/skills"})
		addSkill(t, fixture.storeDir, "alpha")
		r := mustRun(t, "sync")
		if r.stdout != "sync: myagent +1 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
	})

	t.Run("relative path is a validation error", func(t *testing.T) {
		fixture := newEnv(t)
		writeAgents(t, fixture, map[string]string{"bad": "relative/skills"})
		addSkill(t, fixture.storeDir, "alpha")
		expectCode(t, run("sync"), exitError)
		assertAbsent(t, fixture.statePath)
	})

	t.Run("empty agents table succeeds", func(t *testing.T) {
		fixture := newEnv(t)
		writeFile(t, fixture.configPath, "[agents]\n")
		addSkill(t, fixture.storeDir, "alpha")
		expectCode(t, run("sync"), exitOK)
	})
}

func TestSyncVerbose(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha")
	quiet := mustRun(t, "sync")
	for _, args := range [][]string{{"-v", "sync"}, {"sync", "-v"}, {"--verbose", "sync"}, {"sync", "--verbose"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if err := os.RemoveAll(agentDir); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(fixture.statePath); err != nil {
				t.Fatal(err)
			}
			r := mustRun(t, args...)
			if r.stdout != quiet.stdout {
				t.Fatalf("verbose must not change stdout: %q vs %q", r.stdout, quiet.stdout)
			}
			if r.stderr == "" {
				t.Fatal("verbose should print a running account to stderr")
			}
			assertAgentLinks(t, agentDir, fixture.storeDir, "alpha")
		})
	}
}

func TestSyncBadFlag(t *testing.T) {
	fixture, _ := singleAgentEnv(t)
	addSkill(t, fixture.storeDir, "alpha")
	expectCode(t, run("sync", "--no-such-flag"), exitUsage)
}

func TestSyncWithoutConfig(t *testing.T) {
	fixture := newEnv(t)
	addSkill(t, fixture.storeDir, "alpha")
	r := run("sync")
	expectCode(t, r, exitError)
	if r.stderr == "" {
		t.Fatal("expected stderr explanation")
	}
}

func TestSyncDataHomeUnsetFallsBackToHomeLocalShare(t *testing.T) {
	fixture := newEnv(t)
	t.Setenv("XDG_DATA_HOME", "")
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills"})
	defaultStore := filepath.Join(fixture.home, ".local", "share", "skills")
	addSkill(t, defaultStore, "alpha")
	mustRun(t, "sync")
	assertAgentLinks(t, filepath.Join(fixture.home, ".claude", "skills"), defaultStore, "alpha")
	statePath := filepath.Join(fixture.home, ".local", "share", "skills-mapper", "state.json")
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state should be under fallback data dir: %v", err)
	}
}

// R19: two agents spelling the same directory are synced once, and the
// warning names both so the user can fix agents.toml.
func TestDoSync_AliasedAgentDirsSyncedOnce(t *testing.T) {
	fixture := newEnv(t)
	realDir := filepath.Join(fixture.home, ".claude", "skills")
	mkdir(t, realDir)
	aliasParent := filepath.Join(fixture.home, ".alias")
	if err := os.Symlink(filepath.Join(fixture.home, ".claude"), aliasParent); err != nil {
		t.Fatal(err)
	}
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "copycat": "~/.alias/skills"})
	addSkills(t, fixture.storeDir, "alpha")

	r := mustRun(t, "sync")
	if r.stdout != "sync: claude-code +1 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	if !strings.Contains(r.stderr, "agent copycat maps to the same directory as agent claude-code") {
		t.Fatalf("warning should name both agents\n%s", r)
	}
	assertAgentLinks(t, realDir, fixture.storeDir, "alpha")
	decoded := readState(t, fixture)
	if len(decoded.Targets) != 1 {
		t.Fatalf("state should key the directory once, got %v", decoded.Targets)
	}
}

func TestSyncZeroAgents(t *testing.T) {
	fixture := newEnv(t)
	writeAgents(t, fixture, map[string]string{})
	addSkills(t, fixture.storeDir, "alpha")
	r := mustRun(t, "sync")
	if r.stdout != "no agents enabled in agents.toml\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
}

// The default state directory holds which directories sm manages; an existing
// one left group/world accessible by an earlier release is made private.
func TestSyncTightensDefaultStateDir(t *testing.T) {
	fixture, _ := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha")
	stateDir := filepath.Dir(fixture.statePath)
	mkdir(t, stateDir)
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "sync")
	info, err := os.Stat(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode %v, want 0700", info.Mode().Perm())
	}
}
