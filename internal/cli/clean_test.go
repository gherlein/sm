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

func TestDoClean_RemovesOwnedLinks(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	seedOwnedLink(t, current, fixture.agentDir, "beta", filepath.Join(fixture.storeDir, "beta"))
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", false, &out)
	if err != nil {
		t.Fatalf("doClean: %v", err)
	}
	if !strings.Contains(summary, "-2") {
		t.Errorf("summary %q lacks -2", summary)
	}
	for _, name := range []string{"alpha", "beta"} {
		if isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s link still present", name)
		}
	}
	after, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Targets[fixture.agentDir].Links) != 0 {
		t.Errorf("state records not cleared: %v", after.Targets)
	}
	if got := listNames(t, fixture.storeDir); len(got) != 2 {
		t.Errorf("store changed: %v", got)
	}
}

func TestDoClean_LeavesUnmanaged(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	unmanagedDir := filepath.Join(fixture.agentDir, "beta")
	if err := os.MkdirAll(unmanagedDir, 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := doClean(fixture.cfg, fixture.statePath, "", false, &out); err != nil {
		t.Fatalf("doClean: %v", err)
	}
	info, err := os.Lstat(unmanagedDir)
	if err != nil || !info.IsDir() {
		t.Fatalf("unmanaged dir was not left intact: %v", err)
	}
}

func TestDoClean_OnlyAgent(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	otherDir := filepath.Join(filepath.Dir(fixture.agentDir), "other", "skills")
	cfg := agents.Config{Agents: []agents.Agent{
		{Name: "claude-code", Dir: otherDir},
		{Name: "copilot", Dir: fixture.agentDir},
	}}
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(fixture.storeDir, "alpha")
	seedOwnedLink(t, current, otherDir, "alpha", storePath)
	seedOwnedLink(t, current, fixture.agentDir, "alpha", storePath)
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := doClean(cfg, fixture.statePath, "copilot", false, &out); err != nil {
		t.Fatalf("doClean: %v", err)
	}
	if isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("copilot link not removed")
	}
	if !isSymlink(t, filepath.Join(otherDir, "alpha")) {
		t.Error("other agent link was removed")
	}
	after, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Targets[otherDir].Links) != 1 {
		t.Errorf("other agent state lost: %v", after.Targets)
	}
}

func TestDoClean_UnknownAgent(t *testing.T) {
	fixture := newSyncFixture(t)
	var out bytes.Buffer
	if _, err := doClean(fixture.cfg, fixture.statePath, "nope", false, &out); err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestDoClean_RecordedNameReplacedByRealDir(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta")
	current, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	target := current.Targets[fixture.agentDir]
	target.Links["beta"] = filepath.Join(fixture.storeDir, "beta")
	current.Targets[fixture.agentDir] = target
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	realDir := filepath.Join(fixture.agentDir, "beta")
	childFile := filepath.Join(realDir, "keep.txt")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(childFile, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", false, &out)
	if err != nil {
		t.Fatalf("doClean must not abort: %v", err)
	}
	if _, err := os.Stat(childFile); err != nil {
		t.Errorf("real dir contents were removed: %v", err)
	}
	if isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("alpha link not removed")
	}
	if !strings.Contains(summary, "-1") {
		t.Errorf("summary %q should reflect only alpha removed (-1)", summary)
	}
	after, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, stillRecorded := after.Targets[fixture.agentDir].Links["beta"]; stillRecorded {
		t.Error("stale beta record was not dropped")
	}
}

// R3: clean removes a recorded symlink only while it still points at the
// recorded target; a link the user re-pointed at that name is theirs.
func TestDoClean_LeavesUserReplacedRecordedLink(t *testing.T) {
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

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", false, &out)
	if err != nil {
		t.Fatalf("doClean: %v", err)
	}
	if !strings.Contains(summary, "fake -0") {
		t.Errorf("summary %q, want fake -0", summary)
	}
	destination, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("user's link was removed: %v", err)
	}
	if destination != usersTarget {
		t.Errorf("user's link points at %q, want %q", destination, usersTarget)
	}
	if !strings.Contains(out.String(), "no longer an sm link; leaving it in place") {
		t.Errorf("missing warning, output: %q", out.String())
	}
	after, err := state.Load(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := after.Targets[fixture.agentDir]; present {
		t.Errorf("state still records the agent: %v", after.Targets)
	}
}

// R13: clean rewrites state, so it must not run beside another sm.
func TestDoClean_RespectsLock(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	unlock, err := state.Lock(fixture.statePath, stateLockHolder)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := unlock(); err != nil {
			t.Error(err)
		}
	})

	var out bytes.Buffer
	_, err = doClean(fixture.cfg, fixture.statePath, "", false, &out)
	want := "another sm or ms process is running (lock " + fixture.statePath + ".lock)"
	if err == nil || err.Error() != want {
		t.Fatalf("error = %v, want %q", err, want)
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("a locked-out clean removed a link")
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("a locked-out clean changed state")
	}
}

