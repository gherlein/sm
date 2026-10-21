package cli_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestSyncStoreOverride(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "default-one", "default-two")
	altStore := filepath.Join(fixture.root, "alt-store")
	addSkills(t, altStore, "alt-one")

	r := mustRun(t, "sync", "--store", altStore)
	if r.stdout != "sync: claude-code +1 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertAgentLinks(t, agentDir, altStore, "alt-one")
	assertStateLinks(t, fixture, agentDir, expectedLinks(altStore, "alt-one"))

	t.Run("tilde expands against HOME", func(t *testing.T) {
		tildeStore := filepath.Join(fixture.home, "tilde-store")
		addSkills(t, tildeStore, "tilde-one")
		mustRun(t, "sync", "--store", "~/tilde-store")
		assertAgentLinks(t, agentDir, tildeStore, "tilde-one")
	})

	t.Run("flag=value form", func(t *testing.T) {
		mustRun(t, "sync", "--store="+altStore)
		assertAgentLinks(t, agentDir, altStore, "alt-one")
	})
}

func TestSyncAgentHomeOverride(t *testing.T) {
	fixture := newEnv(t)
	writeAgents(t, fixture, map[string]string{
		"claude-code": "~/.claude/skills",
		"custom":      "$HOME/.config/custom/skills",
	})
	addSkills(t, fixture.storeDir, "alpha", "beta")
	altHome := filepath.Join(fixture.root, "alt-home")
	mkdir(t, altHome)

	r := mustRun(t, "sync", "--agent-home", altHome)
	if r.stdout != "sync: claude-code +2 -0   custom +2 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	claudeDir := filepath.Join(altHome, ".claude", "skills")
	customDir := filepath.Join(altHome, ".config", "custom", "skills")
	assertAgentLinks(t, claudeDir, fixture.storeDir, "alpha", "beta")
	assertAgentLinks(t, customDir, fixture.storeDir, "alpha", "beta")
	assertAbsent(t, filepath.Join(fixture.home, ".claude"))
	assertAbsent(t, filepath.Join(fixture.home, ".config", "custom"))
	assertStateLinks(t, fixture, claudeDir, expectedLinks(fixture.storeDir, "alpha", "beta"))
	assertStateLinks(t, fixture, customDir, expectedLinks(fixture.storeDir, "alpha", "beta"))
	if _, present := readState(t, fixture).Targets[filepath.Join(fixture.home, ".claude", "skills")]; present {
		t.Fatal("state must not record the real-home dir for a re-rooted run")
	}

	t.Run("dry-run writes nothing under the alternate home", func(t *testing.T) {
		freshHome := filepath.Join(fixture.root, "fresh-home")
		mkdir(t, freshHome)
		r := mustRun(t, "sync", "--dry-run", "--agent-home", freshHome)
		if r.stdout != "sync: claude-code +2 -0   custom +2 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertNames(t, freshHome)
	})

	t.Run("nonexistent alternate home is created or reported, never links elsewhere", func(t *testing.T) {
		newHome := filepath.Join(fixture.root, "not-yet")
		r := run("sync", "--agent-home", newHome)
		if r.code == exitOK {
			assertAgentLinks(t, filepath.Join(newHome, ".claude", "skills"), fixture.storeDir, "alpha", "beta")
		}
		assertAbsent(t, filepath.Join(fixture.home, ".claude"))
	})
}

