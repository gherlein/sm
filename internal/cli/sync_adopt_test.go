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

const (
	unmanagedWarningFragment = "exists and is not managed by sm"
	worldWritableDirMode     = 0o777
)

// configFor returns a one-agent config for the fixture's agent "fake" spelled
// as dir.
func configFor(dir string) agents.Config {
	return agents.Config{Agents: []agents.Agent{{Name: "fake", Dir: dir}}}
}

func syncOrFail(t *testing.T, cfg agents.Config, fixture homeFixture, options syncOptions, log *bytes.Buffer) string {
	t.Helper()
	summary, err := doSync(cfg, fixture.storeDir, fixture.statePath, options, log)
	if err != nil {
		t.Fatalf("doSync %+v: %v (log %q)", options, err, log.String())
	}
	return summary
}

func readStateFile(t *testing.T, statePath string) []byte {
	t.Helper()
	content, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// Respelling an agent directory through a symlink must not orphan the entries
// sm made under the old spelling: they verify by identity at the new spelling,
// so sync adopts them instead of warning "not managed by sm" forever.
func TestDoSync_AdoptsRecordsFromAliasedStateKey(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)

	log.Reset()
	summary := syncOrFail(t, configFor(aliasDir), fixture, syncOptions{}, &log)
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	if strings.Contains(log.String(), unmanagedWarningFragment) {
		t.Errorf("adopted entries reported as unmanaged: %q", log.String())
	}
	wantAdopted := "adopted 2 record(s) from " + fixture.agentDir + " into " + aliasDir
	if !strings.Contains(log.String(), wantAdopted) {
		t.Errorf("log lacks %q: %q", wantAdopted, log.String())
	}
	for _, name := range []string{"alpha", "beta"} {
		if got := readLink(t, filepath.Join(aliasDir, name)); got != filepath.Join(fixture.storeDir, name) {
			t.Errorf("%s link = %q", name, got)
		}
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[fixture.agentDir]; present {
		t.Errorf("old key not dropped: %v", after.Targets[fixture.agentDir])
	}
	if got := after.Targets[aliasDir].Links; len(got) != 2 || got["alpha"] != filepath.Join(fixture.storeDir, "alpha") || got["beta"] != filepath.Join(fixture.storeDir, "beta") {
		t.Errorf("new key records %v", got)
	}

	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	summary = syncOrFail(t, configFor(aliasDir), fixture, syncOptions{}, &log)
	if summary != "sync: fake +0 -1" {
		t.Errorf("prune summary %q, want %q", summary, "sync: fake +0 -1")
	}
	if _, err := os.Lstat(filepath.Join(fixture.agentDir, "beta")); !os.IsNotExist(err) {
		t.Errorf("beta not pruned through the new key: %v", err)
	}
	if _, recorded := loadState(t, fixture.statePath).Targets[aliasDir].Links["beta"]; recorded {
		t.Error("beta record survived the prune")
	}
}

func TestDoSync_CopyAdoptsRecordsFromAliasedStateKey(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	var log bytes.Buffer
	copyMode := syncOptions{CopyMode: true}
	syncOrFail(t, configFor(fixture.agentDir), fixture, copyMode, &log)

	log.Reset()
	summary := syncOrFail(t, configFor(aliasDir), fixture, copyMode, &log)
	if summary != "sync: fake +0 ~2 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 ~2 -0")
	}
	if strings.Contains(log.String(), unmanagedWarningFragment) {
		t.Errorf("adopted copies reported as unmanaged: %q", log.String())
	}
	wantAdopted := "adopted 2 record(s) from " + fixture.agentDir + " into " + aliasDir
	if !strings.Contains(log.String(), wantAdopted) {
		t.Errorf("log lacks %q: %q", wantAdopted, log.String())
	}
	for _, name := range []string{"alpha", "beta"} {
		owned, err := link.IsOwnedCopy(aliasDir, name, filepath.Join(fixture.storeDir, name))
		if err != nil || !owned {
			t.Errorf("%s copy not owned after adoption: %v, %v", name, owned, err)
		}
	}
	after := loadState(t, fixture.statePath)
	if _, present := after.Targets[fixture.agentDir]; present {
		t.Errorf("old key not dropped: %v", after.Targets[fixture.agentDir])
	}
	if got := after.Targets[aliasDir].Copies; len(got) != 2 || got["alpha"] != filepath.Join(fixture.storeDir, "alpha") || got["beta"] != filepath.Join(fixture.storeDir, "beta") {
		t.Errorf("new key records %v", got)
	}

	if err := os.RemoveAll(filepath.Join(fixture.storeDir, "beta")); err != nil {
		t.Fatal(err)
	}
	log.Reset()
	summary = syncOrFail(t, configFor(aliasDir), fixture, copyMode, &log)
	if summary != "sync: fake +0 ~1 -1" {
		t.Errorf("prune summary %q, want %q", summary, "sync: fake +0 ~1 -1")
	}
	if _, err := os.Lstat(filepath.Join(fixture.agentDir, "beta")); !os.IsNotExist(err) {
		t.Errorf("beta copy not pruned through the new key: %v", err)
	}
}

