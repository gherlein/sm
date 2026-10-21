//go:build unix

package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Black-box tests for the final fix wave (Task 18): argument safety, lock
// sharing with ms, provisioned-target spelling, clean adoption, copy-mode
// counts and per-skill failure isolation. Every test drives cli.Run only.

const (
	hardeningNoAgentsMessage   = "no agents enabled in agents.toml"
	hardeningUnreadableMode    = 0o000
	hardeningWorldWritableMode = 0o777
)

// hardeningSyncedEnv is a regress env with one agent and alpha already linked.
func hardeningSyncedEnv(t *testing.T) *regressEnv {
	t.Helper()
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "first sync", code, regressExitOK, stdout, stderr)
	return env
}

// hardeningStateTargets decodes the state file's targets with both ledgers.
func hardeningStateTargets(t *testing.T, statePath string) map[string]map[string]map[string]string {
	t.Helper()
	var decoded struct {
		Targets map[string]map[string]map[string]string `json:"targets"`
	}
	if err := json.Unmarshal([]byte(regressRead(t, statePath)), &decoded); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return decoded.Targets
}

// An explicitly empty path or agent value must never fall back to the default
// (the real agent directories, every agent): it is a usage error that changes
// nothing.
func TestHardeningEmptyFlagValuesAreUsageErrors(t *testing.T) {
	cases := []struct {
		args []string
		flag string
	}{
		{[]string{"sync", "--store", ""}, "--store"},
		{[]string{"sync", "--agent-home", ""}, "--agent-home"},
		{[]string{"sync", "--copy", "--agent-home", ""}, "--agent-home"},
		{[]string{"sync", "--store="}, "--store"},
		{[]string{"status", "--store", ""}, "--store"},
		{[]string{"status", "--agent-home", ""}, "--agent-home"},
		{[]string{"clean", "--agent", ""}, "--agent"},
		{[]string{"clean", "--agent="}, "--agent"},
		{[]string{"clean", "--agent-home", ""}, "--agent-home"},
	}
	for _, testCase := range cases {
		t.Run(strings.Join(testCase.args, " "), func(t *testing.T) {
			env := hardeningSyncedEnv(t)
			regressSkill(t, filepath.Join(env.store, "beta"), "beta")
			before := snapshot(t, env.root)
			code, stdout, stderr := regressRun(t, testCase.args...)
			regressExpectCode(t, strings.Join(testCase.args, " "), code, regressExitUsage, stdout, stderr)
			regressContains(t, "stderr", stderr, testCase.flag)
			regressContains(t, "stderr", stderr, "empty")
			assertSnapshotsEqual(t, before, snapshot(t, env.root))
		})
	}
}

// A stray positional argument must not be ignored: `sm clean a` would clean
// every agent and `sm sync now --dry-run` would stop flag parsing at "now"
// and do a real sync.
func TestHardeningStrayArgumentsAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"sync", "now", "--dry-run"},
		{"sync", "now"},
		{"status", "extra"},
		{"clean", "a"},
		{"clean", "--agent", "one", "extra"},
		{"probe", "extra"},
		{"probe", "--dry-run", "extra"},
		{"init", "extra"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			env := hardeningSyncedEnv(t)
			// An empty PATH keeps probe from detecting real agent binaries.
			emptyPath := filepath.Join(env.root, "empty-path")
			regressMkdir(t, emptyPath)
			t.Setenv("PATH", emptyPath)
			regressSkill(t, filepath.Join(env.store, "beta"), "beta")
			before := snapshot(t, env.root)
			code, stdout, stderr := regressRun(t, args...)
			regressExpectCode(t, strings.Join(args, " "), code, regressExitUsage, stdout, stderr)
			regressContains(t, "stderr", stderr, "unexpected argument")
			regressAbsent(t, filepath.Join(env.agentDir("one"), "beta"))
			assertSnapshotsEqual(t, before, snapshot(t, env.root))
		})
	}
}