func TestSyncAgentHomeRejectsAgentOutsideHome(t *testing.T) {
	fixture := newEnv(t)
	outside := filepath.Join(fixture.root, "outside", "skills")
	writeAgents(t, fixture, map[string]string{
		"inside":  "~/.claude/skills",
		"outside": outside,
	})
	addSkill(t, fixture.storeDir, "alpha")
	altHome := filepath.Join(fixture.root, "alt-home")
	mkdir(t, altHome)

	r := run("sync", "--agent-home", altHome)
	if r.code == exitOK {
		t.Fatalf("expected non-zero exit for agent outside HOME\n%s", r)
	}
	if r.stderr == "" {
		t.Fatalf("expected error on stderr\n%s", r)
	}
	assertAbsent(t, outside)
	assertAbsent(t, filepath.Join(fixture.home, ".claude"))
	assertNames(t, altHome)

	t.Run("without the flag the outside agent syncs fine", func(t *testing.T) {
		mustRun(t, "sync")
		assertAgentLinks(t, outside, fixture.storeDir, "alpha")
	})

	t.Run("status and clean also reject it", func(t *testing.T) {
		if r := run("status", "--agent-home", altHome); r.code == exitOK {
			t.Fatalf("status should fail\n%s", r)
		}
		if r := run("clean", "--agent-home", altHome); r.code == exitOK {
			t.Fatalf("clean should fail\n%s", r)
		}
		assertLink(t, filepath.Join(outside, "alpha"), filepath.Join(fixture.storeDir, "alpha"))
	})
}

func TestSyncStoreAndAgentHomeTogether(t *testing.T) {
	fixture, _ := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "default-one")
	altStore := filepath.Join(fixture.root, "alt-store")
	addSkills(t, altStore, "alt-one", "alt-two")
	altHome := filepath.Join(fixture.root, "alt-home")
	mkdir(t, altHome)

	r := mustRun(t, "sync", "--store", altStore, "--agent-home", altHome)
	if r.stdout != "sync: claude-code +2 -0\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	altAgentDir := filepath.Join(altHome, ".claude", "skills")
	assertAgentLinks(t, altAgentDir, altStore, "alt-one", "alt-two")
	assertAbsent(t, filepath.Join(fixture.home, ".claude"))
	assertStateLinks(t, fixture, altAgentDir, expectedLinks(altStore, "alt-one", "alt-two"))
}

func TestIndependentTrackingAndCleanAgentHome(t *testing.T) {
	fixture, defaultDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha", "beta")
	altHome := filepath.Join(fixture.root, "alt-home")
	mkdir(t, altHome)
	altDir := filepath.Join(altHome, ".claude", "skills")

	mustRun(t, "sync")
	mustRun(t, "sync", "--agent-home", altHome)
	assertAgentLinks(t, defaultDir, fixture.storeDir, "alpha", "beta")
	assertAgentLinks(t, altDir, fixture.storeDir, "alpha", "beta")

	r := mustRun(t, "clean", "--agent-home", altHome)
	if r.stdout != "clean: claude-code -2\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertNames(t, altDir)
	assertAgentLinks(t, defaultDir, fixture.storeDir, "alpha", "beta")
	assertStateLinks(t, fixture, altDir, nil)
	assertStateLinks(t, fixture, defaultDir, expectedLinks(fixture.storeDir, "alpha", "beta"))

	t.Run("a further plain sync does not resurrect alt home", func(t *testing.T) {
		mustRun(t, "sync")
		assertNames(t, altDir)
	})
}

