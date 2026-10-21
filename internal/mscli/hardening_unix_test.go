//go:build unix

package mscli_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// hardeningHoldLock takes the exclusive flock a concurrent sm or ms would hold.
func hardeningHoldLock(t *testing.T, lockPath string) {
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

// ms takes the sm state lock before writing, so a running sm (holding it)
// makes ms fail fast with nothing written.
func TestHardeningIngestBlockedBySmStateLock(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
	lockPath := filepath.Join(env.dataDir, "skills-mapper", "state.json.lock")
	hardeningHoldLock(t, lockPath)

	code, stdout, stderr := regressRun(t, source)
	regressExpectCode(t, "ms under the state lock", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "another sm or ms process is running (lock "+lockPath+")")
	regressContains(t, "stderr", stderr, lockPath)
	regressAbsent(t, filepath.Join(env.store, "alpha"))
	regressAbsent(t, env.ledgerPath)

	// A dry run writes nothing and takes no lock.
	code, stdout, stderr = regressRun(t, "--dry-run", source)
	regressExpectCode(t, "ms --dry-run under the state lock", code, regressExitOK, stdout, stderr)
	regressContains(t, "dry-run summary", stdout, "ms: +1 (skipped 0)")
}

// The ledger lock is ms's own, and its contention message names ms.
func TestHardeningLedgerLockMessageNamesMs(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
	lockPath := env.ledgerPath + ".lock"
	hardeningHoldLock(t, lockPath)

	code, stdout, stderr := regressRun(t, source)
	regressExpectCode(t, "ms under the ledger lock", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "another ms process is running (lock "+lockPath+")")
	regressAbsent(t, filepath.Join(env.store, "alpha"))
}