// R14: an agent dropped from agents.toml leaves its records behind; --orphans
// reaches them, removing only entries whose identity still matches.
func TestDoClean_OrphansRemovesIdentityCheckedEntries(t *testing.T) {
	fixture := newSyncFixture(t, "alpha", "beta", "gamma")
	droppedDir := filepath.Join(filepath.Dir(filepath.Dir(fixture.agentDir)), "dropped", "skills")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	seedOwnedLink(t, current, droppedDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))

	// beta was recorded, then replaced by the user's own link.
	usersTarget := t.TempDir()
	if err := os.Symlink(usersTarget, filepath.Join(droppedDir, "beta")); err != nil {
		t.Fatal(err)
	}
	target := current.Targets[droppedDir]
	target.Links["beta"] = filepath.Join(fixture.storeDir, "beta")
	target.Copies = map[string]string{"gamma": filepath.Join(fixture.storeDir, "gamma")}
	current.Targets[droppedDir] = target
	gammaStorePath := filepath.Join(fixture.storeDir, "gamma")
	if _, err := link.CreateCopy(droppedDir, "gamma", gammaStorePath, pinOf(t, gammaStorePath)); err != nil {
		t.Fatal(err)
	}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != "clean: "+droppedDir+" -2" {
		t.Errorf("summary %q, want %q", summary, "clean: "+droppedDir+" -2")
	}
	for _, name := range []string{"alpha", "gamma"} {
		if _, err := os.Lstat(filepath.Join(droppedDir, name)); !os.IsNotExist(err) {
			t.Errorf("orphaned %s not removed: %v", name, err)
		}
	}
	if got := readLink(t, filepath.Join(droppedDir, "beta")); got != usersTarget {
		t.Errorf("user's link points at %q, want %q", got, usersTarget)
	}
	if !strings.Contains(out.String(), "beta is no longer an sm link; leaving it in place") {
		t.Errorf("missing warning for the user's link: %q", out.String())
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[droppedDir]; present {
		t.Errorf("orphaned target still in state: %v", after.Targets[droppedDir])
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("a configured agent's link was removed by --orphans")
	}
	if _, recorded := after.Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("a configured agent's record was dropped by --orphans")
	}
}

func TestDoClean_OrphansNoneFound(t *testing.T) {
	fixture := newSyncFixture(t, "alpha")
	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != "clean: no orphaned targets" {
		t.Errorf("summary %q, want %q", summary, "clean: no orphaned targets")
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("a configured agent's link was removed")
	}
}

func TestDoClean_OrphansWithAgentIsRejected(t *testing.T) {
	fixture := newSyncFixture(t)
	var out bytes.Buffer
	if _, err := doClean(fixture.cfg, fixture.statePath, "fake", true, &out); err == nil {
		t.Fatal("expected an error combining --orphans with --agent")
	}
}

// homeFixture is a sync fixture whose single agent "fake" lives under a HOME
// set for the test, so --agent-home style re-rooted targets can be modeled.
type homeFixture struct {
	syncFixture
	root string
	home string
}

func newHomeFixture(t *testing.T, skillNames ...string) homeFixture {
	t.Helper()
	fixture := newSyncFixture(t, skillNames...)
	root := filepath.Dir(fixture.storeDir)
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	fixture.agentDir = filepath.Join(home, ".claude", "skills")
	fixture.cfg = agents.Config{Agents: []agents.Agent{{Name: "fake", Dir: fixture.agentDir}}}
	return homeFixture{syncFixture: fixture, root: root, home: home}
}

