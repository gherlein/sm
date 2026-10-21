package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/agents"
	"github.com/gherlein/sm/internal/link"
	"github.com/gherlein/sm/internal/state"
)

func writeSkill(t *testing.T, storeDir, name string) {
	t.Helper()
	skillDir := filepath.Join(storeDir, name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type syncFixture struct {
	storeDir  string
	agentDir  string
	statePath string
	cfg       agents.Config
}

func newSyncFixture(t *testing.T, skillNames ...string) syncFixture {
	t.Helper()
	root := t.TempDir()
	fixture := syncFixture{
		storeDir:  filepath.Join(root, "store"),
		agentDir:  filepath.Join(root, "agent", "skills"),
		statePath: filepath.Join(root, "state", "state.json"),
	}
	if err := os.MkdirAll(fixture.storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range skillNames {
		writeSkill(t, fixture.storeDir, name)
	}
	fixture.cfg = agents.Config{Agents: []agents.Agent{{Name: "fake", Dir: fixture.agentDir}}}
	return fixture
}

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSymlink != 0
}

func TestDoSync_LinksAllSkills(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if !strings.Contains(summary, "+2 -0") {
		t.Errorf("summary %q lacks +2 -0", summary)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s not linked", name)
		}
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	recorded := saved.Targets[fixture.agentDir].Links
	if len(recorded) != 2 || recorded["alpha"] == "" || recorded["beta"] == "" {
		t.Errorf("state links = %v", recorded)
	}
}

func TestDoSync_ReconcilesRemoved(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if !strings.Contains(summary, "+0 -1") {
		t.Errorf("summary %q lacks +0 -1", summary)
	}
	if isSymlink(t, filepath.Join(fixture.agentDir, "beta")) {
		t.Error("beta link should be removed")
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("alpha link should remain")
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillRecorded := saved.Targets[fixture.agentDir].Links["beta"]; stillRecorded {
		t.Error("beta still recorded in state")
	}
}

func TestDoSync_SkipsUnmanaged(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	handPlaced := filepath.Join(fixture.agentDir, "alpha")
	if err := os.MkdirAll(handPlaced, 0o755); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer

	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if isSymlink(t, handPlaced) {
		t.Error("real directory was replaced by a symlink")
	}
	if !strings.Contains(log.String(), "alpha") {
		t.Errorf("no warning naming alpha in log %q", log.String())
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	links := saved.Targets[fixture.agentDir].Links
	if _, recorded := links["alpha"]; recorded {
		t.Error("unmanaged alpha must not be recorded")
	}
	if _, recorded := links["beta"]; !recorded {
		t.Error("beta should be recorded")
	}
}

func TestDoSync_MissingStoreNoChanges(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(fixture.storeDir); err != nil {
		t.Fatal(err)
	}

	_, err = doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err == nil {
		t.Fatal("expected an error for a missing store")
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("existing link must be untouched when the store is missing")
	}
	stateAfter, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state must not change when the store is missing")
	}
	if log.Len() == 0 {
		t.Error("expected a warning in the log")
	}
}

func TestDoSync_DryRunTouchesNothing(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{DryRun: true}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if !strings.Contains(summary, "+2 -0") {
		t.Errorf("summary %q lacks +2 -0", summary)
	}
	if _, statErr := os.Lstat(filepath.Join(fixture.agentDir, "alpha")); !os.IsNotExist(statErr) {
		t.Error("dry run created a link")
	}
	if _, statErr := os.Stat(fixture.statePath); !os.IsNotExist(statErr) {
		t.Error("dry run wrote the state file")
	}
}

func TestDoSync_PartialFailurePersistsProgress(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so a read-only dir cannot force a Create failure")
	}
	fixture := newSyncFixture(t, "alpha")
	blockedDir := filepath.Join(t.TempDir(), "blocked")
	if err := os.MkdirAll(blockedDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(blockedDir, 0o700) })
	fixture.cfg = agents.Config{Agents: []agents.Agent{
		{Name: "first", Dir: fixture.agentDir},
		{Name: "second", Dir: blockedDir},
	}}
	var log bytes.Buffer

	_, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err == nil {
		t.Fatal("expected an error from the read-only agent dir")
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Fatal("first agent link should have been created")
	}
	saved, loadErr := state.Load(fixture.statePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if _, recorded := saved.Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("successfully created link was not persisted to state")
	}
}

func TestDoSync_StaleNameNowRealDirSkipped(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	secondAgentDir := filepath.Join(filepath.Dir(filepath.Dir(fixture.agentDir)), "agent2", "skills")
	fixture.cfg = agents.Config{Agents: []agents.Agent{
		{Name: "fake", Dir: fixture.agentDir},
		{Name: "second", Dir: secondAgentDir},
	}}
	// First run only knows the first agent so alpha is recorded for it alone.
	firstOnly := agents.Config{Agents: fixture.cfg.Agents[:1]}
	var log bytes.Buffer
	if _, err := doSync(firstOnly, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("first doSync: %v", err)
	}

	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	writeSkill(t, fixture.storeDir, "beta")
	alphaPath := filepath.Join(fixture.agentDir, "alpha")
	if err := os.Remove(alphaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(alphaPath, 0o755); err != nil {
		t.Fatal(err)
	}
	childPath := filepath.Join(alphaPath, "keep.txt")
	if err := os.WriteFile(childPath, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("second doSync must not fail: %v", err)
	}

	if _, err := os.Stat(childPath); err != nil {
		t.Errorf("real directory content lost: %v", err)
	}
	if !isSymlink(t, filepath.Join(secondAgentDir, "beta")) {
		t.Error("second agent's beta not linked; run was aborted")
	}
	saved, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillRecorded := saved.Targets[fixture.agentDir].Links["alpha"]; stillRecorded {
		t.Error("stale alpha record not dropped")
	}
}

// requireNonRoot fails loudly when the test runs as root: root ignores the
// permission bits these tests rely on to provoke failures, so the test would
// otherwise pass without exercising anything.
func requireNonRoot(t *testing.T, why string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatalf("this test needs a non-root user: %s", why)
	}
}

// readLink returns the raw target of the symlink at path.
func readLink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

func loadState(t *testing.T, statePath string) state.State {
	t.Helper()
	loaded, err := state.Load(statePath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

// R3: a recorded link the user replaced with their own symlink is theirs; when
// the store skill goes away sm must drop its record and leave the link alone.
func TestDoSync_DoesNotDeleteUserReplacedSymlink(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(fixture.agentDir, "alpha")
	usersTarget := t.TempDir()
	usersFile := filepath.Join(usersTarget, "SKILL.md")
	if err := os.WriteFile(usersFile, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(usersTarget, linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "alpha")); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	if got := readLink(t, linkPath); got != usersTarget {
		t.Errorf("user's link points at %q, want %q", got, usersTarget)
	}
	if _, err := os.Stat(usersFile); err != nil {
		t.Errorf("user's link target damaged: %v", err)
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["alpha"]; recorded {
		t.Error("record for the user-replaced link was not dropped")
	}
	if !strings.Contains(log.String(), "alpha is no longer an sm link; leaving it in place") {
		t.Errorf("missing warning, log: %q", log.String())
	}
}

// A desired name whose recorded link the user replaced is both Lost and
// Unmanaged in the plan; the user must see one message for it, not two.
func TestDoSync_LostAndUnmanagedWarnsOnce(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(fixture.agentDir, "alpha")
	usersTarget := t.TempDir()
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(usersTarget, linkPath); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if count := strings.Count(log.String(), "warning:"); count != 1 {
		t.Errorf("got %d warnings, want exactly 1: %q", count, log.String())
	}
	if !strings.Contains(log.String(), "no longer an sm link; leaving it in place") {
		t.Errorf("missing lost warning: %q", log.String())
	}
	if got := readLink(t, linkPath); got != usersTarget {
		t.Errorf("user's link points at %q, want %q", got, usersTarget)
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["alpha"]; recorded {
		t.Error("record for the user-replaced link was not dropped")
	}
}

// R4: when the store moves, sm repoints the links it owns at the new store.
func TestDoSync_RepointsLinkWhenStoreChanges(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	newStore := filepath.Join(filepath.Dir(fixture.storeDir), "store-b")
	writeSkill(t, newStore, "alpha")
	linkPath := filepath.Join(fixture.agentDir, "alpha")
	oldTarget := filepath.Join(fixture.storeDir, "alpha")
	newTarget := filepath.Join(newStore, "alpha")

	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	dryRunSummary, err := doSync(fixture.cfg, newStore, fixture.statePath, syncOptions{DryRun: true}, &log)
	if err != nil {
		t.Fatalf("dry-run doSync: %v", err)
	}
	if dryRunSummary != "sync: fake +0 -0 ~1" {
		t.Errorf("dry-run summary %q, want %q", dryRunSummary, "sync: fake +0 -0 ~1")
	}
	if got := readLink(t, linkPath); got != oldTarget {
		t.Errorf("dry run repointed the link to %q", got)
	}
	stateAfterDryRun, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfterDryRun) {
		t.Error("dry run rewrote the state file")
	}

	summary, err := doSync(fixture.cfg, newStore, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0 ~1" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0 ~1")
	}
	if got := readLink(t, linkPath); got != newTarget {
		t.Errorf("link points at %q, want %q", got, newTarget)
	}
	if got := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["alpha"]; got != newTarget {
		t.Errorf("state records %q, want %q", got, newTarget)
	}
}

func TestDoSync_RepointsDanglingLinkAfterOldStoreRemoved(t *testing.T) {
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

	summary, err := doSync(fixture.cfg, newStore, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0 ~1" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0 ~1")
	}
	newTarget := filepath.Join(newStore, "alpha")
	if got := readLink(t, filepath.Join(fixture.agentDir, "alpha")); got != newTarget {
		t.Errorf("link points at %q, want %q", got, newTarget)
	}
	if _, err := os.Stat(filepath.Join(fixture.agentDir, "alpha", "SKILL.md")); err != nil {
		t.Errorf("repointed link does not resolve: %v", err)
	}
}

// R20: a store entry that exists but cannot be used right now (unreadable
// SKILL.md, dangling store symlink) is not a removed skill; the link sm made
// for it must survive.
func TestDoSync_KeepsLinksForUnusableStoreEntries(t *testing.T) {
	requireNonRoot(t, "root can read a mode-0 SKILL.md, so the entry would not be unusable")
	fixture := newSyncFixture(t, "alpha", "beta", "gamma")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	alphaSkill := filepath.Join(fixture.storeDir, "alpha", "SKILL.md")
	if err := os.Chmod(alphaSkill, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(alphaSkill, 0o644); err != nil {
			t.Error(err)
		}
	})
	betaStore := filepath.Join(fixture.storeDir, "beta")
	if err := os.RemoveAll(betaStore); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), betaStore); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if got := readLink(t, filepath.Join(fixture.agentDir, name)); got != filepath.Join(fixture.storeDir, name) {
			t.Errorf("%s link points at %q", name, got)
		}
	}
	links := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, recorded := links[name]; !recorded {
			t.Errorf("%s record dropped", name)
		}
	}
	for _, want := range []string{
		"warning: fake: keeping " + filepath.Join(fixture.agentDir, "alpha"),
		"warning: fake: keeping " + filepath.Join(fixture.agentDir, "beta"),
	} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q: %q", want, log.String())
		}
	}
}

