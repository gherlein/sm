//go:build unix

package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// regressHoldLock takes the exclusive flock a concurrent sm would hold.
func regressHoldLock(t *testing.T, lockPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("mkdir lock dir: %v", err)
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open lock %s: %v", lockPath, err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock %s: %v", lockPath, err)
	}
	t.Cleanup(func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			t.Errorf("unlock: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Errorf("close lock: %v", err)
		}
	})
}

// R8: special files never hang sync.
func TestRegressFifoDoesNotHang(t *testing.T) {
	t.Run("fifo inside skill, copy mode", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		skillDir := filepath.Join(env.store, "alpha")
		regressSkill(t, skillDir, "alpha")
		if err := syscall.Mkfifo(filepath.Join(skillDir, "pipe"), 0o644); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		code, stdout, stderr := regressRunTimeout(t, regressTimeout, "sync", "--copy")
		regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)
		copied := filepath.Join(env.agentDir("one"), "alpha")
		regressRealDir(t, copied)
		regressAbsent(t, filepath.Join(copied, "pipe"))
		regressContains(t, "stderr", stderr, "warning")
	})
	t.Run("SKILL.md is fifo or device", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		fifoPath := filepath.Join(env.root, "fifo")
		if err := syscall.Mkfifo(fifoPath, 0o644); err != nil {
			t.Fatalf("mkfifo: %v", err)
		}
		regressSymlink(t, fifoPath, filepath.Join(env.store, "beta", "SKILL.md"))
		regressSymlink(t, "/dev/zero", filepath.Join(env.store, "gamma", "SKILL.md"))
		for _, args := range [][]string{{"sync"}, {"sync", "--copy", "--agent-home", filepath.Join(env.root, "profile")}, {"status"}} {
			code, stdout, stderr := regressRunTimeout(t, regressTimeout, args...)
			regressExpectCode(t, strings.Join(args, " "), code, regressExitOK, stdout, stderr)
			// The contract does not fix the stream status uses for store warnings.
			regressContains(t, strings.Join(args, " ")+" output", stdout+stderr, filepath.Join(env.store, "beta"))
			regressContains(t, strings.Join(args, " ")+" output", stdout+stderr, filepath.Join(env.store, "gamma"))
		}
		regressExists(t, filepath.Join(env.agentDir("one"), "alpha"))
		regressAbsent(t, filepath.Join(env.agentDir("one"), "beta"))
		regressAbsent(t, filepath.Join(env.agentDir("one"), "gamma"))
	})
}

// R13: a second concurrent sync/clean fails fast on the lock.
func TestRegressConcurrentSyncFailsFast(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	lockPath := env.statePath + ".lock"
	regressHoldLock(t, lockPath)

	for _, command := range []string{"sync", "clean"} {
		code, stdout, stderr := regressRunTimeout(t, regressTimeout, command)
		regressExpectCode(t, command, code, regressExitError, stdout, stderr)
		regressContains(t, command+" stderr", stderr, "another sm or ms process is running (lock "+lockPath+")")
		regressContains(t, command+" stderr", stderr, lockPath)
	}
	regressAbsent(t, filepath.Join(env.agentDir("one"), "alpha"))
}

// sm never takes the ms ledger lock, so an ms run holding it does not block a
// sync (ms takes the state lock first, which is what serializes the two).
func TestHardeningSyncNotBlockedByLedgerLock(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressHoldLock(t, filepath.Join(env.dataDir, "skills-mapper", "ingest-log.json.lock"))

	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync under the ledger lock", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, filepath.Join(env.agentDir("one"), "alpha")); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("link = %q", got)
	}
}
