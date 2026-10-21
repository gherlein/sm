//go:build unix

package mscli_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Contract 4: a FIFO inside a source skill is skipped, never read.
func TestRegressIngestFifoSkipped(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
	if err := syscall.Mkfifo(filepath.Join(source, "alpha", "pipe"), 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	fifoSkill := filepath.Join(env.root, "fifo")
	if err := syscall.Mkfifo(fifoSkill, 0o644); err != nil {
		t.Fatalf("mkfifo: %v", err)
	}
	regressSymlink(t, fifoSkill, filepath.Join(source, "beta", "SKILL.md"))
	dest := filepath.Join(env.root, "dest")
	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	regressExists(t, filepath.Join(dest, "alpha", "SKILL.md"))
	regressAbsent(t, filepath.Join(dest, "alpha", "pipe"))
	regressAbsent(t, filepath.Join(dest, "beta"))
	regressContains(t, "stderr", stderr, "warning")
}

// R13: a second concurrent ms fails fast on the ledger lock.
func TestRegressConcurrentIngestFailsFast(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
	lockPath := env.ledgerPath + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("open lock: %v", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("flock: %v", err)
	}
	t.Cleanup(func() {
		if err := syscall.Flock(int(file.Fd()), syscall.LOCK_UN); err != nil {
			t.Errorf("unlock: %v", err)
		}
		if err := file.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	code, stdout, stderr := regressRun(t, source)
	regressExpectCode(t, "ms", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "another ms process is running")
	regressContains(t, "stderr", stderr, lockPath)
	regressAbsent(t, filepath.Join(env.store, "alpha"))
}