// When HOME is a symlink and agents.toml spells the agent directory through
// the resolved home, a --agent-home layout is still recognized as provisioned
// and clean --orphans leaves it alone.
func TestHardeningProvisionedRecognizedUnderResolvedHome(t *testing.T) {
	env := newRegressEnv(t)
	homeLink := filepath.Join(env.root, "home-link")
	regressSymlink(t, env.home, homeLink)
	t.Setenv("HOME", homeLink)
	env.writeAgents(t, "[agents]\none = \""+filepath.Join(env.home, ".one", "skills")+"\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	profile := filepath.Join(env.root, "profile")
	regressMkdir(t, profile)

	code, stdout, stderr := regressRun(t, "sync", "--agent-home", profile)
	regressExpectCode(t, "provision", code, regressExitOK, stdout, stderr)
	provisioned := filepath.Join(profile, ".one", "skills", "alpha")
	regressExists(t, provisioned)

	code, stdout, stderr = regressRun(t, "status")
	regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
	regressContains(t, "status output", stdout, "provisioned target: "+filepath.Join(profile, ".one", "skills"))
	regressNotContains(t, "status output", stdout, "orphaned target")

	code, stdout, stderr = regressRun(t, "clean", "--orphans")
	regressExpectCode(t, "clean --orphans", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, provisioned); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("provisioned link = %q", got)
	}
}

// Respelling an agent directory (here through a symlinked parent) and then
// running clean must remove the links recorded under the old spelling.
func TestHardeningCleanAdoptsAliasRecords(t *testing.T) {
	env := newRegressEnv(t)
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)

	regressSymlink(t, filepath.Join(env.home, ".one"), filepath.Join(env.home, ".alias"))
	env.writeAgents(t, "[agents]\none = \"~/.alias/skills\"\n")
	code, stdout, stderr = regressRun(t, "clean")
	regressExpectCode(t, "clean", code, regressExitOK, stdout, stderr)
	regressContains(t, "clean summary", stdout, "clean: one -2")
	for _, name := range []string{"alpha", "beta"} {
		regressAbsent(t, filepath.Join(env.agentDir("one"), name))
	}
	if targets := hardeningStateTargets(t, env.statePath); len(targets) != 0 {
		t.Fatalf("state still records %v", targets)
	}
}

func TestHardeningCleanZeroAgentsMessage(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\n")
	code, stdout, stderr := regressRun(t, "clean")
	regressExpectCode(t, "clean", code, regressExitOK, stdout, stderr)
	if strings.TrimSpace(stdout) != hardeningNoAgentsMessage {
		t.Fatalf("clean stdout = %q, want %q", stdout, hardeningNoAgentsMessage)
	}

	code, stdout, stderr = regressRun(t, "clean", "--agent", "one")
	regressExpectCode(t, "clean --agent with no agents", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "unknown agent \"one\"")
}

// A recorded copy that is already gone is reconciled away in both runs, so
// dry run and real run print the same counts.
func TestHardeningCopyDryRunCountsMatchWhenCopyAlreadyGone(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "first", code, regressExitOK, stdout, stderr)
	for _, path := range []string{filepath.Join(env.store, "beta"), filepath.Join(env.agentDir("one"), "beta")} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
	}

	code, dryStdout, stderr := regressRun(t, "sync", "--copy", "--dry-run")
	regressExpectCode(t, "dry run", code, regressExitOK, dryStdout, stderr)
	code, realStdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "real run", code, regressExitOK, realStdout, stderr)
	regressContains(t, "dry run", dryStdout, "one +0 ~1 -1")
	if dryStdout != realStdout {
		t.Fatalf("dry run %q != real run %q", dryStdout, realStdout)
	}
	if copies := hardeningStateTargets(t, env.statePath)[env.agentDir("one")]["copies"]; len(copies) != 1 || copies["alpha"] == "" {
		t.Fatalf("copies ledger = %v, want only alpha", copies)
	}
}