func TestStatus(t *testing.T) {
	t.Run("empty everything", func(t *testing.T) {
		fixture, _ := singleAgentEnv(t)
		mkdir(t, fixture.storeDir)
		r := mustRun(t, "status")
		storeCount, perAgent := parseStatus(t, r.stdout)
		if storeCount != 0 {
			t.Fatalf("store count: %d\n%s", storeCount, r)
		}
		if perAgent["claude-code"] != (statusCounts{}) {
			t.Fatalf("counts: %+v\n%s", perAgent["claude-code"], r)
		}
	})

	t.Run("one of each category and read-only", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "owned-a", "owned-b", "unmanaged-d", "dangling-e")
		writeFile(t, filepath.Join(agentDir, "unmanaged-d", "SKILL.md"), "hand placed")
		mustRun(t, "sync")
		// dangling-e stays an owned link whose store skill is gone.
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, "dangling-e")); err != nil {
			t.Fatal(err)
		}
		addSkill(t, fixture.storeDir, "missing-c")

		before := snapshot(t, fixture.root)
		stateBefore := readStateBytes(t, fixture)
		r := mustRun(t, "status")
		storeCount, perAgent := parseStatus(t, r.stdout)
		if storeCount != 4 {
			t.Fatalf("store count: %d\n%s", storeCount, r)
		}
		want := statusCounts{owned: 2, missing: 1, unmanaged: 1, dangling: 1}
		if perAgent["claude-code"] != want {
			t.Fatalf("counts: got %+v want %+v\n%s", perAgent["claude-code"], want, r)
		}
		if !strings.Contains(r.stdout, agentDir) {
			t.Fatalf("status should show the agent dir %s\n%s", agentDir, r)
		}
		assertSnapshotsEqual(t, before, snapshot(t, fixture.root))
		if string(readStateBytes(t, fixture)) != string(stateBefore) {
			t.Fatal("status modified state")
		}
	})

	t.Run("before any sync no state file is created", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		r := mustRun(t, "status")
		storeCount, perAgent := parseStatus(t, r.stdout)
		if storeCount != 2 {
			t.Fatalf("store count: %d\n%s", storeCount, r)
		}
		if perAgent["claude-code"] != (statusCounts{missing: 2}) {
			t.Fatalf("counts: %+v\n%s", perAgent["claude-code"], r)
		}
		assertAbsent(t, fixture.statePath)
		assertAbsent(t, agentDir)
	})

	t.Run("dangling owned link after store skill removed", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
			t.Fatal(err)
		}
		before := snapshot(t, fixture.root)
		r := mustRun(t, "status")
		_, perAgent := parseStatus(t, r.stdout)
		counts := perAgent["claude-code"]
		if counts.dangling != 1 || counts.missing != 0 || counts.unmanaged != 0 {
			t.Fatalf("counts: %+v\n%s", counts, r)
		}
		assertSnapshotsEqual(t, before, snapshot(t, fixture.root))
		assertLink(t, filepath.Join(agentDir, "beta"), filepath.Join(fixture.storeDir, "beta"))
	})

	t.Run("multiple agents reported separately", func(t *testing.T) {
		fixture := newEnv(t)
		writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "hax": "~/.config/hax/skills"})
		addSkills(t, fixture.storeDir, "alpha")
		mustRun(t, "sync")
		if err := os.Remove(filepath.Join(fixture.home, ".config", "hax", "skills", "alpha")); err != nil {
			t.Fatal(err)
		}
		_, perAgent := parseStatus(t, mustRun(t, "status").stdout)
		if perAgent["claude-code"] != (statusCounts{owned: 1}) {
			t.Fatalf("claude-code: %+v", perAgent["claude-code"])
		}
		if perAgent["hax"].missing != 1 || perAgent["hax"].dangling != 0 {
			t.Fatalf("hax: %+v", perAgent["hax"])
		}
	})

	t.Run("--store override", func(t *testing.T) {
		fixture, _ := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "one")
		altStore := filepath.Join(fixture.root, "alt-store")
		addSkills(t, altStore, "x", "y", "z")
		r := mustRun(t, "status", "--store", altStore)
		storeCount, perAgent := parseStatus(t, r.stdout)
		if storeCount != 3 {
			t.Fatalf("store count %d\n%s", storeCount, r)
		}
		if perAgent["claude-code"].missing != 3 {
			t.Fatalf("counts %+v\n%s", perAgent["claude-code"], r)
		}
		if !strings.Contains(r.stdout, altStore) {
			t.Fatalf("status should mention alt store path\n%s", r)
		}
	})

	t.Run("--agent-home override", func(t *testing.T) {
		fixture, _ := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		altHome := filepath.Join(fixture.root, "alt-home")
		mkdir(t, altHome)
		altDir := filepath.Join(altHome, ".claude", "skills")

		before := parseCounts(t, mustRun(t, "status", "--agent-home", altHome))
		if before != (statusCounts{missing: 2}) {
			t.Fatalf("pre-sync alt counts: %+v", before)
		}
		mustRun(t, "sync", "--agent-home", altHome)

		snapshotBefore := snapshot(t, fixture.root)
		r := mustRun(t, "status", "--agent-home", altHome)
		if got := parseCounts(t, r); got != (statusCounts{owned: 2}) {
			t.Fatalf("alt counts: %+v\n%s", got, r)
		}
		if !strings.Contains(r.stdout, altDir) {
			t.Fatalf("status should mention %s\n%s", altDir, r)
		}
		if got := parseCounts(t, mustRun(t, "status")); got != (statusCounts{missing: 2}) {
			t.Fatalf("default home counts: %+v", got)
		}
		assertSnapshotsEqual(t, snapshotBefore, snapshot(t, fixture.root))
	})
}