func emptyStore(t *testing.T, fixture syncFixture, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := os.RemoveAll(filepath.Join(fixture.storeDir, name)); err != nil {
			t.Fatal(err)
		}
	}
}

// R20: an empty store (say, an unmounted volume) while sm has recorded links
// is far more likely an accident than a request to prune everything.
func TestDoSync_EmptyStoreRefusedWithRecordedState(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	emptyStore(t, fixture, "alpha", "beta")
	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}

	for _, options := range []syncOptions{{}, {DryRun: true}} {
		_, err = doSync(fixture.cfg, fixture.storeDir, fixture.statePath, options, &log)
		if err == nil {
			t.Fatalf("%+v: expected a refusal for an empty store", options)
		}
		if !strings.Contains(err.Error(), "--allow-empty") {
			t.Errorf("%+v: error %q does not mention --allow-empty", options, err)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s link removed despite the refusal", name)
		}
	}
	stateAfter, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state changed despite the refusal")
	}
}

func TestDoSync_AllowEmptyPrunes(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	emptyStore(t, fixture, "alpha", "beta")

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{AllowEmpty: true}, &log)
	if err != nil {
		t.Fatalf("doSync --allow-empty: %v", err)
	}
	if summary != "sync: fake +0 -2" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -2")
	}
	if names := listNames(t, fixture.agentDir); len(names) != 0 {
		t.Errorf("agent dir not pruned: %v", names)
	}
	if links := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links; len(links) != 0 {
		t.Errorf("state still records %v", links)
	}
}