// A record whose entry the user replaced no longer proves the entry is sm's,
// so it is not adopted: the entry stays, its old record stays where it was,
// and the name gets the ordinary unmanaged warning.
func TestDoSync_DoesNotAdoptRecordThatNoLongerVerifies(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	alphaPath := filepath.Join(fixture.agentDir, "alpha")
	usersTarget := t.TempDir()
	if err := os.Remove(alphaPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(usersTarget, alphaPath); err != nil {
		t.Fatal(err)
	}

	log.Reset()
	summary := syncOrFail(t, configFor(aliasDir), fixture, syncOptions{}, &log)
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	if got := readLink(t, alphaPath); got != usersTarget {
		t.Errorf("user's link changed to %q", got)
	}
	wantWarning := filepath.Join(aliasDir, "alpha") + " " + unmanagedWarningFragment
	if !strings.Contains(log.String(), wantWarning) {
		t.Errorf("log lacks %q: %q", wantWarning, log.String())
	}
	wantAdopted := "adopted 1 record(s) from " + fixture.agentDir + " into " + aliasDir
	if !strings.Contains(log.String(), wantAdopted) {
		t.Errorf("log lacks %q: %q", wantAdopted, log.String())
	}
	after := loadState(t, fixture.statePath)
	if _, adopted := after.Targets[aliasDir].Links["alpha"]; adopted {
		t.Error("an unverified record was adopted")
	}
	if got := after.Targets[fixture.agentDir].Links["alpha"]; got != filepath.Join(fixture.storeDir, "alpha") {
		t.Errorf("unverified record not left in the old key: %v", after.Targets[fixture.agentDir])
	}
	if _, left := after.Targets[fixture.agentDir].Links["beta"]; left {
		t.Error("adopted record still in the old key")
	}
	if got := after.Targets[aliasDir].Links["beta"]; got != filepath.Join(fixture.storeDir, "beta") {
		t.Errorf("beta not adopted: %v", after.Targets[aliasDir].Links)
	}
}

func TestDoSync_DryRunReportsAdoptionWithoutChangingState(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	stateBefore := readStateFile(t, fixture.statePath)
	namesBefore := listNames(t, fixture.agentDir)

	log.Reset()
	summary := syncOrFail(t, configFor(aliasDir), fixture, syncOptions{DryRun: true}, &log)
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	wantReport := "would adopt 2 record(s) from " + fixture.agentDir + " into " + aliasDir
	if !strings.Contains(log.String(), wantReport) {
		t.Errorf("log lacks %q: %q", wantReport, log.String())
	}
	if strings.Contains(log.String(), "adopted ") {
		t.Errorf("dry run claims an adoption happened: %q", log.String())
	}
	if strings.Contains(log.String(), unmanagedWarningFragment) {
		t.Errorf("dry run reports adoptable entries as unmanaged: %q", log.String())
	}
	if !bytes.Equal(stateBefore, readStateFile(t, fixture.statePath)) {
		t.Error("dry run changed the state file")
	}
	if got := listNames(t, fixture.agentDir); strings.Join(got, ",") != strings.Join(namesBefore, ",") {
		t.Errorf("dry run changed the agent dir: %v -> %v", namesBefore, got)
	}
	for _, name := range []string{"alpha", "beta"} {
		if got := readLink(t, filepath.Join(fixture.agentDir, name)); got != filepath.Join(fixture.storeDir, name) {
			t.Errorf("dry run changed %s to %q", name, got)
		}
	}
}

// A key that merely shares a prefix with the agent directory, and whose
// entries would verify there, is a different directory and must never be
// treated as an alias.
func TestDoSync_DoesNotAdoptFromDifferentDirectory(t *testing.T) {
	fixture := newHomeFixture(t, "alpha")
	alphaStore := filepath.Join(fixture.storeDir, "alpha")
	siblingDir := fixture.agentDir + "-old"
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, siblingDir, "alpha", alphaStore)
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixture.agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alphaStore, filepath.Join(fixture.agentDir, "alpha")); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	if strings.Contains(log.String(), "adopt") {
		t.Errorf("adopted from a different directory: %q", log.String())
	}
	if !strings.Contains(log.String(), unmanagedWarningFragment) {
		t.Errorf("hand-made link not reported unmanaged: %q", log.String())
	}
	after := loadState(t, fixture.statePath)
	if got := after.Targets[siblingDir].Links["alpha"]; got != alphaStore {
		t.Errorf("sibling key changed: %v", after.Targets[siblingDir])
	}
	if _, recorded := after.Targets[fixture.agentDir].Links["alpha"]; recorded {
		t.Error("hand-made link recorded as sm's")
	}
}