func parseCounts(t *testing.T, r result) statusCounts {
	t.Helper()
	_, perAgent := parseStatus(t, r.stdout)
	return perAgent["claude-code"]
}

func TestClean(t *testing.T) {
	t.Run("removes all owned links and clears state", func(t *testing.T) {
		fixture := newEnv(t)
		claudeDir := filepath.Join(fixture.home, ".claude", "skills")
		haxDir := filepath.Join(fixture.home, ".config", "hax", "skills")
		writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "hax": "~/.config/hax/skills"})
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")
		writeFile(t, filepath.Join(claudeDir, "mine", "SKILL.md"), "mine")

		r := mustRun(t, "clean")
		if r.stdout != "clean: claude-code -2   hax -2\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertNames(t, claudeDir, "mine")
		assertNames(t, haxDir)
		assertStateLinks(t, fixture, claudeDir, nil)
		assertStateLinks(t, fixture, haxDir, nil)
		assertNames(t, fixture.storeDir, "alpha", "beta")
		assertAbsent(t, filepath.Join(fixture.storeDir, "alpha", "gone"))
		content, err := os.ReadFile(filepath.Join(fixture.storeDir, "alpha", "SKILL.md"))
		if err != nil || string(content) != skillContent("alpha") {
			t.Fatalf("store content damaged: %q %v", content, err)
		}
	})

	t.Run("--agent scopes to one agent", func(t *testing.T) {
		fixture := newEnv(t)
		claudeDir := filepath.Join(fixture.home, ".claude", "skills")
		haxDir := filepath.Join(fixture.home, ".config", "hax", "skills")
		writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "hax": "~/.config/hax/skills"})
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")

		r := mustRun(t, "clean", "--agent", "hax")
		if r.stdout != "clean: hax -2\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
		assertNames(t, haxDir)
		assertAgentLinks(t, claudeDir, fixture.storeDir, "alpha", "beta")
		assertStateLinks(t, fixture, haxDir, nil)
		assertStateLinks(t, fixture, claudeDir, expectedLinks(fixture.storeDir, "alpha", "beta"))
	})

	t.Run("real dir at recorded name is left intact and record dropped", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha", "beta")
		mustRun(t, "sync")
		replaced := filepath.Join(agentDir, "alpha")
		if err := os.Remove(replaced); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(replaced, "SKILL.md"), "replaced by hand")

		r := mustRun(t, "clean")
		if r.code != exitOK {
			t.Fatalf("clean must not abort\n%s", r)
		}
		assertRealDir(t, replaced)
		content, err := os.ReadFile(filepath.Join(replaced, "SKILL.md"))
		if err != nil || string(content) != "replaced by hand" {
			t.Fatalf("real dir content changed: %q %v", content, err)
		}
		assertAbsent(t, filepath.Join(agentDir, "beta"))
		assertStateLinks(t, fixture, agentDir, nil)
	})

	t.Run("is idempotent", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		mustRun(t, "sync")
		mustRun(t, "clean")
		r := mustRun(t, "clean")
		if r.code != exitOK {
			t.Fatalf("second clean failed\n%s", r)
		}
		assertNames(t, agentDir)
	})

	t.Run("without prior sync is harmless", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		writeFile(t, filepath.Join(agentDir, "mine", "SKILL.md"), "mine")
		expectCode(t, run("clean"), exitOK)
		assertNames(t, agentDir, "mine")
	})

	t.Run("does not remove a foreign symlink with the same name", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkills(t, fixture.storeDir, "alpha")
		mkdir(t, agentDir)
		foreignTarget := filepath.Join(fixture.root, "elsewhere")
		mkdir(t, foreignTarget)
		if err := os.Symlink(foreignTarget, filepath.Join(agentDir, "alpha")); err != nil {
			t.Fatal(err)
		}
		mustRun(t, "sync")
		mustRun(t, "clean")
		assertLink(t, filepath.Join(agentDir, "alpha"), foreignTarget)
	})
}

