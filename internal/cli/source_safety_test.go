package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const groupWritableMode os.FileMode = 0o775

// A store skill that is a symlink to the agent directory would make the copy
// recurse into its own stage without end; it must be refused promptly with
// both paths named, leaving no stage behind.
func TestSourceSafetyStoreSkillLinkedToAgentDirRefused(t *testing.T) {
	sandbox := newRegressEnv(t)
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	agentDir := sandbox.agentDir("one")
	regressSkill(t, agentDir, "foo")
	regressSkill(t, filepath.Join(sandbox.store, "alpha"), "alpha")
	regressSymlink(t, agentDir, filepath.Join(sandbox.store, "foo"))

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "overlaps")
	regressContains(t, "stderr", stderr, agentDir)
	regressContains(t, "stderr", stderr, filepath.Join(agentDir, "foo"))
	regressAbsent(t, filepath.Join(agentDir, "foo"))
	regressRealDir(t, filepath.Join(agentDir, "alpha"))
	for _, name := range regressDirEntries(t, agentDir) {
		if strings.HasPrefix(name, ".sm-stage-") {
			t.Fatalf("stage left in the agent dir: %s", name)
		}
	}
}

// sm --copy dereferences a symlinked SKILL.md so the copy always carries its
// manifest as a regular file with the same bytes.
func TestSourceSafetyCopyDereferencesLinkedSkillFile(t *testing.T) {
	sandbox := newRegressEnv(t)
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	manifest := filepath.Join(sandbox.root, "manifests", "alpha.md")
	body := "---\nname: alpha\n---\n# kept outside the store\n"
	regressWrite(t, manifest, body)
	skillDir := filepath.Join(sandbox.store, "alpha")
	regressWrite(t, filepath.Join(skillDir, "notes.txt"), "notes")
	regressSymlink(t, manifest, filepath.Join(skillDir, "SKILL.md"))

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)
	copied := filepath.Join(sandbox.agentDir("one"), "alpha", "SKILL.md")
	info, err := os.Lstat(copied)
	if err != nil {
		t.Fatalf("copy has no SKILL.md: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("copied SKILL.md mode = %v, want a regular file", info.Mode())
	}
	if got := regressRead(t, copied); got != body {
		t.Fatalf("copied SKILL.md = %q, want %q", got, body)
	}
}

// Anyone can plant or swap skills in a world-writable store, so sync refuses
// it in both modes and in a dry run; a group-writable store stays usable.
func TestSourceSafetyWorldWritableStoreRefused(t *testing.T) {
	sandbox := newRegressEnv(t)
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(sandbox.store, "alpha"), "alpha")
	if err := os.Chmod(sandbox.store, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	for _, args := range [][]string{{"sync"}, {"sync", "--copy"}, {"sync", "--dry-run"}} {
		code, stdout, stderr := regressRun(t, args...)
		regressExpectCode(t, strings.Join(args, " "), code, regressExitError, stdout, stderr)
		regressContains(t, "stderr", stderr, "store "+sandbox.store+" is world-writable; refusing")
		regressAbsent(t, filepath.Join(sandbox.agentDir("one"), "alpha"))
	}

	if err := os.Chmod(sandbox.store, groupWritableMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync with a group-writable store", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, filepath.Join(sandbox.agentDir("one"), "alpha")); got != filepath.Join(sandbox.store, "alpha") {
		t.Fatalf("alpha links to %q", got)
	}
}

// A run with several per-skill failures prints them as separate lines: each
// line is made terminal-safe, but the line breaks between them are kept.
func TestSourceSafetyJoinedErrorsKeepLineBreaks(t *testing.T) {
	regressRequireNonRoot(t)
	sandbox := newRegressEnv(t)
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	for _, name := range []string{"alpha", "beta"} {
		skillDir := filepath.Join(sandbox.store, name)
		regressSkill(t, skillDir, name)
		locked := filepath.Join(skillDir, "evil\x1b[2J.txt")
		regressWrite(t, locked, "unreadable")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(locked, regressFileMode); err != nil {
				t.Errorf("restore mode: %v", err)
			}
		})
	}

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitError, stdout, stderr)
	lines := strings.Split(strings.TrimSuffix(stderr, "\n"), "\n")
	var failureLines []string
	for _, line := range lines {
		for _, character := range line {
			if character < ' ' || character == '\x7f' {
				t.Fatalf("stderr line carries raw control character %U: %q", character, line)
			}
		}
		if strings.Contains(line, "agent one: ") {
			failureLines = append(failureLines, line)
		}
	}
	if len(failureLines) != 2 || !strings.HasPrefix(failureLines[0], "sm: sync failed: agent one: alpha: ") || !strings.HasPrefix(failureLines[1], "agent one: beta: ") {
		t.Fatalf("want one line per failed skill, got:\n%s", stderr)
	}
	regressContains(t, "stderr", stderr, `evil\x1b[2J.txt`)
}

// clean refusing an unsafe agent directory says how to fix it and how to
// clean the other agents meanwhile.
func TestSourceSafetyCleanUnsafeAgentDirHints(t *testing.T) {
	sandbox := newRegressEnv(t)
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\ntwo = \"~/.two/skills\"\n")
	regressSkill(t, filepath.Join(sandbox.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
	openDir := sandbox.agentDir("one")
	if err := os.Chmod(openDir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	code, stdout, stderr = regressRun(t, "clean")
	regressExpectCode(t, "clean", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "refusing target dir "+openDir+": world-writable (fix with: chmod o-w '"+openDir+"')")
	regressContains(t, "stderr", stderr, "to clean the other agents meanwhile, run: sm clean --agent two")

	// Scoped to the refused agent, there is nothing else to suggest.
	code, stdout, stderr = regressRun(t, "clean", "--agent", "one")
	regressExpectCode(t, "clean --agent one", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "fix with: chmod o-w '"+openDir+"'")
	regressNotContains(t, "stderr", stderr, "other agents")

	// With only the refused agent configured, there is no other agent either.
	sandbox.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	code, stdout, stderr = regressRun(t, "clean")
	regressExpectCode(t, "clean with one agent", code, regressExitError, stdout, stderr)
	regressNotContains(t, "stderr", stderr, "other agents")
	if got := regressReadlink(t, filepath.Join(openDir, "alpha")); got != filepath.Join(sandbox.store, "alpha") {
		t.Fatalf("link in the refused dir changed: %q", got)
	}
}
