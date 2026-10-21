package mscli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/mscli"
)

const (
	exitOK     = 0
	exitError  = 1
	exitUsage  = 2
	markerName = ".sm-copy"
)

var conformingName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

func runMS(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := mscli.Run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func skillContent(name string) string {
	return "---\nname: " + name + "\n---\n# body\n"
}

// writeSkill creates <dir>/SKILL.md with the given frontmatter name.
func writeSkill(t *testing.T, dir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(dir, "SKILL.md"), skillContent(name))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func listDir(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func assertEntries(t *testing.T, dir string, want ...string) {
	t.Helper()
	sort.Strings(want)
	got := listDir(t, dir)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries of %s = %v, want %v", dir, got, want)
	}
}

func assertNoMarkers(t *testing.T, root string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Name() == markerName {
			t.Errorf("provenance marker found: %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertEmptyOrAbsent(t *testing.T, dir string) {
	t.Helper()
	if !exists(dir) {
		return
	}
	if names := listDir(t, dir); len(names) != 0 {
		t.Fatalf("%s should be empty, has %v", dir, names)
	}
}

func assertExit(t *testing.T, got, want int, stdout, stderr string) {
	t.Helper()
	if got != want {
		t.Fatalf("exit = %d, want %d\nstdout: %s\nstderr: %s", got, want, stdout, stderr)
	}
}

// assertRefused checks the guard's contract: exit 1 and a stderr message
// naming the refusal, not merely any non-zero exit.
func assertRefused(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	assertExit(t, code, exitError, stdout, stderr)
	if !strings.Contains(stderr, "refusing") || !strings.Contains(stderr, "source") {
		t.Errorf("stderr does not describe the refusal: %q", stderr)
	}
}

func TestRealisticTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "skills", "foo"), "foo")
	writeSkill(t, filepath.Join(source, "plugins", "p", "skills", "bar"), "bar")
	writeFile(t, filepath.Join(source, "skills", "noname", "SKILL.md"), "---\ndescription: no name here\n---\n# body\n")
	writeSkill(t, filepath.Join(source, "node_modules", "x"), "x")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo", "bar")
	for _, name := range []string{"foo", "bar"} {
		if got := readFile(t, filepath.Join(dest, name, "SKILL.md")); got != skillContent(name) {
			t.Errorf("%s SKILL.md = %q", name, got)
		}
	}
	assertNoMarkers(t, dest)
	if !strings.Contains(stdout, "+2") {
		t.Errorf("summary should contain +2, got %q", stdout)
	}
	if !strings.Contains(stderr, "noname") {
		t.Errorf("expected warning mentioning noname, stderr=%q", stderr)
	}
}

func TestNormalizationToFrontmatterName(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	dir := filepath.Join(source, "Weird-Dir")
	writeSkill(t, dir, "weird")
	writeFile(t, filepath.Join(dir, "extra.txt"), "extra")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "weird")
	if got := readFile(t, filepath.Join(dest, "weird", "SKILL.md")); got != skillContent("weird") {
		t.Errorf("SKILL.md = %q", got)
	}
	if got := readFile(t, filepath.Join(dest, "weird", "extra.txt")); got != "extra" {
		t.Errorf("extra.txt = %q", got)
	}
}

func TestBundledContentPreserved(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	dir := filepath.Join(source, "foo")
	writeSkill(t, dir, "foo")
	writeFile(t, filepath.Join(dir, "scripts", "run.sh"), "#!/bin/sh\necho hi\n")
	writeFile(t, filepath.Join(dir, "scripts", "deep", "data.json"), `{"a":1}`)
	writeFile(t, filepath.Join(dir, "README.txt"), "readme")
	writeFile(t, filepath.Join(dir, "empty.txt"), "")
	if err := os.Chmod(filepath.Join(dir, "scripts", "run.sh"), 0o755); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	for rel, want := range map[string]string{
		"SKILL.md":               skillContent("foo"),
		"scripts/run.sh":         "#!/bin/sh\necho hi\n",
		"scripts/deep/data.json": `{"a":1}`,
		"README.txt":             "readme",
		"empty.txt":              "",
	} {
		if got := readFile(t, filepath.Join(dest, "foo", rel)); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(dest, "foo", "scripts", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("executable bit lost: %v", info.Mode())
	}
	assertNoMarkers(t, dest)
}

func TestDefaultDest(t *testing.T) {
	root := t.TempDir()
	xdg := filepath.Join(root, "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("HOME", filepath.Join(root, "home"))
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, filepath.Join(xdg, "skills"), "foo")
	if got := readFile(t, filepath.Join(xdg, "skills", "foo", "SKILL.md")); got != skillContent("foo") {
		t.Errorf("SKILL.md = %q", got)
	}
}

func TestDefaultDestFallsBackToHome(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, filepath.Join(home, ".local", "share", "skills"), "foo")
}

