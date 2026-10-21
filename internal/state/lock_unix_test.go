//go:build unix

package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testHolder is the tool name the state-lock tests lock as.
const testHolder = "sm"

// R13: two runs must never interleave their read-modify-write of state. flock
// locks belong to the open file description, so a second Lock in the same
// process (a second descriptor) conflicts exactly like a second process.
func TestLock_SecondLockFailsFast(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "skills-mapper", "state.json")
	unlock, err := Lock(statePath, testHolder)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Errorf("unlock: %v", err)
		}
	}()

	secondUnlock, err := Lock(statePath, testHolder)
	if err == nil {
		if unlockErr := secondUnlock(); unlockErr != nil {
			t.Errorf("unlock second: %v", unlockErr)
		}
		t.Fatal("second Lock succeeded while the first is held")
	}
	lockPath := statePath + ".lock"
	want := "another sm process is running (lock " + lockPath + ")"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}

	lockInfo, err := os.Stat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if lockInfo.Mode().Perm() != 0o600 {
		t.Errorf("lock file mode = %o, want 600", lockInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(lockPath))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Errorf("lock dir mode = %o, want 700", dirInfo.Mode().Perm())
	}
}

func TestLock_ReleaseAllowsRelock(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	unlock, err := Lock(statePath, testHolder)
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	if err := unlock(); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	relock, err := Lock(statePath, testHolder)
	if err != nil {
		t.Fatalf("Lock after release: %v", err)
	}
	if err := relock(); err != nil {
		t.Fatalf("unlock relock: %v", err)
	}
}

// A planted symlink at the lock path must not make sm create or lock a file
// somewhere else.
func TestLock_RefusesSymlinkedLockFile(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	elsewhere := filepath.Join(dir, "elsewhere")
	if err := os.Symlink(elsewhere, statePath+".lock"); err != nil {
		t.Fatal(err)
	}
	unlock, err := Lock(statePath, testHolder)
	if err == nil {
		if unlockErr := unlock(); unlockErr != nil {
			t.Errorf("unlock: %v", unlockErr)
		}
		t.Fatal("Lock followed a symlinked lock file")
	}
	if _, statErr := os.Lstat(elsewhere); !os.IsNotExist(statErr) {
		t.Fatalf("Lock created the symlink target: %v", statErr)
	}
}

// The contention message names the tool the lock belongs to, so an ms run
// blocked on its ledger lock is not reported as an sm process.
func TestLock_ContentionMessageNamesHolder(t *testing.T) {
	ledgerPath := filepath.Join(t.TempDir(), "ingest-log.json")
	unlock, err := Lock(ledgerPath, "ms")
	if err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Errorf("unlock: %v", err)
		}
	}()
	_, err = Lock(ledgerPath, "ms")
	want := "another ms process is running (lock " + ledgerPath + ".lock)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
}

func TestLock_EmptyHolderIsError(t *testing.T) {
	if _, err := Lock(filepath.Join(t.TempDir(), "state.json"), ""); err == nil {
		t.Fatal("Lock accepted an empty holder name")
	}
}