func TestDoSync_EmptyStoreWithNoStateIsFine(t *testing.T) {
	fixture := newSyncFixture(t)
	var log bytes.Buffer
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
}

// R13: two runs must never interleave their load-modify-save of state.
func TestDoSync_ConcurrentSecondRunFailsFast(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	unlock, err := state.Lock(fixture.statePath, stateLockHolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	})
	var log bytes.Buffer

	_, err = doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	want := "another sm or ms process is running (lock " + fixture.statePath + ".lock)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if _, statErr := os.Lstat(filepath.Join(fixture.agentDir, "alpha")); !os.IsNotExist(statErr) {
		t.Error("a locked-out sync created a link")
	}
	if _, statErr := os.Stat(fixture.statePath); !os.IsNotExist(statErr) {
		t.Error("a locked-out sync wrote state")
	}

	// A dry run writes nothing, so it needs no lock and still works.
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{DryRun: true}, &log)
	if err != nil {
		t.Fatalf("dry run under a held lock: %v", err)
	}
	if summary != "sync: fake +1 -0" {
		t.Errorf("dry-run summary %q", summary)
	}
}

// R27: an empty agent list must say so instead of printing a bare "sync:".
func TestDoSync_ZeroAgentsMessage(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	summary, err := doSync(agents.Config{}, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "no agents enabled in agents.toml" {
		t.Errorf("summary %q, want %q", summary, "no agents enabled in agents.toml")
	}
	if _, statErr := os.Stat(fixture.statePath); !os.IsNotExist(statErr) {
		t.Error("a zero-agent sync wrote state")
	}
}

// Link mode reports ~R only when something was repointed, so the common
// summary stays the familiar "+N -M".
func TestDoSync_LinkSummaryFormat(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "sync: fake +2 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +2 -0")
	}
	summary, err = doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if summary != "sync: fake +0 -0" {
		t.Errorf("idempotent summary %q, want %q", summary, "sync: fake +0 -0")
	}
}