// The current key's own record wins over an alias's record for the same name,
// and of two aliases recording one name only the first (in key order) is
// adopted; the losing records are left for clean --orphans.
func TestDoSync_AdoptionNeverOverridesOrDuplicatesARecord(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	firstAlias := fixture.aliasOf(t)
	secondParent := filepath.Join(fixture.root, "second")
	if err := os.Symlink(filepath.Dir(fixture.agentDir), secondParent); err != nil {
		t.Fatal(err)
	}
	secondAlias := filepath.Join(secondParent, filepath.Base(fixture.agentDir))
	alphaStore := filepath.Join(fixture.storeDir, "alpha")
	betaStore := filepath.Join(fixture.storeDir, "beta")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, fixture.agentDir, "alpha", alphaStore)
	seedOwnedLink(t, current, fixture.agentDir, "beta", betaStore)
	current.Targets[firstAlias] = state.Target{Links: map[string]string{"alpha": alphaStore, "beta": betaStore}}
	current.Targets[secondAlias] = state.Target{Links: map[string]string{"beta": betaStore}}
	// The current key records only alpha; beta is held by both aliases.
	current.Targets[fixture.agentDir] = state.Target{Links: map[string]string{"alpha": alphaStore}}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	wantAdopted := "adopted 1 record(s) from " + firstAlias + " into " + fixture.agentDir
	if !strings.Contains(log.String(), wantAdopted) {
		t.Errorf("log lacks %q: %q", wantAdopted, log.String())
	}
	if strings.Contains(log.String(), "from "+secondAlias) {
		t.Errorf("second alias also adopted: %q", log.String())
	}
	after := loadState(t, fixture.statePath)
	if got := after.Targets[fixture.agentDir].Links; len(got) != 2 || got["alpha"] != alphaStore || got["beta"] != betaStore {
		t.Errorf("current key records %v", got)
	}
	if got := after.Targets[firstAlias].Links; len(got) != 1 || got["alpha"] != alphaStore {
		t.Errorf("first alias should keep only its overridden alpha record: %v", got)
	}
	if got := after.Targets[secondAlias].Links; len(got) != 1 || got["beta"] != betaStore {
		t.Errorf("second alias should keep its losing beta record: %v", got)
	}
	for _, name := range []string{"alpha", "beta"} {
		if got := readLink(t, filepath.Join(fixture.agentDir, name)); got != filepath.Join(fixture.storeDir, name) {
			t.Errorf("%s link = %q", name, got)
		}
	}
}