// One unreadable file in one store skill must not stop the other skills or
// the other agents: they are copied, state is saved, the summary is printed
// and the command exits 1; a rerun after the fix completes the job.
func TestHardeningCopyIsolatesPerSkillFailure(t *testing.T) {
	regressRequireNonRoot(t)
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\ntwo = \"~/.two/skills\"\n")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		regressSkill(t, filepath.Join(env.store, name), name)
	}
	secret := filepath.Join(env.store, "beta", "data.txt")
	regressWrite(t, secret, "data")
	if err := os.Chmod(secret, hardeningUnreadableMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(secret, regressFileMode); err != nil {
			t.Errorf("restore mode: %v", err)
		}
	})

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync with one unreadable skill", code, regressExitError, stdout, stderr)
	regressContains(t, "summary", stdout, "one +2 ~0 -0")
	regressContains(t, "summary", stdout, "two +2 ~0 -0")
	regressContains(t, "stderr", stderr, "beta")
	regressContains(t, "stderr", stderr, "permission denied")
	targets := hardeningStateTargets(t, env.statePath)
	for _, agent := range []string{"one", "two"} {
		agentDir := env.agentDir(agent)
		for _, name := range []string{"alpha", "gamma"} {
			regressRealDir(t, filepath.Join(agentDir, name))
			regressExists(t, filepath.Join(agentDir, name, "SKILL.md"))
		}
		regressAbsent(t, filepath.Join(agentDir, "beta"))
		copies := targets[agentDir]["copies"]
		if len(copies) != 2 || copies["alpha"] == "" || copies["gamma"] == "" {
			t.Fatalf("%s copies ledger = %v, want alpha and gamma", agent, copies)
		}
	}

	if err := os.Chmod(secret, regressFileMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	code, stdout, stderr = regressRun(t, "sync", "--copy")
	regressExpectCode(t, "rerun", code, regressExitOK, stdout, stderr)
	regressContains(t, "rerun summary", stdout, "one +1 ~2 -0")
	regressContains(t, "rerun summary", stdout, "two +1 ~2 -0")
	for _, agent := range []string{"one", "two"} {
		if regressRead(t, filepath.Join(env.agentDir(agent), "beta", "data.txt")) != "data" {
			t.Fatalf("%s: beta not copied on the rerun", agent)
		}
	}
}

// In copy mode an unsafe agent directory stops the run once, before any skill
// is copied, rather than being reported as a failure of every skill.
func TestHardeningCopyUnsafeAgentDirStopsOnce(t *testing.T) {
	regressRequireNonRoot(t)
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	for _, name := range []string{"alpha", "beta"} {
		regressSkill(t, filepath.Join(env.store, name), name)
	}
	agentDir := env.agentDir("one")
	regressMkdir(t, agentDir)
	if err := os.Chmod(agentDir, hardeningWorldWritableMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy into a world-writable dir", code, regressExitError, stdout, stderr)
	if got := strings.Count(stderr, "world-writable"); got != 1 {
		t.Fatalf("refusal reported %d times, want once:\n%s", got, stderr)
	}
	if stdout != "" {
		t.Fatalf("a stopped run printed a summary: %q", stdout)
	}
	if entries := regressDirEntries(t, agentDir); len(entries) != 0 {
		t.Fatalf("agent dir written: %v", entries)
	}
}

// A recorded copy the user replaced with their own directory is not sm's to
// remove: the real run leaves it and drops the record without counting a
// removal, and the dry run must report the same numbers.
func TestHardeningCopyDryRunCountsMatchWhenCopyReplaced(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "first", code, regressExitOK, stdout, stderr)
	userCopy := filepath.Join(env.agentDir("one"), "beta")
	for _, path := range []string{filepath.Join(env.store, "beta"), userCopy} {
		if err := os.RemoveAll(path); err != nil {
			t.Fatalf("remove %s: %v", path, err)
		}
	}
	regressWrite(t, filepath.Join(userCopy, "mine.txt"), "user data")

	code, dryStdout, dryStderr := regressRun(t, "sync", "--copy", "--dry-run")
	regressExpectCode(t, "dry run", code, regressExitOK, dryStdout, dryStderr)
	code, realStdout, realStderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "real run", code, regressExitOK, realStdout, realStderr)
	regressContains(t, "dry run", dryStdout, "one +0 ~1 -0")
	if dryStdout != realStdout {
		t.Fatalf("dry run %q != real run %q", dryStdout, realStdout)
	}
	for label, output := range map[string]string{"dry run": dryStderr, "real run": realStderr} {
		regressContains(t, label+" stderr", output, userCopy+" is no longer an sm copy")
	}
	if got := regressRead(t, filepath.Join(userCopy, "mine.txt")); got != "user data" {
		t.Fatalf("user directory changed: %q", got)
	}
	if copies := hardeningStateTargets(t, env.statePath)[env.agentDir("one")]["copies"]; len(copies) != 1 || copies["alpha"] == "" {
		t.Fatalf("copies ledger = %v, want only alpha", copies)
	}
}