// A permission error while inspecting an entry is not "absent": treating it so
// would try to create over, or forget, an entry sm cannot see. The agent stops,
// and the progress made for earlier agents is persisted first.
func TestDoSync_ProbeErrorStopsAgent(t *testing.T) {
	requireNonRoot(t, "root can inspect entries in a mode-0 directory, so no probe error could occur")
	fixture := newSyncFixture(t, "alpha")
	blockedDir := filepath.Join(t.TempDir(), "zz-blocked")
	if err := os.MkdirAll(blockedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(blockedDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(blockedDir, 0o755); err != nil {
			t.Error(err)
		}
	})
	fixture.cfg = agents.Config{Agents: []agents.Agent{
		{Name: "first", Dir: fixture.agentDir},
		{Name: "second", Dir: blockedDir},
	}}
	var log bytes.Buffer

	_, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err == nil {
		t.Fatal("expected a probe error from the unreadable agent dir")
	}
	if !strings.Contains(err.Error(), "agent second") || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("error %q should name agent second and the permission failure", err)
	}
	saved := loadState(t, fixture.statePath)
	if _, recorded := saved.Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("first agent's progress was not persisted")
	}
	if links := saved.Targets[blockedDir].Links; len(links) != 0 {
		t.Errorf("blocked agent recorded %v", links)
	}
}