func TestExplicitDestLeavesDefaultUntouched(t *testing.T) {
	root := t.TempDir()
	xdg := filepath.Join(root, "xdg")
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("HOME", filepath.Join(root, "home"))
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo")
	if exists(filepath.Join(xdg, "skills")) {
		t.Errorf("default store was touched")
	}
}

func collisionFixture(t *testing.T) (source, dest string) {
	t.Helper()
	root := t.TempDir()
	source = filepath.Join(root, "src")
	dest = filepath.Join(root, "dest")
	writeFile(t, filepath.Join(source, "foo", "SKILL.md"), "---\nname: foo\n---\n# NEW\n")
	writeSkill(t, filepath.Join(source, "bar"), "bar")
	writeSkill(t, filepath.Join(dest, "foo"), "foo")
	writeFile(t, filepath.Join(dest, "foo", "old-sentinel.txt"), "old")
	return source, dest
}

func TestCollisionSkippedWithoutForce(t *testing.T) {
	source, dest := collisionFixture(t)
	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo", "bar")
	if got := readFile(t, filepath.Join(dest, "foo", "old-sentinel.txt")); got != "old" {
		t.Errorf("existing foo was modified")
	}
	if got := readFile(t, filepath.Join(dest, "foo", "SKILL.md")); got != skillContent("foo") {
		t.Errorf("existing foo SKILL.md overwritten: %q", got)
	}
	if !strings.Contains(stderr, "foo") {
		t.Errorf("expected warning for foo, stderr=%q", stderr)
	}
	if !strings.Contains(stdout, "+1") {
		t.Errorf("summary should contain +1, got %q", stdout)
	}
}

func TestCollisionReplacedWithForce(t *testing.T) {
	source, dest := collisionFixture(t)
	code, stdout, stderr := runMS(t, "--force", source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo", "bar")
	if exists(filepath.Join(dest, "foo", "old-sentinel.txt")) {
		t.Errorf("old sentinel survived --force")
	}
	if got := readFile(t, filepath.Join(dest, "foo", "SKILL.md")); !strings.Contains(got, "# NEW") {
		t.Errorf("source content not present: %q", got)
	}
	assertEntries(t, filepath.Join(dest, "foo"), "SKILL.md")
	assertNoMarkers(t, dest)
}

func TestDedupeAcrossSourceTree(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeFile(t, filepath.Join(source, "a", "SKILL.md"), "---\nname: dup\n---\n# FIRST\n")
	writeFile(t, filepath.Join(source, "b", "SKILL.md"), "---\nname: dup\n---\n# SECOND\n")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "dup")
	if got := readFile(t, filepath.Join(dest, "dup", "SKILL.md")); !strings.Contains(got, "# FIRST") {
		t.Errorf("first discovered (lexical) should win, got %q", got)
	}
	if n := strings.Count(stderr, "dup"); n < 1 {
		t.Errorf("expected a warning about dup, stderr=%q", stderr)
	}
	if lines := strings.Count(strings.TrimSpace(stderr), "\n") + 1; strings.TrimSpace(stderr) == "" || lines != 1 {
		t.Errorf("expected exactly one warning line, stderr=%q", stderr)
	}
}