// The owner key's record for a name no longer verifies (it names a store path
// the entry does not point at) while an alias key's record does: the verified
// alias record replaces it, so one sync converges with no false warning.
func TestDoSync_VerifiedAliasRecordReplacesStaleOwnerRecord(t *testing.T) {
	fixture := newHomeFixture(t, "alpha")
	aliasDir := fixture.aliasOf(t)
	alphaStore := filepath.Join(fixture.storeDir, "alpha")
	staleStore := filepath.Join(fixture.root, "old-store", "alpha")
	current := loadState(t, fixture.statePath)
	seedOwnedLink(t, current, aliasDir, "alpha", alphaStore)
	current.Targets[fixture.agentDir] = state.Target{Links: map[string]string{"alpha": staleStore}}
	if err := current.Save(fixture.statePath); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer
	summary := syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	if summary != "sync: fake +0 -0" {
		t.Errorf("summary %q, want %q", summary, "sync: fake +0 -0")
	}
	if strings.Contains(log.String(), "warning") {
		t.Errorf("false warning: %q", log.String())
	}
	wantAdopted := "adopted 1 record(s) from " + aliasDir + " into " + fixture.agentDir
	if !strings.Contains(log.String(), wantAdopted) {
		t.Errorf("log lacks %q: %q", wantAdopted, log.String())
	}
	if got := readLink(t, filepath.Join(fixture.agentDir, "alpha")); got != alphaStore {
		t.Errorf("alpha link = %q", got)
	}
	after := loadState(t, fixture.statePath)
	if got := after.Targets[fixture.agentDir].Links; len(got) != 1 || got["alpha"] != alphaStore {
		t.Errorf("owner key records %v, want alpha -> %s", got, alphaStore)
	}
	if _, present := after.Targets[aliasDir]; present {
		t.Errorf("alias key not dropped: %v", after.Targets[aliasDir])
	}

	log.Reset()
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	if log.Len() != 0 {
		t.Errorf("second sync not quiet: %q", log.String())
	}
}

// Adopted records count for the empty-store guard: otherwise records only an
// old spelling held would be adopted and then pruned by an empty store.
func TestDoSync_EmptyStoreGuardCountsAdoptedRecords(t *testing.T) {
	fixture := newHomeFixture(t, "alpha", "beta")
	aliasDir := fixture.aliasOf(t)
	var log bytes.Buffer
	syncOrFail(t, configFor(fixture.agentDir), fixture, syncOptions{}, &log)
	emptyStore(t, fixture.syncFixture, "alpha", "beta")

	for _, options := range []syncOptions{{DryRun: true}, {}} {
		_, err := doSync(configFor(aliasDir), fixture.storeDir, fixture.statePath, options, &log)
		if err == nil || !strings.Contains(err.Error(), "--allow-empty") {
			t.Fatalf("%+v: expected the empty-store refusal, got %v", options, err)
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		if !isSymlink(t, filepath.Join(fixture.agentDir, name)) {
			t.Errorf("%s link removed despite the refusal", name)
		}
	}
	after := loadState(t, fixture.statePath)
	if got := after.Targets[aliasDir].Links; len(got) != 2 {
		t.Errorf("adoption before the refusal not persisted: %v", after.Targets)
	}
}

func TestDoSync_LinkModeRefusesWorldWritableAgentDir(t *testing.T) {
	requireNonRoot(t, "the refusal is checked against the test user's own directories")
	fixture := newSyncFixture(t, "alpha")
	openDir := filepath.Join(t.TempDir(), "zz-open", "skills")
	if err := os.MkdirAll(openDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(openDir, worldWritableDirMode); err != nil {
		t.Fatal(err)
	}
	fixture.cfg = agents.Config{Agents: []agents.Agent{
		{Name: "first", Dir: fixture.agentDir},
		{Name: "second", Dir: openDir},
	}}
	var log bytes.Buffer

	_, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{}, &log)
	wantText := "refusing target dir " + openDir + ": world-writable (fix with: chmod o-w '" + openDir + "')"
	wantHint := "sync stops at this agent: fix the directory, or remove agent second from agents.toml"
	if err == nil || !strings.Contains(err.Error(), "agent second") || !strings.Contains(err.Error(), wantText) || !strings.Contains(err.Error(), wantHint) {
		t.Fatalf("err = %v, want agent second refused with %q", err, wantText)
	}
	if names := listNames(t, openDir); len(names) != 0 {
		t.Errorf("links made in a world-writable dir: %v", names)
	}
	saved := loadState(t, fixture.statePath)
	if _, recorded := saved.Targets[fixture.agentDir].Links["alpha"]; !recorded {
		t.Error("first agent's progress was not persisted")
	}

	summary, err := doSync(fixture.cfg, fixture.storeDir, fixture.statePath, syncOptions{DryRun: true}, &log)
	if err != nil {
		t.Fatalf("dry run should not check the target dir: %v", err)
	}
	if summary != "sync: first +0 -0   second +1 -0" {
		t.Errorf("dry-run summary %q", summary)
	}
}
