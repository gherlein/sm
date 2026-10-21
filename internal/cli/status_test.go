package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/state"
)

// seedOwnedLink creates the symlink on disk and records it in state, which is
// what a prior sync would have left behind.
func seedOwnedLink(t *testing.T, current state.State, agentDir, name, storePath string) {
	t.Helper()
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(storePath, filepath.Join(agentDir, name)); err != nil {
		t.Fatal(err)
	}
	target := current.Targets[agentDir]
	if target.Links == nil {
		target.Links = map[string]string{}
	}
	target.Links[name] = storePath
	current.Targets[agentDir] = target
}

func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDoStatus_ReportsCounts(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta", "gamma")
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(fixture.agentDir, "gamma"), 0o755); err != nil {
		t.Fatal(err)
	}
	storeBefore := listNames(t, fixture.storeDir)
	agentBefore := listNames(t, fixture.agentDir)
	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}

	report := out.String()
	for _, want := range []string{"store: 3 skills", "owned=1", "missing=1", "unmanaged=1", "dangling=0"} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
	if got := listNames(t, fixture.storeDir); strings.Join(got, ",") != strings.Join(storeBefore, ",") {
		t.Errorf("store changed: %v -> %v", storeBefore, got)
	}
	if got := listNames(t, fixture.agentDir); strings.Join(got, ",") != strings.Join(agentBefore, ",") {
		t.Errorf("agent dir changed: %v -> %v", agentBefore, got)
	}
	stateAfter, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state file changed")
	}
}

func TestDoStatus_ReportsDangling(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	seedOwnedLink(t, current, fixture.agentDir, "removed", filepath.Join(fixture.storeDir, "removed"))
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	for _, want := range []string{"owned=1", "dangling=1", "missing=0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// R14: records for an agent no longer in agents.toml must be visible.
func TestDoStatus_ListsOrphanedTargets(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	droppedDir := filepath.Join(filepath.Dir(filepath.Dir(fixture.agentDir)), "dropped", "skills")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, droppedDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	seedOwnedLink(t, current, droppedDir, "beta", filepath.Join(fixture.storeDir, "beta"))
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	wantLine := "orphaned target: " + droppedDir + " (2 entries)\n"

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	report := out.String()
	if !strings.Contains(report, wantLine) {
		t.Errorf("report lacks %q:\n%s", wantLine, report)
	}
	agentLine := strings.Index(report, "fake ")
	orphanLine := strings.Index(report, "orphaned target:")
	if agentLine < 0 || orphanLine < agentLine {
		t.Errorf("orphan line should follow the agent lines:\n%s", report)
	}

	out.Reset()
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, false, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	if strings.Contains(out.String(), "orphaned target") {
		t.Errorf("orphans listed outside orphan scope:\n%s", out.String())
	}
}

// R27: a recorded link whose target is gone is dangling even when the store
// has a skill of that name (the store moved and sync has not repointed yet).
func TestDoStatus_CountsBrokenLinkAsDangling(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(fixture.storeDir); err != nil {
		t.Fatal(err)
	}
	newStore := filepath.Join(filepath.Dir(fixture.storeDir), "store-b")
	writeSkill(t, newStore, "alpha")

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, newStore, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	for _, want := range []string{"owned=0", "missing=0", "unmanaged=0", "dangling=1"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report missing %q:\n%s", want, out.String())
		}
	}
}

// R27: status must not hide the store problems sync would warn about.
func TestDoStatus_PrintsStoreWarnings(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	writeSkill(t, fixture.storeDir, "Upper")
	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	if !strings.Contains(out.String(), "warning: skipping \"Upper\"") {
		t.Errorf("store warning not printed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "store: 1 skills") {
		t.Errorf("store count wrong:\n%s", out.String())
	}
}

func TestDoStatus_LabelsAliasedProvisionedAndOrphanedTargetsDistinctly(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	containerHome := filepath.Join(fixture.root, "ctr-home")
	provisionedDir := filepath.Join(containerHome, ".claude", "skills")
	droppedDir := filepath.Join(fixture.home, ".pi", "skills")
	current := loadState(t, fixture.statePath)
	for _, dir := range []string{fixture.agentDir, provisionedDir, droppedDir} {
		seedOwnedLink(t, current, dir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	}
	seedOwnedLink(t, current, provisionedDir, "beta", filepath.Join(fixture.storeDir, "beta"))
	current.Targets[aliasDir] = state.Target{Links: map[string]string{"alpha": filepath.Join(fixture.storeDir, "alpha")}}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus: %v", err)
	}
	report := out.String()
	for _, want := range []string{
		"aliased target: " + aliasDir + " (same directory as agent fake)\n",
		"provisioned target: " + provisionedDir + " (2 entries; clean with: sm clean --agent-home " + containerHome + ")\n",
		"orphaned target: " + droppedDir + " (1 entries)\n",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Count(report, "target: ") != 3 {
		t.Errorf("want exactly three target lines:\n%s", report)
	}
}

// One stale state key that cannot be resolved (here a symlink loop) must not
// make status unusable, and clean --orphans must never act under it.
func TestDoStatus_UnresolvableStateKeyIsReportedNotFatal(t *testing.T) {
	fixture := newHomeFixture(t, "alpha")
	loop := filepath.Join(fixture.root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	staleDir := filepath.Join(loop, "skills")
	current := loadState(t, fixture.statePath)
	current.Targets[staleDir] = state.Target{Links: map[string]string{"alpha": filepath.Join(fixture.storeDir, "alpha")}}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	wantPrefix := "unclassifiable target: " + staleDir + " ("

	var out bytes.Buffer
	if err := doStatus(fixture.cfg, fixture.storeDir, fixture.statePath, true, &out); err != nil {
		t.Fatalf("doStatus must not fail on one stale key: %v", err)
	}
	if !strings.Contains(out.String(), wantPrefix) {
		t.Errorf("report lacks %q:\n%s", wantPrefix, out.String())
	}
	if !strings.Contains(out.String(), "fake ") {
		t.Errorf("agent line missing:\n%s", out.String())
	}

	out.Reset()
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans must not fail on one stale key: %v", err)
	}
	if summary != noOrphansSummary {
		t.Errorf("summary %q, want %q", summary, noOrphansSummary)
	}
	if !strings.Contains(out.String(), wantPrefix) {
		t.Errorf("clean output lacks %q: %q", wantPrefix, out.String())
	}
	if _, present := loadState(t, fixture.statePath).Targets[staleDir]; !present {
		t.Error("an unclassifiable key was dropped")
	}
}