func TestDryRun(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	writeSkill(t, filepath.Join(source, "bar"), "bar")

	code, stdout, stderr := runMS(t, "--dry-run", source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	if exists(dest) {
		t.Errorf("dest created during dry-run")
	}
	if !strings.Contains(stdout, "+2") {
		t.Errorf("summary should still be computed (+2), got %q", stdout)
	}
}

func TestDryRunWithExistingDestSkill(t *testing.T) {
	source, dest := collisionFixture(t)
	for _, flags := range [][]string{{"--dry-run"}, {"--dry-run", "--force"}} {
		code, stdout, stderr := runMS(t, append(flags, source, dest)...)
		assertExit(t, code, exitOK, stdout, stderr)
		assertEntries(t, dest, "foo")
		assertEntries(t, filepath.Join(dest, "foo"), "SKILL.md", "old-sentinel.txt")
		if got := readFile(t, filepath.Join(dest, "foo", "SKILL.md")); got != skillContent("foo") {
			t.Errorf("flags %v modified existing skill: %q", flags, got)
		}
	}
}

func TestStopAtSkill(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "outer"), "outer")
	writeSkill(t, filepath.Join(source, "outer", "sub"), "sub")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "outer")
	if got := readFile(t, filepath.Join(dest, "outer", "sub", "SKILL.md")); got != skillContent("sub") {
		t.Errorf("bundled nested content should be copied with outer skill, got %q", got)
	}
}

func TestPrunedDirectories(t *testing.T) {
	for _, pruned := range []string{".git", "node_modules", "vendor", "dist", "build"} {
		t.Run(pruned, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "src")
			dest := filepath.Join(root, "dest")
			writeSkill(t, filepath.Join(source, "keep"), "keep")
			writeSkill(t, filepath.Join(source, pruned, "hidden"), "hidden")
			writeSkill(t, filepath.Join(source, "nest", pruned, "deep"), "deep")

			code, stdout, stderr := runMS(t, source, dest)
			assertExit(t, code, exitOK, stdout, stderr)
			assertEntries(t, dest, "keep")
		})
	}
}

func TestSymlinkedDirectoryNotFollowed(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	outside := filepath.Join(root, "outside")
	writeSkill(t, filepath.Join(source, "keep"), "keep")
	writeSkill(t, filepath.Join(outside, "linked"), "linked")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "linked"), filepath.Join(source, "linkskill")); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "keep")
}

func TestRefusesDestEqualsSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, source)
	assertRefused(t, code, stdout, stderr)
	assertEntries(t, source, "foo")
	assertEntries(t, filepath.Join(source, "foo"), "SKILL.md")
}

func TestRefusesDestInsideSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	dest := filepath.Join(source, "out")

	code, stdout, stderr := runMS(t, source, dest)
	assertRefused(t, code, stdout, stderr)
	if exists(dest) {
		t.Errorf("dest inside source was created")
	}
	assertEntries(t, source, "foo")
}

func TestRefusesDestEqualsSourceViaDotSegments(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, filepath.Join(source, "foo", ".."))
	assertRefused(t, code, stdout, stderr)
	assertEntries(t, source, "foo")
}

func TestRefusesDestSymlinkIntoSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	link := filepath.Join(root, "alias")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, source, link)
	assertRefused(t, code, stdout, stderr)
	assertEntries(t, source, "foo")
}

// The self-overwrite hazard the guard exists for: with --force and a colliding
// name, a symlinked dest would replace the source skill with itself.
func TestRefusesForceWithDestSymlinkIntoSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeFile(t, filepath.Join(source, "foo", "SKILL.md"), "---\nname: foo\n---\n# ORIGINAL\n")
	link := filepath.Join(root, "alias")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, "--force", source, link)
	assertRefused(t, code, stdout, stderr)
	if got := readFile(t, filepath.Join(source, "foo", "SKILL.md")); got != "---\nname: foo\n---\n# ORIGINAL\n" {
		t.Errorf("source skill modified: %q", got)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link is no longer a symlink: %v", err)
	}
	assertEntries(t, source, "foo")
}

// Exercises resolveExisting re-appending a non-existent tail beneath a symlink.
func TestRefusesNonexistentDestTailThroughSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	link := filepath.Join(root, "alias")
	if err := os.Symlink(source, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, source, filepath.Join(link, "newstore"))
	assertRefused(t, code, stdout, stderr)
	if exists(filepath.Join(source, "newstore")) {
		t.Errorf("newstore was created under source")
	}
	assertEntries(t, source, "foo")
}