func TestProbe(t *testing.T) {
	t.Run("yes adds detected agent and round-trips through sync", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		addSkill(t, fixture.storeDir, "alpha")

		r := mustRun(t, "probe", "--yes")
		if !strings.Contains(r.stdout, "claude-code") {
			t.Fatalf("stdout should mention the added agent\n%s", r)
		}
		content, err := os.ReadFile(fixture.configPath)
		if err != nil {
			t.Fatal(err)
		}
		linePattern := regexp.MustCompile(`(?m)^claude-code\s*=\s*"(~|` + regexp.QuoteMeta(fixture.home) + `)/\.claude/skills"$`)
		if !linePattern.Match(content) {
			t.Fatalf("agents.toml lacks the claude-code entry:\n%s", content)
		}
		for _, absent := range []string{"copilot", "hax", "pi", "oh-my-pi", "kit"} {
			if strings.Contains(string(content), absent) {
				t.Errorf("undetected agent %q must not be added:\n%s", absent, content)
			}
		}

		syncResult := mustRun(t, "sync")
		if syncResult.stdout != "sync: claude-code +1 -0\n" {
			t.Fatalf("summary mismatch\n%s", syncResult)
		}
		assertAgentLinks(t, filepath.Join(fixture.home, ".claude", "skills"), fixture.storeDir, "alpha")
	})

	t.Run("dry-run detects but writes nothing", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		r := mustRun(t, "probe", "--dry-run")
		if !strings.Contains(r.stdout, "claude-code") {
			t.Fatalf("dry-run should name claude-code\n%s", r)
		}
		assertAbsent(t, fixture.configPath)
	})

	t.Run("dry-run does not modify an existing config", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		original := "[agents]\nmine = \"~/.mine/skills\"\n"
		writeFile(t, fixture.configPath, original)
		mustRun(t, "probe", "--dry-run")
		content, _ := os.ReadFile(fixture.configPath)
		if string(content) != original {
			t.Fatalf("config changed: %q", content)
		}
	})

	t.Run("adds only, never removes or rewrites existing entries", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		writeFile(t, fixture.configPath, "[agents]\nmine = \"~/.mine/skills\"\n")
		mustRun(t, "probe", "--yes")
		content, _ := os.ReadFile(fixture.configPath)
		if !strings.Contains(string(content), "mine") || !strings.Contains(string(content), "claude-code") {
			t.Fatalf("unexpected config:\n%s", content)
		}
		addSkill(t, fixture.storeDir, "alpha")
		r := mustRun(t, "sync")
		if r.stdout != "sync: claude-code +1 -0   mine +1 -0\n" {
			t.Fatalf("summary mismatch\n%s", r)
		}
	})

	t.Run("already-listed agent is not added twice", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		mustRun(t, "probe", "--yes")
		first, _ := os.ReadFile(fixture.configPath)
		r := mustRun(t, "probe", "--yes")
		second, _ := os.ReadFile(fixture.configPath)
		if string(first) != string(second) {
			t.Fatalf("second probe changed config:\n%s\n---\n%s\n%s", first, second, r)
		}
	})

	t.Run("nothing detected writes no agents", func(t *testing.T) {
		fixture := newEnv(t)
		expectCode(t, run("probe", "--yes"), exitOK)
		if content, err := os.ReadFile(fixture.configPath); err == nil && strings.Contains(string(content), "=") {
			t.Fatalf("no agent should be added:\n%s", content)
		}
	})

	t.Run("probe never touches store or agent skills", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		addSkill(t, fixture.storeDir, "alpha")
		storeBefore := snapshot(t, fixture.storeDir)
		mustRun(t, "probe", "--yes")
		assertSnapshotsEqual(t, storeBefore, snapshot(t, fixture.storeDir))
		assertAbsent(t, filepath.Join(fixture.home, ".claude", "skills"))
	})

	t.Run("detects multiple known agents by config dir", func(t *testing.T) {
		fixture := newEnv(t)
		mkdir(t, filepath.Join(fixture.home, ".claude"))
		mkdir(t, filepath.Join(fixture.home, ".copilot"))
		mkdir(t, filepath.Join(fixture.home, ".pi-go"))
		mustRun(t, "probe", "--yes")
		content, _ := os.ReadFile(fixture.configPath)
		for _, want := range []string{"claude-code", "copilot", "pi "} {
			if !strings.Contains(string(content), want) && !strings.Contains(string(content), strings.TrimSpace(want)+"=") {
				t.Errorf("missing %q in:\n%s", want, content)
			}
		}
	})
}

