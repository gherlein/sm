package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/state"
)

func assertModeSwitchWarning(t *testing.T, log string) {
	t.Helper()
	if !strings.Contains(log, "sm clean") {
		t.Errorf("warning lacks 'sm clean': %q", log)
	}
	if !strings.Contains(log, "managed by sm") {
		t.Errorf("warning does not say entry is managed by sm: %q", log)
	}
	if strings.Contains(log, "not managed by sm") {
		t.Errorf("warning reads as foreign-entry message: %q", log)
	}
}

func TestDoSync_ModeSwitchSymlinkThenCopyWarns(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var firstLog bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &firstLog); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(fixture.agentDir, "alpha")
	if !isSymlink(t, entry) {
		t.Fatal("setup: alpha should be a symlink")
	}

	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatalf("copy sync: %v", err)
	}
	assertModeSwitchWarning(t, log.String())
	if !isSymlink(t, entry) {
		t.Error("alpha was replaced; want it left as a symlink")
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := saved.Targets[fixture.agentDir].Copies["alpha"]; present {
		t.Errorf("alpha recorded in Copies ledger: %v", saved.Targets[fixture.agentDir].Copies)
	}
}

func TestDoSync_ModeSwitchCopyThenSymlinkWarns(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var firstLog bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &firstLog); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(fixture.agentDir, "alpha")

	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("symlink sync: %v", err)
	}
	assertModeSwitchWarning(t, log.String())
	if isSymlink(t, entry) {
		t.Error("alpha was replaced by a symlink; want the copy left intact")
	}
	if _, err := os.Stat(filepath.Join(entry, copyMarkerName)); err != nil {
		t.Errorf("copy marker lost: %v", err)
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := saved.Targets[fixture.agentDir].Links["alpha"]; present {
		t.Errorf("alpha recorded in Links ledger: %v", saved.Targets[fixture.agentDir].Links)
	}
}

func TestDoSync_CopyDryRunReportsRefreshCountWithoutWriting(t *testing.T) {
	for _, names := range [][]string{{"alpha"}, {"alpha", "beta", "gamma"}} {
		fixture := newSyncFixture(t, names...)
		var log bytes.Buffer
		if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
			t.Fatal(err)
		}
		// A sentinel inside each copy would be erased by a real refresh, so its survival proves nothing was rewritten.
		for _, name := range names {
			sentinel := filepath.Join(fixture.agentDir, name, "sentinel.txt")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		stateBefore, err := os.ReadFile(fixture.statePath)
		if err != nil {
			t.Fatal(err)
		}

		summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true, DryRun: true}, &log)
		if err != nil {
			t.Fatalf("dry-run: %v", err)
		}
		want := "~" + string(rune('0'+len(names)))
		if !strings.Contains(summary, want) {
			t.Errorf("%d skills: summary %q lacks %q", len(names), summary, want)
		}
		if !strings.Contains(summary, "+0") || !strings.Contains(summary, "-0") {
			t.Errorf("summary %q should show +0 and -0", summary)
		}
		for _, name := range names {
			entry := filepath.Join(fixture.agentDir, name)
			if _, err := os.Stat(filepath.Join(entry, "sentinel.txt")); err != nil {
				t.Errorf("%s sentinel gone, dry-run mutated copy: %v", name, err)
			}
			if _, err := os.Stat(filepath.Join(entry, copyMarkerName)); err != nil {
				t.Errorf("%s marker missing: %v", name, err)
			}
			if _, err := os.Stat(filepath.Join(entry, "SKILL.md")); err != nil {
				t.Errorf("%s SKILL.md missing: %v", name, err)
			}
		}
		stateAfter, err := os.ReadFile(fixture.statePath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stateBefore, stateAfter) {
			t.Error("dry-run rewrote state file")
		}
	}
}