// A mistyped --store full of unrelated, non-conforming directories must not
// switch the empty-store guard off: only unusable entries that sm actually
// recorded show that this is the real store with a temporary problem.
func TestDoSync_EmptyStoreGuardNotDefeatedByUnrelatedUnusableEntries(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	wrongStore := filepath.Join(filepath.Dir(fixture.storeDir), "src")
	for _, name := range []string{"foo", "bar"} {
		if err := os.MkdirAll(filepath.Join(wrongStore, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_, err := doSync(fixture.cfg, wrongStore, fixture.statePath, syncOptions{}, &log)
	if err == nil || !strings.Contains(err.Error(), "--allow-empty") {
		t.Fatalf("error = %v, want a refusal mentioning --allow-empty", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s link removed despite the refusal", name)
		}
	}

	// The real store with only some recorded skills unusable is still refused:
	// beta would be pruned on the strength of one unusable name.
	emptyStore(t, fixture, "alpha", "beta")
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(fixture.storeDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	_, err = doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err == nil || !strings.Contains(err.Error(), "--allow-empty") {
		t.Fatalf("partial overlap: error = %v, want a refusal mentioning --allow-empty", err)
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s link removed despite the refusal", name)
		}
	}

	// Every recorded skill unusable (a transient store problem) is the case the
	// guard lets through: both links and records are kept.
	if err := os.Symlink(filepath.Join(t.TempDir(), "gone"), filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}
	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("link for the unusable recorded skill %s was removed", name)
		}
	}
	if links := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links; len(links) != 2 {
		t.Errorf("records = %v, want alpha and beta kept", links)
	}
}

// A mistyped --store whose unrelated directories happen to include one
// recorded name (docs) must not switch the guard off for the whole ledger.
func TestDoSync_EmptyStoreGuardNotDefeatedByOneNameCollision(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "docs")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(filepath.Dir(fixture.storeDir), "work")
	for _, name := range []string{"docs", "Downloads", "projects"} {
		if err := os.MkdirAll(filepath.Join(work, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, options := range []syncOptions{{}, {DryRun: true}} {
		_, err := doSync(fixture.cfg, work, fixture.statePath, options, &log)
		if err == nil || !strings.Contains(err.Error(), "--allow-empty") {
			t.Fatalf("%+v: error = %v, want a refusal mentioning --allow-empty", options, err)
		}
	}
	for _, name := range []string{"alpha", "docs"} {
		if got := readLink(t, filepath.Join(fixture.agentDir, name)); got != filepath.Join(fixture.storeDir, name) {
			t.Errorf("%s link changed to %q", name, got)
		}
	}
	stateAfter, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state changed despite the refusal")
	}
}

// A recorded link that is already gone is reconciled away (its record
// dropped) and counts as removed, so dry run and real run print the same.
func TestDoSync_DryRunAndRealRunCountsMatchWhenLinkAlreadyGone(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	emptyStore(t, fixture, "beta")
	if err := os.Remove(filepath.Join(fixture.agentDir, "beta")); err != nil {
		t.Fatal(err)
	}

	dryRun, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{DryRun: true}, &log)
	if err != nil {
		t.Fatal(err)
	}
	real, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun != "sync: fake +0 -1" || real != dryRun {
		t.Errorf("dry run %q, real run %q, want both %q", dryRun, real, "sync: fake +0 -1")
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["beta"]; recorded {
		t.Error("record for the vanished link was not dropped")
	}
}

func TestDoSync_VerboseAbsentLinkReportsDroppedRecordNotUnlinked(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	emptyStore(t, fixture, "beta")
	if err := os.Remove(filepath.Join(fixture.agentDir, "beta")); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{Verbose: true}, &log); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(log.String(), "unlinked beta") {
		t.Errorf("an already-absent link must not be reported as unlinked:\n%s", log.String())
	}
	if want := "fake: dropped record for beta (link already gone)"; !strings.Contains(log.String(), want) {
		t.Errorf("log missing %q:\n%s", want, log.String())
	}
}

func TestLogUnlinkedBeforeError_LogsRemovedWithoutVerbose(t *testing.T) {
	var log bytes.Buffer
	logUnlinkedBeforeError(&log, "fake", "beta", link.Removed)
	if want := "fake: unlinked beta\n"; log.String() != want {
		t.Errorf("log = %q, want %q", log.String(), want)
	}
}

func TestLogUnlinkedBeforeError_SilentForOtherResults(t *testing.T) {
	for _, result := range []link.RemoveResult{0, link.Absent, link.NotOurs} {
		var log bytes.Buffer
		logUnlinkedBeforeError(&log, "fake", "beta", result)
		if log.Len() != 0 {
			t.Errorf("result %v logged %q, want nothing", result, log.String())
		}
	}
}

func TestTallyRemoval_RemovedCountsAndLogsWhenVerbose(t *testing.T) {
	var counts syncCounts
	var log bytes.Buffer
	tallyRemoval(&counts, &log, "fake", "beta", link.Removed, true)
	if counts.removed != 1 {
		t.Errorf("removed = %d, want 1", counts.removed)
	}
	if !strings.Contains(log.String(), "fake: unlinked beta") {
		t.Errorf("log = %q, want an unlinked line", log.String())
	}
}

func TestTallyRemoval_NotOursIsNotCounted(t *testing.T) {
	var counts syncCounts
	var log bytes.Buffer
	tallyRemoval(&counts, &log, "fake", "beta", link.NotOurs, true)
	if counts.removed != 0 || log.Len() != 0 {
		t.Errorf("removed = %d, log = %q, want nothing", counts.removed, log.String())
	}
}