// aliasOf creates <root>/alias as a symlink to the agent's parent and returns
// the agent dir spelled through it: the same physical directory.
func (fixture homeFixture) aliasOf(t *testing.T) string {
	t.Helper()
	if err := os.MkdirAll(fixture.agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasParent := filepath.Join(fixture.root, "alias")
	if err := os.Symlink(filepath.Dir(fixture.agentDir), aliasParent); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(aliasParent, filepath.Base(fixture.agentDir))
}

// Scenario A: an earlier release kept a second key for an alias of the live
// agent's directory. Its records describe the live agent's own entries, so
// --orphans must remove nothing for it, only drop the duplicate record.
func TestDoClean_OrphansLeavesAliasedAgentLinksIntact(t *testing.T) {
	fixture := newHomeFixture(t, "alpha")
	aliasDir := fixture.aliasOf(t)
	storePath := filepath.Join(fixture.storeDir, "alpha")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, fixture.agentDir, "alpha", storePath)
	current.Targets[aliasDir] = state.Target{Links: map[string]string{"alpha": storePath}}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != noOrphansSummary {
		t.Errorf("summary %q, want %q", summary, noOrphansSummary)
	}
	if got := readLink(t, filepath.Join(fixture.agentDir, "alpha")); got != storePath {
		t.Errorf("live agent's link points at %q, want %q", got, storePath)
	}
	if !strings.Contains(out.String(), aliasDir+": same directory as agent fake; dropped its duplicate record, removed nothing") {
		t.Errorf("alias handling not reported: %q", out.String())
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[aliasDir]; present {
		t.Error("the alias key's duplicate record was not dropped")
	}
	if got := after.Targets[fixture.agentDir].Links["alpha"]; got != storePath {
		t.Errorf("live agent's record = %q, want %q", got, storePath)
	}
}

// Scenario B: a profile provisioned with sync --agent-home is not an orphan;
// only an explicit clean --agent-home may remove it.
func TestDoClean_OrphansLeavesProvisionedAgentHomeTargets(t *testing.T) {
	fixture := newHomeFixture(t, "alpha")
	containerHome := filepath.Join(fixture.root, "ctr-home")
	provisionedDir := filepath.Join(containerHome, ".claude", "skills")
	provisioned := agents.Config{Agents: []agents.Agent{{Name: "fake", Dir: provisionedDir}}}
	var log bytes.Buffer
	if _, err := doSync(provisioned, fixture.storeDir, fixture.statePath, syncOptions{CopyMode: true}, &log); err != nil {
		t.Fatal(err)
	}
	stateBefore, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != noOrphansSummary {
		t.Errorf("summary %q, want %q", summary, noOrphansSummary)
	}
	owned, err := link.IsOwnedCopy(provisionedDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	if err != nil || !owned {
		t.Errorf("provisioned copy was disturbed: %v, %v", owned, err)
	}
	wantNotice := provisionedDir + ": provisioned with --agent-home; left alone (clean with: sm clean --agent-home " + containerHome + ")"
	if !strings.Contains(out.String(), wantNotice) {
		t.Errorf("output lacks %q: %q", wantNotice, out.String())
	}
	stateAfter, err := os.ReadFile(fixture.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Error("state changed although nothing was cleaned")
	}
}

// An agent removed from agents.toml is a true orphan even with the alias and
// provisioning checks active: its sm links go, a user-replaced entry stays.
func TestDoClean_OrphansStillRemovesTrueOrphans(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	droppedDir := filepath.Join(fixture.home, ".pi", "skills")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, fixture.agentDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	seedOwnedLink(t, current, droppedDir, "alpha", filepath.Join(fixture.storeDir, "alpha"))
	usersTarget := t.TempDir()
	if err := os.Symlink(usersTarget, filepath.Join(droppedDir, "beta")); err != nil {
		t.Fatal(err)
	}
	target := current.Targets[droppedDir]
	target.Links["beta"] = filepath.Join(fixture.storeDir, "beta")
	current.Targets[droppedDir] = target
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != "clean: "+droppedDir+" -1" {
		t.Errorf("summary %q, want %q", summary, "clean: "+droppedDir+" -1")
	}
	if _, err := os.Lstat(filepath.Join(droppedDir, "alpha")); !os.IsNotExist(err) {
		t.Errorf("orphaned sm link not removed: %v", err)
	}
	if got := readLink(t, filepath.Join(droppedDir, "beta")); got != usersTarget {
		t.Errorf("user's link points at %q, want %q", got, usersTarget)
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[droppedDir]; present {
		t.Error("cleaned orphan still in state")
	}
	if !isSymlink(t, filepath.Join(fixture.agentDir, "alpha")) {
		t.Error("the configured agent's link was removed")
	}
}

// Records only the alias spelling held (from the previous release) are moved
// into the live agent's key when the entry still verifies there, so they stay
// tracked; one that no longer verifies is dropped and its entry left alone.
func TestDoClean_OrphansMergesVerifiedAliasRecordsIntoLiveKey(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta", "gamma")
	aliasDir := fixture.aliasOf(t)
	alphaStore := filepath.Join(fixture.storeDir, "alpha")
	gammaStore := filepath.Join(fixture.storeDir, "gamma")
	if err := os.Symlink(alphaStore, filepath.Join(fixture.agentDir, "alpha")); err != nil {
		t.Fatal(err)
	}
	usersTarget := t.TempDir()
	betaPath := filepath.Join(fixture.agentDir, "beta")
	if err := os.Symlink(usersTarget, betaPath); err != nil {
		t.Fatal(err)
	}
	if _, err := link.CreateCopy(fixture.agentDir, "gamma", gammaStore, pinOf(t, gammaStore)); err != nil {
		t.Fatal(err)
	}
	current := loadState(t, fixture.statePath)
	current.Targets[aliasDir] = state.Target{
		Links:  map[string]string{"alpha": alphaStore, "beta": filepath.Join(fixture.storeDir, "beta")},
		Copies: map[string]string{"gamma": gammaStore},
	}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	summary, err := doClean(fixture.cfg, fixture.statePath, "", true, &out)
	if err != nil {
		t.Fatalf("doClean --orphans: %v", err)
	}
	if summary != noOrphansSummary {
		t.Errorf("summary %q, want %q", summary, noOrphansSummary)
	}
	wantMerged := "merged 2 record(s) from " + aliasDir + " into " + fixture.agentDir
	if !strings.Contains(out.String(), wantMerged) {
		t.Errorf("output lacks %q: %q", wantMerged, out.String())
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[aliasDir]; present {
		t.Error("alias key not dropped")
	}
	live := after.Targets[fixture.agentDir]
	if live.Links["alpha"] != alphaStore {
		t.Errorf("alpha not merged into the live key: %v", live.Links)
	}
	if live.Copies["gamma"] != gammaStore {
		t.Errorf("gamma not merged into the live key: %v", live.Copies)
	}
	if _, merged := live.Links["beta"]; merged {
		t.Error("an unverified record was merged")
	}
	if got := readLink(t, betaPath); got != usersTarget {
		t.Errorf("user's beta link changed to %q", got)
	}
	if got := readLink(t, filepath.Join(fixture.agentDir, "alpha")); got != alphaStore {
		t.Errorf("alpha link changed to %q", got)
	}

	var log bytes.Buffer
	if _, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log); err != nil {
		t.Fatalf("doSync: %v", err)
	}
	if strings.Contains(log.String(), "alpha exists and is not managed by sm") {
		t.Errorf("merged alpha still reported unmanaged: %q", log.String())
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("alpha record lost on the next sync")
	}
}

func TestProvisionedUnder_PrefersLongestSuffix(t *testing.T) {
	home := "/home/u"
	cfg := agents.Config{Agents: []agents.Agent{
		{Name: "a-generic", Dir: "/home/u/skills"},
		{Name: "pi", Dir: "/home/u/.pi/skills"},
	}}
	agentName, agentHome, isProvisioned := provisionedUnder(cfg, []string{home}, "/ctr/.pi/skills")
	if !isProvisioned || agentName != "pi" || agentHome != "/ctr" {
		t.Errorf("provisionedUnder = (%q, %q, %v), want (\"pi\", \"/ctr\", true)", agentName, agentHome, isProvisioned)
	}
}