func TestRefusesDestSymlinkToSourceSubdirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	subdirectory := filepath.Join(source, "sub")
	if err := os.MkdirAll(subdirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "alias")
	if err := os.Symlink(subdirectory, link); err != nil {
		t.Fatal(err)
	}

	code, stdout, stderr := runMS(t, source, link)
	assertRefused(t, code, stdout, stderr)
	assertEntries(t, source, "foo", "sub")
	assertEmptyOrAbsent(t, subdirectory)
}

func TestUnknownFormat(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, "--format", "copilot", source, dest)
	assertExit(t, code, exitError, stdout, stderr)
	if stderr == "" {
		t.Errorf("expected error on stderr")
	}
	assertEmptyOrAbsent(t, dest)
}

func TestExplicitClaudeCodeFormat(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, "--format", "claude-code", source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo")
}

func TestBadSource(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "dest")
	regular := filepath.Join(root, "file.txt")
	writeFile(t, regular, "not a dir")

	for name, source := range map[string]string{
		"nonexistent":  filepath.Join(root, "missing"),
		"regular file": regular,
	} {
		t.Run(name, func(t *testing.T) {
			code, stdout, stderr := runMS(t, source, dest)
			assertExit(t, code, exitError, stdout, stderr)
			if stderr == "" {
				t.Errorf("expected error on stderr")
			}
			assertEmptyOrAbsent(t, dest)
		})
	}
}

func TestUsageNoSource(t *testing.T) {
	code, stdout, stderr := runMS(t)
	assertExit(t, code, exitUsage, stdout, stderr)
	if stderr == "" {
		t.Errorf("expected usage on stderr")
	}
}

func TestUsageOnlyFlagsNoSource(t *testing.T) {
	code, stdout, stderr := runMS(t, "--dry-run")
	assertExit(t, code, exitUsage, stdout, stderr)
}

func TestUsageTooManyPositionals(t *testing.T) {
	root := t.TempDir()
	code, stdout, stderr := runMS(t, root, filepath.Join(root, "a"), filepath.Join(root, "b"))
	assertExit(t, code, exitUsage, stdout, stderr)
}

func TestUsageUnknownFlag(t *testing.T) {
	root := t.TempDir()
	code, stdout, stderr := runMS(t, "--bogus", root)
	assertExit(t, code, exitUsage, stdout, stderr)
}

func TestDryRunAfterPositionals(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, dest, "--dry-run")
	assertExit(t, code, exitOK, stdout, stderr)
	if exists(dest) {
		t.Errorf("trailing --dry-run ignored; dest created")
	}
}

func TestForceBetweenPositionals(t *testing.T) {
	source, dest := collisionFixture(t)
	code, stdout, stderr := runMS(t, source, "--force", dest)
	assertExit(t, code, exitOK, stdout, stderr)
	if exists(filepath.Join(dest, "foo", "old-sentinel.txt")) {
		t.Errorf("interspersed --force ignored")
	}
}

func TestVerboseFlags(t *testing.T) {
	for _, flag := range []string{"-v", "--verbose"} {
		t.Run(flag, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "src")
			dest := filepath.Join(root, "dest")
			writeSkill(t, filepath.Join(source, "foo"), "foo")
			code, stdout, stderr := runMS(t, source, dest, flag)
			assertExit(t, code, exitOK, stdout, stderr)
			assertEntries(t, dest, "foo")
			if want := "copied foo -> " + filepath.Join(dest, "foo") + "\n"; !strings.Contains(stdout, want) {
				t.Errorf("verbose output %q missing %q", stdout, want)
			}
		})
	}
}