func TestUsageAndErrors(t *testing.T) {
	t.Run("no args", func(t *testing.T) {
		newEnv(t)
		r := run()
		expectCode(t, r, exitUsage)
		if !strings.Contains(r.stderr, "usage") || r.stdout != "" {
			t.Fatalf("expected usage on stderr only\n%s", r)
		}
	})

	t.Run("only -v is still no command", func(t *testing.T) {
		newEnv(t)
		r := run("-v")
		expectCode(t, r, exitUsage)
		if !strings.Contains(r.stderr, "usage") {
			t.Fatalf("expected usage\n%s", r)
		}
	})

	t.Run("unknown command", func(t *testing.T) {
		newEnv(t)
		r := run("frobnicate")
		expectCode(t, r, exitUsage)
		if !strings.Contains(r.stderr, "usage") || !strings.Contains(r.stderr, "frobnicate") {
			t.Fatalf("expected usage naming the command\n%s", r)
		}
	})

	t.Run("-h", func(t *testing.T) {
		newEnv(t)
		r := run("-h")
		expectCode(t, r, exitOK)
		if !strings.Contains(r.stdout, "usage") || r.stderr != "" {
			t.Fatalf("expected usage on stdout only\n%s", r)
		}
	})

	t.Run("usage lists every command", func(t *testing.T) {
		newEnv(t)
		r := run()
		for _, command := range []string{"sync", "init", "status", "clean", "probe"} {
			if !strings.Contains(r.stderr, command) {
				t.Errorf("usage lacks %q\n%s", command, r.stderr)
			}
		}
	})

	malformedConfigs := map[string]string{
		"garbage":          "this is [[ not toml ===",
		"agents not a map": "agents = 5\n",
		"value not string": "[agents]\nfoo = 7\n",
		"unterminated":     "[agents]\nfoo = \"~/x\n",
		"relative value":   "[agents]\nfoo = \"rel/skills\"\n",
		"empty value":      "[agents]\nfoo = \"\"\n",
	}
	for label, content := range malformedConfigs {
		for _, command := range []string{"sync", "status", "clean"} {
			t.Run(command+" with "+label, func(t *testing.T) {
				fixture := newEnv(t)
				addSkill(t, fixture.storeDir, "alpha")
				writeFile(t, fixture.configPath, content)
				before := snapshot(t, fixture.root)
				r := run(command)
				expectCode(t, r, exitError)
				if r.stderr == "" {
					t.Fatalf("expected explanation on stderr\n%s", r)
				}
				assertSnapshotsEqual(t, before, snapshot(t, fixture.root))
			})
		}
	}

	for _, command := range []string{"status", "clean"} {
		t.Run(command+" without config", func(t *testing.T) {
			fixture := newEnv(t)
			addSkill(t, fixture.storeDir, "alpha")
			expectCode(t, run(command), exitError)
		})
	}

	t.Run("-v accepted before and after the subcommand", func(t *testing.T) {
		fixture, _ := singleAgentEnv(t)
		addSkill(t, fixture.storeDir, "alpha")
		for _, args := range [][]string{
			{"-v", "status"}, {"status", "-v"}, {"--verbose", "status"}, {"status", "--verbose"},
			{"-v", "clean"}, {"clean", "-v"},
		} {
			if r := run(args...); r.code != exitOK {
				t.Errorf("%v: %s", args, r)
			}
		}
		expectCode(t, run("-v", "init"), exitError)
	})

	t.Run("-v accepted between flags", func(t *testing.T) {
		fixture, agentDir := singleAgentEnv(t)
		addSkill(t, fixture.storeDir, "alpha")
		mustRun(t, "sync", "--dry-run", "-v")
		assertAbsent(t, agentDir)
		mustRun(t, "sync", "-v", "--dry-run")
		assertAbsent(t, agentDir)
	})
}

