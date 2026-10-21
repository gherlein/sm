package mscli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/ingest"
	"github.com/gherlein/sm/internal/state"
)

// TestMain points XDG_DATA_HOME at a throwaway directory so no test can touch
// the real ingest ledger; tests that care set their own value.
func TestMain(m *testing.M) {
	dataHome, err := os.MkdirTemp("", "ms-test-xdg-")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_DATA_HOME", dataHome); err != nil {
		panic(err)
	}
	code := m.Run()
	if err := os.RemoveAll(dataHome); err != nil {
		panic(err)
	}
	os.Exit(code)
}

func writeSourceSkill(t *testing.T, source, name, body string) {
	t.Helper()
	directory := filepath.Join(source, "skills", name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: test skill\n---\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sourceFixture(t *testing.T) string {
	t.Helper()
	source := filepath.Join(t.TempDir(), "claude")
	writeSourceSkill(t, source, "alpha", "from source")
	writeSourceSkill(t, source, "beta", "from source")
	return source
}

func run(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err == nil {
		t.Fatalf("expected %s to be absent", path)
	}
}

func TestRun_IngestsToExplicitDest(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, stdout, stderr := run(source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "+2") {
		t.Fatalf("summary missing +2: %q", stdout)
	}
	for _, name := range []string{"alpha", "beta"} {
		readFile(t, filepath.Join(destination, name, "SKILL.md"))
	}
}

func TestRun_DefaultsToStore(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	source := sourceFixture(t)

	code, _, stderr := run(source)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	readFile(t, filepath.Join(dataHome, "skills", "alpha", "SKILL.md"))
}

func existingAlpha(t *testing.T) string {
	t.Helper()
	destination := t.TempDir()
	directory := filepath.Join(destination, "alpha")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	return destination
}

func TestRun_SkipsCollisionWithoutForce(t *testing.T) {
	source := sourceFixture(t)
	destination := existingAlpha(t)

	code, stdout, stderr := run(source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(destination, "alpha", "SKILL.md")); got != "old" {
		t.Fatalf("alpha was overwritten: %q", got)
	}
	readFile(t, filepath.Join(destination, "beta", "SKILL.md"))
	if !strings.Contains(stderr, "alpha") {
		t.Fatalf("expected collision warning for alpha: %q", stderr)
	}
	if !strings.Contains(stdout, "+1") || !strings.Contains(stdout, "skipped 1") {
		t.Fatalf("unexpected summary: %q", stdout)
	}
}

func TestRun_ForceOverwrites(t *testing.T) {
	source := sourceFixture(t)
	destination := existingAlpha(t)

	code, _, stderr := run("--force", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(destination, "alpha", "SKILL.md")); got == "old" {
		t.Fatal("alpha was not replaced")
	}
}

func TestRun_DryRunWritesNothing(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, stdout, stderr := run("--dry-run", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	assertAbsent(t, destination)
	if !strings.Contains(stdout, "+2") {
		t.Fatalf("dry-run summary missing +2: %q", stdout)
	}
}

func TestRun_UnknownFormatErrors(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, _, stderr := run("--format", "copilot", source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	if stderr == "" {
		t.Fatal("expected error on stderr")
	}
	assertAbsent(t, destination)
}

func TestRun_DestInsideSourceRefused(t *testing.T) {
	source := sourceFixture(t)
	for _, destination := range []string{source, filepath.Join(source, "out")} {
		code, _, stderr := run(source, destination)
		if code != exitError {
			t.Fatalf("dest %s: exit %d, want %d", destination, code, exitError)
		}
		if !strings.Contains(stderr, "overlaps the source") {
			t.Fatalf("stderr = %q", stderr)
		}
	}
	assertAbsent(t, filepath.Join(source, "out"))
}

func TestRun_SourceMustBeDirectory(t *testing.T) {
	code, _, stderr := run(filepath.Join(t.TempDir(), "missing"))
	if code != exitError || stderr == "" {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
}

func TestRun_NoSourceUsage(t *testing.T) {
	code, _, stderr := run()
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "usage") {
		t.Fatalf("usage missing: %q", stderr)
	}
}

func TestRun_FlagAfterPositionalsDryRun(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, stdout, stderr := run(source, destination, "--dry-run")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	assertAbsent(t, destination)
	if !strings.Contains(stdout, "+2") {
		t.Fatalf("dry-run summary missing +2: %q", stdout)
	}
}

func TestRun_FlagBetweenPositionalsForce(t *testing.T) {
	source := sourceFixture(t)
	destination := existingAlpha(t)

	code, _, stderr := run(source, "--force", destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(destination, "alpha", "SKILL.md")); got == "old" {
		t.Fatal("force after source did not replace alpha")
	}
	readFile(t, filepath.Join(destination, "beta", "SKILL.md"))
}

func TestRun_DoubleDashEndsFlagParsing(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	source := sourceFixture(t)

	code, _, stderr := run("--", source, "-weird")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	readFile(t, filepath.Join(workDir, "-weird", "alpha", "SKILL.md"))
}

func TestRun_DoubleDashDoesNotForce(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	oldAlpha := filepath.Join(dataHome, "skills", "alpha")
	if err := os.MkdirAll(oldAlpha, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldAlpha, "SKILL.md"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	source := sourceFixture(t)

	code, _, stderr := run("--", source, "--force")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(oldAlpha, "SKILL.md")); got != "old" {
		t.Fatalf("default store was forced: %q", got)
	}
	readFile(t, filepath.Join(workDir, "--force", "alpha", "SKILL.md"))
}

func TestRun_FlagsBeforeDoubleDashStillParsed(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")
	code, _, stderr := run("--dry-run", "--", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	assertAbsent(t, destination)
}

func TestRun_HelpExitsZeroWithPositionals(t *testing.T) {
	for _, flagName := range []string{"-h", "--help"} {
		code, stdout, stderr := run(flagName)
		if code != 0 {
			t.Fatalf("%s: exit %d", flagName, code)
		}
		if stderr != "" {
			t.Fatalf("%s: stderr should be empty: %q", flagName, stderr)
		}
		for _, want := range []string{
			"<source> [dest]", "--format", "--force", "--dry-run", "-v", "--verbose",
			"--follow-symlinks", "ingest skills reached through symlinks, including ones outside the source tree",
		} {
			if !strings.Contains(stdout, want) {
				t.Fatalf("%s: usage missing %q:\n%s", flagName, want, stdout)
			}
		}
	}
}

func TestRun_DoesNotExpandEnvInPaths(t *testing.T) {
	t.Setenv("MS_TEST_VAR", "expanded")
	source := sourceFixture(t)
	root := t.TempDir()
	destination := filepath.Join(root, "p$MS_TEST_VAR")

	code, _, stderr := run(source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	readFile(t, filepath.Join(destination, "alpha", "SKILL.md"))
	assertAbsent(t, filepath.Join(root, "pexpanded"))
}

func TestRun_SkippedCountIncludesDuplicatesAndInvalid(t *testing.T) {
	source := sourceFixture(t)
	duplicateDirectory := filepath.Join(source, "skills", "alpha-copy")
	if err := os.MkdirAll(duplicateDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(duplicateDirectory, "SKILL.md"), []byte("---\nname: alpha\ndescription: dup\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	invalidDirectory := filepath.Join(source, "skills", "bad")
	if err := os.MkdirAll(invalidDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(invalidDirectory, "SKILL.md"), []byte("---\nname: Bad Name\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := run(source, filepath.Join(t.TempDir(), "store"))
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if stdout != "ms: +2 (skipped 2)\n" {
		t.Fatalf("summary = %q", stdout)
	}
}

func linkedSourceFixture(t *testing.T) (source, destination string) {
	t.Helper()
	root := t.TempDir()
	source = filepath.Join(root, "src")
	writeSourceSkill(t, source, "alpha", "inside")
	outside := filepath.Join(root, "outside")
	writeSourceSkill(t, outside, "gamma", "outside")
	if err := os.Symlink(filepath.Join(outside, "skills", "gamma"), filepath.Join(source, "skills", "gamma-link")); err != nil {
		t.Fatal(err)
	}
	return source, filepath.Join(root, "store")
}

func TestRun_FollowSymlinksFlag(t *testing.T) {
	source, destination := linkedSourceFixture(t)

	code, stdout, stderr := run(source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	assertAbsent(t, filepath.Join(destination, "gamma"))
	if !strings.Contains(stderr, "gamma-link") {
		t.Fatalf("expected a warning naming the skipped symlink: %q", stderr)
	}
	if !strings.Contains(stdout, "skipped 1") {
		t.Fatalf("summary = %q", stdout)
	}

	followed := filepath.Join(filepath.Dir(destination), "store-followed")
	code, stdout, stderr = run("--follow-symlinks", source, followed)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	readFile(t, filepath.Join(followed, "gamma", "SKILL.md"))
	if !strings.Contains(stdout, "+2") {
		t.Fatalf("summary = %q", stdout)
	}
}

func TestRun_SymlinkedSourceRootIngests(t *testing.T) {
	source := sourceFixture(t)
	linkedRoot := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(source, linkedRoot); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "store")

	code, stdout, stderr := run(linkedRoot, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if stdout != "ms: +2 (skipped 0)\n" {
		t.Fatalf("summary = %q", stdout)
	}
	readFile(t, filepath.Join(destination, "alpha", "SKILL.md"))
}

func TestRun_WritesIngestLedger(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")
	ledgerPath := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")

	code, _, stderr := run("--dry-run", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	assertAbsent(t, ledgerPath)
	assertAbsent(t, ledgerPath+".lock")

	code, _, stderr = run(source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	ledger, err := ingest.LoadLedger(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Records) != 2 {
		t.Fatalf("records = %+v, want 2", ledger.Records)
	}
	resolvedSource, err := filepath.EvalSymlinks(source)
	if err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"alpha", "beta"} {
		record := ledger.Records[index]
		if record.Name != name || record.SourceRoot != resolvedSource || record.FromPlugin || record.At.IsZero() ||
			record.SourceDir != filepath.Join(resolvedSource, "skills", name) {
			t.Fatalf("record %d = %+v", index, record)
		}
	}
	assertEntriesOnly(t, destination, "alpha", "beta")
}

func assertEntriesOnly(t *testing.T, directory string, want ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries of %s = %v, want %v", directory, got, want)
	}
}

func TestRun_PluginNoticeOnStderr(t *testing.T) {
	source := sourceFixture(t)
	pluginSkill := filepath.Join(source, "plugins", "acme", "skills", "gamma")
	if err := os.MkdirAll(pluginSkill, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginSkill, "SKILL.md"), []byte("---\nname: gamma\ndescription: plugin\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const notice = "note: 1 ingested skill(s) came from plugin directories; review third-party skills before enabling them for your agents\n"

	code, _, stderr := run("--dry-run", source, filepath.Join(t.TempDir(), "dry"))
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if strings.Contains(stderr, "note:") {
		t.Fatalf("dry-run wrote nothing, so no notice expected: %q", stderr)
	}

	code, stdout, stderr := run("-v", source, filepath.Join(t.TempDir(), "store"))
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, notice) {
		t.Fatalf("notice missing: %q", stderr)
	}
	if !strings.Contains(stdout, "copied gamma -> ") || !strings.Contains(stdout, "/gamma (plugin)") {
		t.Fatalf("verbose line for plugin skill missing: %q", stdout)
	}

	plain := sourceFixture(t)
	code, _, stderr = run(plain, filepath.Join(t.TempDir(), "store"))
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if strings.Contains(stderr, "note:") {
		t.Fatalf("notice must be absent without plugin skills: %q", stderr)
	}
}

func TestRun_VerboseLinePerSkill(t *testing.T) {
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")
	code, stdout, stderr := run("-v", "--dry-run", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	for _, name := range []string{"alpha", "beta"} {
		want := "would-copy " + name + " -> " + filepath.Join(destination, name) + "\n"
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing %q in %q", want, stdout)
		}
	}
}

func TestRun_SecondConcurrentRunFailsFast(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	ledgerPath, err := ingest.LedgerPath()
	if err != nil {
		t.Fatal(err)
	}
	unlock, err := state.Lock(ledgerPath, ledgerLockHolder)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := unlock(); err != nil {
			t.Fatal(err)
		}
	}()
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, _, stderr := run(source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "another ms process is running (lock "+ledgerPath+".lock)") {
		t.Fatalf("stderr = %q", stderr)
	}
	assertAbsent(t, destination)
}

func TestRun_DoubleDashAfterValueTakingFlagIsUsageError(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	source := sourceFixture(t)

	code, stdout, stderr := run("--format", "--", source)
	if code != exitUsage {
		t.Fatalf("exit %d, want %d; stderr: %s", code, exitUsage, stderr)
	}
	if !strings.Contains(stderr, "flag needs an argument") || !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr = %q", stderr)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q", stdout)
	}
	assertAbsent(t, filepath.Join(dataHome, "skills"))
}

func TestRun_BareDoubleDashIsUsageError(t *testing.T) {
	code, _, stderr := run("--")
	if code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr, "usage:") {
		t.Fatalf("stderr = %q", stderr)
	}
}

func TestRun_DoubleDashAllowsSourceStartingWithDash(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	writeSourceSkill(t, filepath.Join(workDir, "-x"), "alpha", "dash source")
	destination := filepath.Join(t.TempDir(), "store")

	code, stdout, stderr := run("--", "-x", destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if stdout != "ms: +1 (skipped 0)\n" {
		t.Fatalf("summary = %q", stdout)
	}
	readFile(t, filepath.Join(destination, "alpha", "SKILL.md"))
}

func TestRun_ForceBeforeDoubleDashStillForces(t *testing.T) {
	source := sourceFixture(t)
	destination := existingAlpha(t)

	code, _, stderr := run("--force", "--", source, destination)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(destination, "alpha", "SKILL.md")); got == "old" {
		t.Fatal("--force before -- was ignored")
	}
}

func TestRun_FlagsAfterDoubleDashAreLiteral(t *testing.T) {
	workDir := t.TempDir()
	t.Chdir(workDir)
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	source := sourceFixture(t)

	code, _, stderr := run(source, "--", "dest")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	readFile(t, filepath.Join(workDir, "dest", "alpha", "SKILL.md"))

	destination := existingAlpha(t)
	t.Chdir(destination)
	code, _, stderr = run("--", source, "--force")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, stderr)
	}
	if got := readFile(t, filepath.Join(destination, "alpha", "SKILL.md")); got != "old" {
		t.Fatalf("--force after -- forced a write: %q", got)
	}
	readFile(t, filepath.Join(destination, "--force", "alpha", "SKILL.md"))
}

func TestRun_CorruptLedgerFailsClosedBeforeWritingAnything(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	ledgerPath := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const corrupt = "{not json"
	if err := os.WriteFile(ledgerPath, []byte(corrupt), 0o600); err != nil {
		t.Fatal(err)
	}
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, _, stderr := run(source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "parse ingest ledger") {
		t.Fatalf("stderr = %q", stderr)
	}
	assertAbsent(t, destination)
	if got := readFile(t, ledgerPath); got != corrupt {
		t.Fatalf("corrupt ledger was modified: %q", got)
	}
}

func TestRun_UnsupportedLedgerVersionFailsClosed(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	ledgerPath := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")
	if err := os.MkdirAll(filepath.Dir(ledgerPath), 0o700); err != nil {
		t.Fatal(err)
	}
	const future = `{"version": 99, "records": []}`
	if err := os.WriteFile(ledgerPath, []byte(future), 0o600); err != nil {
		t.Fatal(err)
	}
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, _, stderr := run(source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr, "unsupported version 99") {
		t.Fatalf("stderr = %q", stderr)
	}
	assertAbsent(t, destination)
	if got := readFile(t, ledgerPath); got != future {
		t.Fatalf("ledger was modified: %q", got)
	}
}

func TestRun_LedgerSaveFailureSaysSkillsWereWritten(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user: root ignores directory permissions")
	}
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	ledgerPath := filepath.Join(dataHome, "skills-mapper", "ingest-log.json")
	ledgerDirectory := filepath.Dir(ledgerPath)
	if err := os.MkdirAll(ledgerDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	// The lock files (ms takes sm's state lock and its own ledger lock)
	// already exist so state.Lock succeeds in a directory that is then
	// read-only, which makes only the ledger save fail.
	for _, lockPath := range []string{ledgerPath + ".lock", filepath.Join(ledgerDirectory, "state.json.lock")} {
		if err := os.WriteFile(lockPath, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(ledgerDirectory, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(ledgerDirectory, 0o700); err != nil {
			t.Error(err)
		}
	})
	source := sourceFixture(t)
	destination := filepath.Join(t.TempDir(), "store")

	code, _, stderr := run(source, destination)
	if code != exitError {
		t.Fatalf("exit %d, want %d", code, exitError)
	}
	want := "skills were written to " + destination + " but the ingest log could not be saved: "
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr = %q, want it to contain %q", stderr, want)
	}
	readFile(t, filepath.Join(destination, "alpha", "SKILL.md"))
}