func TestOutputConformance(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "Weird-Dir"), "weird")
	writeSkill(t, filepath.Join(source, "x", "y", "z"), "zed-9")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	for _, name := range listDir(t, dest) {
		skillDir := filepath.Join(dest, name)
		info, err := os.Lstat(skillDir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("%s is not a real directory", skillDir)
		}
		if !conformingName.MatchString(name) {
			t.Errorf("dest name %q violates charset", name)
		}
		content := readFile(t, filepath.Join(skillDir, "SKILL.md"))
		if !strings.Contains(content, "name: "+name+"\n") {
			t.Errorf("dir %q != frontmatter name in %q", name, content)
		}
		if exists(filepath.Join(skillDir, markerName)) {
			t.Errorf("%s has provenance marker", skillDir)
		}
	}
	assertEntries(t, dest, "weird", "zed-9", "foo")
}

func TestInvalidFrontmatterNamesSkipped(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "good"), "good")
	for dir, name := range map[string]string{
		"upper":    "Upper",
		"space":    "has space",
		"leading":  "-lead",
		"trailing": "trail-",
		"slash":    "a/b",
		"dotdot":   "..",
		"under":    "a_b",
	} {
		writeFile(t, filepath.Join(source, dir, "SKILL.md"), "---\nname: "+name+"\n---\n# body\n")
	}
	writeFile(t, filepath.Join(source, "nofm", "SKILL.md"), "# no frontmatter at all\n")
	writeFile(t, filepath.Join(source, "emptyname", "SKILL.md"), "---\nname:\n---\n# body\n")
	writeFile(t, filepath.Join(source, "emptyfile", "SKILL.md"), "")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "good")
	if !strings.Contains(stdout, "+1") {
		t.Errorf("summary should contain +1, got %q", stdout)
	}
	if !exists(filepath.Join(root, "src", "upper", "SKILL.md")) {
		t.Errorf("source modified")
	}
	if exists(filepath.Join(root, "Upper")) || exists(filepath.Join(root, "a")) {
		t.Errorf("path traversal artifacts written outside dest")
	}
}

func TestEmptySourceWritesNothing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEmptyOrAbsent(t, dest)
	if !strings.Contains(stdout, "+0") {
		t.Errorf("summary should contain +0, got %q", stdout)
	}
}

func TestSourceItselfIsSkill(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, source, "rootskill")
	writeFile(t, filepath.Join(source, "extra.txt"), "x")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "rootskill")
	if got := readFile(t, filepath.Join(dest, "rootskill", "extra.txt")); got != "x" {
		t.Errorf("extra.txt = %q", got)
	}
}

func TestIdempotentSecondRun(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	code, stdout, stderr = runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo")
	if !strings.Contains(stdout, "+0") {
		t.Errorf("second run should write 0, got %q", stdout)
	}
}

func TestForceLeavesNoStagingArtifacts(t *testing.T) {
	source, dest := collisionFixture(t)
	code, stdout, stderr := runMS(t, "--force", source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "foo", "bar")
}

func TestSourceNotModified(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	writeFile(t, filepath.Join(source, "foo", "a.txt"), "a")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, filepath.Join(source, "foo"), "SKILL.md", "a.txt")
}

func TestDestExistingAsRegularFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "src")
	dest := filepath.Join(root, "dest")
	writeSkill(t, filepath.Join(source, "foo"), "foo")
	writeFile(t, dest, "i am a file")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitError, stdout, stderr)
	// The destination itself is unusable, so the run stops once on it before
	// any skill is attempted, rather than failing every skill in turn.
	want := "ms: create target dir " + dest + ": mkdir " + dest + ": not a directory\n"
	if stderr != want {
		t.Errorf("stderr = %q, want exactly %q", stderr, want)
	}
	if got := readFile(t, dest); got != "i am a file" {
		t.Errorf("dest file modified: %q", got)
	}
}

func TestUnicodeAndSpacesInPaths(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "sörce dir")
	dest := filepath.Join(root, "dëst dir")
	writeSkill(t, filepath.Join(source, "ünï cøde"), "uni")
	writeFile(t, filepath.Join(source, "ünï cøde", "fïle näme.txt"), "u")

	code, stdout, stderr := runMS(t, source, dest)
	assertExit(t, code, exitOK, stdout, stderr)
	assertEntries(t, dest, "uni")
	if got := readFile(t, filepath.Join(dest, "uni", "fïle näme.txt")); got != "u" {
		t.Errorf("bundled unicode file = %q", got)
	}
}