// R14: --orphans acts on targets no configured agent maps, which is
// meaningless once --agent or --agent-home narrows or re-roots the agents.
func TestDoClean_OrphansRejectsAgentFlags(t *testing.T) {
	fixture, agentDir := singleAgentEnv(t)
	addSkills(t, fixture.storeDir, "alpha")
	mustRun(t, "sync")
	for _, args := range [][]string{
		{"clean", "--orphans", "--agent", "claude-code"},
		{"clean", "--orphans", "--agent-home", fixture.home},
	} {
		r := run(args...)
		expectCode(t, r, exitUsage)
		if !strings.Contains(r.stderr, "--orphans") {
			t.Fatalf("usage error should name --orphans\n%s", r)
		}
	}
	assertAgentLinks(t, agentDir, fixture.storeDir, "alpha")
}

func TestCleanOrphansEndToEnd(t *testing.T) {
	fixture := newEnv(t)
	claudeDir := filepath.Join(fixture.home, ".claude", "skills")
	haxDir := filepath.Join(fixture.home, ".config", "hax", "skills")
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "hax": "~/.config/hax/skills"})
	addSkills(t, fixture.storeDir, "alpha")
	mustRun(t, "sync")
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills"})

	status := mustRun(t, "status")
	if !strings.Contains(status.stdout, "orphaned target: "+haxDir+" (1 entries)\n") {
		t.Fatalf("status should list the orphaned target\n%s", status)
	}
	r := mustRun(t, "clean", "--orphans")
	if r.stdout != "clean: "+haxDir+" -1\n" {
		t.Fatalf("summary mismatch\n%s", r)
	}
	assertNames(t, haxDir)
	assertAgentLinks(t, claudeDir, fixture.storeDir, "alpha")
	if _, present := readState(t, fixture).Targets[haxDir]; present {
		t.Fatal("orphaned target still recorded")
	}
	if strings.Contains(mustRun(t, "status").stdout, "orphaned target") {
		t.Fatal("orphan still listed after clean --orphans")
	}
}

// An agent collapsed into another because both name one directory has no
// records of its own; clean --agent must point the user at the owner.
func TestCleanCollapsedAgentNamesOwner(t *testing.T) {
	fixture := newEnv(t)
	mkdir(t, filepath.Join(fixture.home, ".claude", "skills"))
	if err := os.Symlink(filepath.Join(fixture.home, ".claude"), filepath.Join(fixture.home, ".alias")); err != nil {
		t.Fatal(err)
	}
	writeAgents(t, fixture, map[string]string{"claude-code": "~/.claude/skills", "copycat": "~/.alias/skills"})
	addSkills(t, fixture.storeDir, "alpha")
	mustRun(t, "sync")

	r := run("clean", "--agent", "copycat")
	expectCode(t, r, exitError)
	if !strings.Contains(r.stderr, "agent copycat shares a directory with agent claude-code; use --agent claude-code") {
		t.Fatalf("message should name the owning agent\n%s", r)
	}
	if strings.Contains(r.stderr, "unknown agent") {
		t.Fatalf("collapsed agent reported as unknown\n%s", r)
	}
	assertAgentLinks(t, filepath.Join(fixture.home, ".claude", "skills"), fixture.storeDir, "alpha")
}
