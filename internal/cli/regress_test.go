package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gherlein/sm/internal/cli"
)

// Black-box regression suite for the 2026-10-20 review findings. Every test
// drives cli.Run only and asserts observable filesystem state, exit codes and
// output substrings taken from the behavior contract.

const (
	regressExitOK    = 0
	regressExitError = 1
	regressExitUsage = 2

	regressTimeout = 20 * time.Second

	regressDirMode  os.FileMode = 0o755
	regressFileMode os.FileMode = 0o644
)

type regressEnv struct {
	root      string
	home      string
	configDir string
	dataDir   string
	store     string
	statePath string
}

func newRegressEnv(t *testing.T) *regressEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	env := &regressEnv{
		root:      root,
		home:      filepath.Join(root, "home"),
		configDir: filepath.Join(root, "config"),
		dataDir:   filepath.Join(root, "data"),
	}
	env.store = filepath.Join(env.dataDir, "skills")
	env.statePath = filepath.Join(env.dataDir, "skills-mapper", "state.json")
	regressMkdir(t, env.home)
	regressMkdir(t, env.configDir)
	regressMkdir(t, env.store)
	t.Setenv("HOME", env.home)
	t.Setenv("XDG_CONFIG_HOME", env.configDir)
	t.Setenv("XDG_DATA_HOME", env.dataDir)
	return env
}

func (env *regressEnv) agentsPath() string {
	return filepath.Join(env.configDir, "skills-mapper", "agents.toml")
}

func (env *regressEnv) writeAgents(t *testing.T, body string) {
	t.Helper()
	regressMkdir(t, filepath.Dir(env.agentsPath()))
	regressWrite(t, env.agentsPath(), body)
}

// agentDir is the absolute skills directory for an agent configured as
// "~/.<name>/skills".
func (env *regressEnv) agentDir(name string) string {
	return filepath.Join(env.home, "."+name, "skills")
}

func regressMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, regressDirMode); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

func regressWrite(t *testing.T, path, content string) {
	t.Helper()
	regressMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(content), regressFileMode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func regressRead(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func regressSymlink(t *testing.T, target, link string) {
	t.Helper()
	regressMkdir(t, filepath.Dir(link))
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink %s -> %s: %v", link, target, err)
	}
}

// regressSkill writes a conforming skill directory dir whose frontmatter name
// is name.
func regressSkill(t *testing.T, dir, name string) {
	t.Helper()
	regressWrite(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\n---\n# "+name+"\n")
}

func regressRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	return regressRunTimeout(t, regressTimeout, args...)
}

// regressRunTimeout fails the test instead of hanging when a command blocks
// (for example on a FIFO), which is itself a regression.
func regressRunTimeout(t *testing.T, timeout time.Duration, args ...string) (int, string, string) {
	t.Helper()
	type result struct {
		code           int
		stdout, stderr string
	}
	done := make(chan result, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := cli.Run(args, &stdout, &stderr)
		done <- result{code, stdout.String(), stderr.String()}
	}()
	select {
	case got := <-done:
		return got.code, got.stdout, got.stderr
	case <-time.After(timeout):
		t.Fatalf("sm %v did not finish within %s (hang)", args, timeout)
		return 0, "", ""
	}
}

func regressExpectCode(t *testing.T, label string, got, want int, stdout, stderr string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", label, got, want, stdout, stderr)
	}
}

func regressReadlink(t *testing.T, path string) string {
	t.Helper()
	target, err := os.Readlink(path)
	if err != nil {
		t.Fatalf("readlink %s: %v", path, err)
	}
	return target
}

func regressAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s should not exist (lstat err=%v)", path, err)
	}
}

func regressExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); err != nil {
		t.Fatalf("%s should exist: %v", path, err)
	}
}

func regressRealDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("%s should be a real directory, mode %v", path, info.Mode())
	}
}

func regressContains(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: want substring %q in:\n%s", label, needle, haystack)
	}
}

func regressNotContains(t *testing.T, label, haystack, needle string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: unexpected substring %q in:\n%s", label, needle, haystack)
	}
}

func regressRequireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("this test exercises permission denial and must run as a non-root user; root bypasses file modes")
	}
}

// regressDirEntries lists names in dir, failing the test on error.
func regressDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// R3: a user-replaced symlink at a recorded name is never removed.
func TestRegressUserReplacedSymlinkSurvivesSync(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "first sync", code, regressExitOK, stdout, stderr)

	link := filepath.Join(env.agentDir("one"), "alpha")
	userTarget := filepath.Join(env.root, "mine", "alpha")
	regressWrite(t, filepath.Join(userTarget, "precious.txt"), "keep")
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	regressSymlink(t, userTarget, link)
	if err := os.Rename(filepath.Join(env.store, "alpha"), filepath.Join(env.root, "alpha-away")); err != nil {
		t.Fatalf("move skill out of store: %v", err)
	}

	code, stdout, stderr = regressRun(t, "sync")
	regressExpectCode(t, "second sync", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, link); got != userTarget {
		t.Fatalf("user link target = %q, want %q", got, userTarget)
	}
	if regressRead(t, filepath.Join(userTarget, "precious.txt")) != "keep" {
		t.Fatal("user link target content changed")
	}
	regressContains(t, "second sync stderr", stderr, "warning")
}

// R4: changing --store repoints the existing link and status follows it.
func TestRegressStoreChangeRepoints(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	storeA := filepath.Join(env.root, "storeA")
	storeB := filepath.Join(env.root, "storeB")
	regressSkill(t, filepath.Join(storeA, "alpha"), "alpha")
	regressSkill(t, filepath.Join(storeB, "alpha"), "alpha")
	link := filepath.Join(env.agentDir("one"), "alpha")

	code, stdout, stderr := regressRun(t, "sync", "--store", storeA)
	regressExpectCode(t, "sync A", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, link); got != filepath.Join(storeA, "alpha") {
		t.Fatalf("after sync A link = %q", got)
	}

	code, stdout, stderr = regressRun(t, "sync", "--store", storeB)
	regressExpectCode(t, "sync B", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, link); got != filepath.Join(storeB, "alpha") {
		t.Fatalf("after sync B link = %q, want into %s", got, storeB)
	}
	regressContains(t, "sync B summary", stdout, "~1")

	if err := os.Rename(storeA, filepath.Join(env.root, "storeA-gone")); err != nil {
		t.Fatalf("remove store A: %v", err)
	}
	code, stdout, stderr = regressRun(t, "status", "--store", storeB)
	regressExpectCode(t, "status B", code, regressExitOK, stdout, stderr)
	regressContains(t, "status B", stdout, "owned=1")
	regressContains(t, "status B", stdout, "dangling=0")
}

// R4 (broken link): a dangling recorded link is reported as dangling, not owned.
func TestRegressStatusBrokenLinkNotOwned(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	storeA := filepath.Join(env.root, "storeA")
	regressSkill(t, filepath.Join(storeA, "alpha"), "alpha")
	regressSkill(t, filepath.Join(storeA, "beta"), "beta")
	code, stdout, stderr := regressRun(t, "sync", "--store", storeA)
	regressExpectCode(t, "sync A", code, regressExitOK, stdout, stderr)
	if err := os.Rename(filepath.Join(storeA, "beta"), filepath.Join(env.root, "beta-gone")); err != nil {
		t.Fatalf("remove beta: %v", err)
	}
	code, stdout, stderr = regressRun(t, "status", "--store", storeA)
	regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
	regressContains(t, "status", stdout, "owned=1")
	regressContains(t, "status", stdout, "dangling=1")
}

// R5: init -h writes nothing; unknown init flags are usage errors.
func TestRegressInitHelpWritesNothing(t *testing.T) {
	env := newRegressEnv(t)
	code, stdout, stderr := regressRun(t, "init", "-h")
	regressExpectCode(t, "init -h", code, regressExitOK, stdout, stderr)
	regressAbsent(t, filepath.Join(env.configDir, "skills-mapper"))

	code, stdout, stderr = regressRun(t, "init", "--bogus")
	regressExpectCode(t, "init --bogus", code, regressExitUsage, stdout, stderr)
	regressAbsent(t, filepath.Join(env.configDir, "skills-mapper"))
}

// R6/R22: copy mode never follows the old predictable stage path, never carries
// a source .sm-copy, and only copies relative in-tree symlinks.
func TestRegressCopyStageAndSpecialContent(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	skillDir := filepath.Join(env.store, "alpha")
	regressSkill(t, skillDir, "alpha")
	regressWrite(t, filepath.Join(skillDir, ".sm-copy"), "forged-by-source")
	regressWrite(t, filepath.Join(skillDir, "sub", "file.txt"), "in tree")
	outsideFile := filepath.Join(env.root, "outside-file.txt")
	regressWrite(t, outsideFile, "outside")
	regressSymlink(t, outsideFile, filepath.Join(skillDir, "abs-link"))
	regressSymlink(t, "../../../outside-file.txt", filepath.Join(skillDir, "escape-link"))
	regressSymlink(t, "sub/file.txt", filepath.Join(skillDir, "rel-link"))

	agentDir := env.agentDir("one")
	regressMkdir(t, agentDir)
	outsideDir := filepath.Join(env.root, "outside-dir")
	regressMkdir(t, outsideDir)
	regressSymlink(t, outsideDir, filepath.Join(agentDir, "alpha.sm-stage"))

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)

	if names := regressDirEntries(t, outsideDir); len(names) != 0 {
		t.Fatalf("files written through planted stage symlink: %v", names)
	}
	copied := filepath.Join(agentDir, "alpha")
	regressRealDir(t, copied)
	marker := regressRead(t, filepath.Join(copied, ".sm-copy"))
	if strings.Contains(marker, "forged-by-source") {
		t.Fatalf("source .sm-copy was copied: marker %q", marker)
	}
	regressAbsent(t, filepath.Join(copied, "abs-link"))
	regressAbsent(t, filepath.Join(copied, "escape-link"))
	if got := regressReadlink(t, filepath.Join(copied, "rel-link")); got != "sub/file.txt" {
		t.Fatalf("rel-link target = %q", got)
	}
	regressContains(t, "sync --copy stderr", stderr, "warning")
	if regressRead(t, outsideFile) != "outside" {
		t.Fatal("outside file modified")
	}
}

// R7: copy of a symlinked store skill yields a real directory; the real source
// is untouched.
func TestRegressCopySymlinkedStoreSkill(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	realRoot := filepath.Join(env.root, "real")
	regressSkill(t, filepath.Join(realRoot, "alpha"), "alpha")
	regressSkill(t, filepath.Join(realRoot, "beta"), "beta")
	regressSymlink(t, filepath.Join(realRoot, "alpha"), filepath.Join(env.store, "alpha"))
	// store is <root>/data/skills, so ../../real reaches <root>/real.
	regressSymlink(t, "../../real/beta", filepath.Join(env.store, "beta"))

	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)
	for _, name := range []string{"alpha", "beta"} {
		copied := filepath.Join(env.agentDir("one"), name)
		regressRealDir(t, copied)
		regressExists(t, filepath.Join(copied, "SKILL.md"))
		regressAbsent(t, filepath.Join(realRoot, name, ".sm-copy"))
		for _, entry := range regressDirEntries(t, filepath.Join(realRoot, name)) {
			if strings.Contains(entry, "sm-stage") {
				t.Fatalf("stage left in real source %s: %s", name, entry)
			}
		}
	}
	for _, entry := range regressDirEntries(t, realRoot) {
		if strings.Contains(entry, "sm-stage") || strings.Contains(entry, "sm-old") {
			t.Fatalf("stage left next to real source: %s", entry)
		}
	}
}

// R9: foreign *.sm-stage entries survive copy sync.
func TestRegressForeignStageSurvives(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	agentDir := env.agentDir("one")
	regressWrite(t, filepath.Join(agentDir, "x.sm-stage", "user.txt"), "mine")
	regressWrite(t, filepath.Join(agentDir, ".sm-stage-foo", "user.txt"), "mine too")

	for run := 0; run < 2; run++ {
		code, stdout, stderr := regressRun(t, "sync", "--copy")
		regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)
	}
	if regressRead(t, filepath.Join(agentDir, "x.sm-stage", "user.txt")) != "mine" {
		t.Fatal("x.sm-stage content changed")
	}
	if regressRead(t, filepath.Join(agentDir, ".sm-stage-foo", "user.txt")) != "mine too" {
		t.Fatal(".sm-stage-foo content changed")
	}
}

// R10: a directory with a forged .sm-copy marker is never removed.
func TestRegressForgedMarkerNotRemoved(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "first copy", code, regressExitOK, stdout, stderr)

	copied := filepath.Join(env.agentDir("one"), "alpha")
	if err := os.RemoveAll(copied); err != nil {
		t.Fatalf("remove copy: %v", err)
	}
	regressWrite(t, filepath.Join(copied, "user.txt"), "user work")
	regressWrite(t, filepath.Join(copied, ".sm-copy"), "/somewhere/else/alpha\n")
	if err := os.Rename(filepath.Join(env.store, "alpha"), filepath.Join(env.root, "alpha-gone")); err != nil {
		t.Fatalf("remove skill from store: %v", err)
	}

	code, stdout, stderr = regressRun(t, "sync", "--copy")
	regressExpectCode(t, "second copy", code, regressExitOK, stdout, stderr)
	if regressRead(t, filepath.Join(copied, "user.txt")) != "user work" {
		t.Fatal("forged-marker directory lost content after sync")
	}
	code, stdout, stderr = regressRun(t, "clean")
	regressExpectCode(t, "clean", code, regressExitOK, stdout, stderr)
	if regressRead(t, filepath.Join(copied, "user.txt")) != "user work" {
		t.Fatal("forged-marker directory lost content after clean")
	}
	regressAbsent(t, filepath.Join(env.agentDir("one"), "beta"))
}

// R11: dangerous agent directories and bad env expansions are refused.
func TestRegressDangerousAgentDirsRefused(t *testing.T) {
	cases := []struct {
		name  string
		value func(env *regressEnv) string
	}{
		{"filesystem root", func(env *regressEnv) string { return "/" }},
		{"home itself", func(env *regressEnv) string { return env.home }},
		{"ancestor of home", func(env *regressEnv) string { return env.root }},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newRegressEnv(t)
			regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
			dir := testCase.value(env)
			env.writeAgents(t, "[agents]\nbad = \""+dir+"\"\n")
			code, stdout, stderr := regressRun(t, "sync")
			regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
			regressAbsent(t, filepath.Join(dir, "alpha"))
			regressAbsent(t, env.statePath)
		})
	}
	t.Run("undefined variable", func(t *testing.T) {
		env := newRegressEnv(t)
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		t.Setenv("SM_REGRESS_UNSET_VAR", "x")
		if err := os.Unsetenv("SM_REGRESS_UNSET_VAR"); err != nil {
			t.Fatalf("unsetenv: %v", err)
		}
		env.writeAgents(t, "[agents]\nbad = \"$SM_REGRESS_UNSET_VAR/skills\"\n")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
		regressContains(t, "stderr", stderr, "undefined or empty environment variable")
		regressAbsent(t, "/skills/alpha")
	})
	t.Run("empty variable", func(t *testing.T) {
		env := newRegressEnv(t)
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		t.Setenv("SM_REGRESS_EMPTY", "")
		env.writeAgents(t, "[agents]\nbad = \"$SM_REGRESS_EMPTY/skills\"\n")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
		regressContains(t, "stderr", stderr, "undefined or empty environment variable")
	})
}

// R12: --agent-home is literal; environment variables are not expanded.
func TestRegressAgentHomeNoEnvExpansion(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	expanded := filepath.Join(env.root, "expanded")
	regressMkdir(t, expanded)
	t.Setenv("SM_REGRESS_VAR", expanded)
	literal := filepath.Join(env.root, "$SM_REGRESS_VAR")
	regressMkdir(t, literal)

	code, stdout, stderr := regressRun(t, "sync", "--agent-home", literal)
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
	regressExists(t, filepath.Join(literal, ".one", "skills", "alpha"))
	regressAbsent(t, filepath.Join(expanded, ".one"))
}

// R14: orphaned targets are reported and cleaned by identity only.
func TestRegressCleanOrphans(t *testing.T) {
	env := newRegressEnv(t)
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressSkill(t, filepath.Join(env.store, "beta"), "beta")
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\ntwo = \"~/.two/skills\"\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)

	twoDir := env.agentDir("two")
	userTarget := filepath.Join(env.root, "mine")
	regressMkdir(t, userTarget)
	if err := os.Remove(filepath.Join(twoDir, "beta")); err != nil {
		t.Fatalf("remove beta link: %v", err)
	}
	regressSymlink(t, userTarget, filepath.Join(twoDir, "beta"))

	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	code, stdout, stderr = regressRun(t, "status")
	regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
	regressContains(t, "status output", stdout+stderr, "orphaned target")
	regressContains(t, "status output", stdout+stderr, twoDir)

	code, stdout, stderr = regressRun(t, "clean", "--orphans", "--agent", "one")
	regressExpectCode(t, "clean --orphans --agent", code, regressExitUsage, stdout, stderr)
	code, stdout, stderr = regressRun(t, "clean", "--orphans", "--agent-home", filepath.Join(env.root, "profile"))
	regressExpectCode(t, "clean --orphans --agent-home", code, regressExitUsage, stdout, stderr)

	code, stdout, stderr = regressRun(t, "clean", "--orphans")
	regressExpectCode(t, "clean --orphans", code, regressExitOK, stdout, stderr)
	regressAbsent(t, filepath.Join(twoDir, "alpha"))
	if got := regressReadlink(t, filepath.Join(twoDir, "beta")); got != userTarget {
		t.Fatalf("user-replaced entry target = %q, want %q", got, userTarget)
	}
	for _, name := range []string{"alpha", "beta"} {
		if got := regressReadlink(t, filepath.Join(env.agentDir("one"), name)); got != filepath.Join(env.store, name) {
			t.Fatalf("configured agent link %s = %q", name, got)
		}
	}
}

// R14: an alias of a configured agent directory is not an orphan.
func TestRegressCleanOrphansKeepsAlias(t *testing.T) {
	env := newRegressEnv(t)
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressMkdir(t, filepath.Join(env.home, ".one"))
	regressSymlink(t, filepath.Join(env.home, ".one"), filepath.Join(env.home, ".alias"))
	env.writeAgents(t, "[agents]\none = \"~/.alias/skills\"\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync via alias", code, regressExitOK, stdout, stderr)
	link := filepath.Join(env.agentDir("one"), "alpha")
	regressExists(t, link)

	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	code, stdout, stderr = regressRun(t, "clean", "--orphans")
	regressExpectCode(t, "clean --orphans", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, link); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("aliased agent link = %q", got)
	}
}

// R14: a --agent-home provisioned profile is listed but never an orphan.
func TestRegressCleanOrphansKeepsProvisioned(t *testing.T) {
	env := newRegressEnv(t)
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	profile := filepath.Join(env.root, "profile")
	regressMkdir(t, profile)
	code, stdout, stderr := regressRun(t, "sync", "--agent-home", profile)
	regressExpectCode(t, "provision", code, regressExitOK, stdout, stderr)
	provisioned := filepath.Join(profile, ".one", "skills", "alpha")
	regressExists(t, provisioned)

	code, stdout, stderr = regressRun(t, "status")
	regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
	regressContains(t, "status output", stdout+stderr, "provisioned target")

	code, stdout, stderr = regressRun(t, "clean", "--orphans")
	regressExpectCode(t, "clean --orphans", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, provisioned); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("provisioned link = %q", got)
	}
}

// R15: probe inserts into the [agents] table and keeps the file loadable.
func TestRegressProbeConfigShapes(t *testing.T) {
	setup := func(t *testing.T) *regressEnv {
		env := newRegressEnv(t)
		emptyPath := filepath.Join(env.root, "empty-path")
		regressMkdir(t, emptyPath)
		t.Setenv("PATH", emptyPath)
		regressMkdir(t, filepath.Join(env.home, ".claude"))
		return env
	}
	const probedKey = "claude-code"
	countKey := func(content string) int {
		count := 0
		for _, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, probedKey+" ") || strings.HasPrefix(trimmed, probedKey+"=") || strings.HasPrefix(trimmed, "\""+probedKey+"\"") {
				count++
			}
		}
		return count
	}
	const configMode os.FileMode = 0o640
	checkWrite := func(t *testing.T, env *regressEnv) {
		info, err := os.Stat(env.agentsPath())
		if err != nil {
			t.Fatalf("stat config: %v", err)
		}
		if info.Mode().Perm() != configMode {
			t.Fatalf("config mode %v, want %v", info.Mode().Perm(), configMode)
		}
		names := regressDirEntries(t, filepath.Dir(env.agentsPath()))
		if len(names) != 1 || names[0] != "agents.toml" {
			t.Fatalf("config dir has extra entries: %v", names)
		}
	}

	// Contract 11 makes any top-level table other than [agents] an error, so a
	// foreign table after [agents] must be refused without touching the file
	// rather than having entries appended into it.
	t.Run("unknown table after agents refused", func(t *testing.T) {
		env := setup(t)
		original := "# top comment\n[agents]\n# keep me\nfoo = \"~/.foo/skills\"\n\n[other]\nkey = \"value\"\n"
		env.writeAgents(t, original)
		code, stdout, stderr := regressRun(t, "probe", "--yes")
		regressExpectCode(t, "probe", code, regressExitError, stdout, stderr)
		if regressRead(t, env.agentsPath()) != original {
			t.Fatal("config with unknown table was modified")
		}
	})
	t.Run("trailing comments after agents table", func(t *testing.T) {
		env := setup(t)
		original := "# top comment\n[agents]\n# keep me\nfoo = \"~/.foo/skills\"\n\n# trailing section comment\n# more notes\n"
		env.writeAgents(t, original)
		if err := os.Chmod(env.agentsPath(), configMode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		code, stdout, stderr := regressRun(t, "probe", "--yes")
		regressExpectCode(t, "probe", code, regressExitOK, stdout, stderr)
		content := regressRead(t, env.agentsPath())
		keyIndex := strings.Index(content, probedKey)
		agentsIndex := strings.Index(content, "[agents]")
		if keyIndex < 0 || keyIndex < agentsIndex || countKey(content) != 1 {
			t.Fatalf("probed entry not inside [agents] table:\n%s", content)
		}
		if strings.Count(content, "[agents]") != 1 {
			t.Fatalf("duplicate [agents] header:\n%s", content)
		}
		for _, comment := range []string{"# keep me", "# top comment", "# trailing section comment", "# more notes"} {
			regressContains(t, "config", content, comment)
		}
		checkWrite(t, env)
		code, stdout, stderr = regressRun(t, "status")
		regressExpectCode(t, "status after probe", code, regressExitOK, stdout, stderr)
	})
	t.Run("comment only", func(t *testing.T) {
		env := setup(t)
		env.writeAgents(t, "# just a comment\n")
		if err := os.Chmod(env.agentsPath(), configMode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		code, stdout, stderr := regressRun(t, "probe", "--yes")
		regressExpectCode(t, "probe", code, regressExitOK, stdout, stderr)
		content := regressRead(t, env.agentsPath())
		regressContains(t, "config", content, "# just a comment")
		regressContains(t, "config", content, "[agents]")
		if countKey(content) != 1 {
			t.Fatalf("want one %s entry:\n%s", probedKey, content)
		}
		checkWrite(t, env)
		code, stdout, stderr = regressRun(t, "status")
		regressExpectCode(t, "status after probe", code, regressExitOK, stdout, stderr)
	})
	t.Run("inline table refused", func(t *testing.T) {
		env := setup(t)
		original := "agents = { foo = \"~/.foo/skills\" }\n"
		env.writeAgents(t, original)
		code, stdout, stderr := regressRun(t, "probe", "--yes")
		regressExpectCode(t, "probe", code, regressExitError, stdout, stderr)
		if regressRead(t, env.agentsPath()) != original {
			t.Fatal("inline config was modified")
		}
	})
	t.Run("second run no duplicates", func(t *testing.T) {
		env := setup(t)
		env.writeAgents(t, "[agents]\n# existing comment\nfoo = \"~/.foo/skills\"\n")
		if err := os.Chmod(env.agentsPath(), configMode); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		for run := 0; run < 2; run++ {
			code, stdout, stderr := regressRun(t, "probe", "--yes")
			regressExpectCode(t, "probe", code, regressExitOK, stdout, stderr)
		}
		content := regressRead(t, env.agentsPath())
		if countKey(content) != 1 {
			t.Fatalf("want one %s entry after two runs:\n%s", probedKey, content)
		}
		regressContains(t, "config", content, "# existing comment")
		checkWrite(t, env)
		code, stdout, stderr := regressRun(t, "status")
		regressExpectCode(t, "status after probe", code, regressExitOK, stdout, stderr)
	})
}

// R17/R28: unsupported state versions are rejected and never rewritten; the
// state file and its directory are private.
func TestRegressStateVersionRejected(t *testing.T) {
	documents := map[string]string{
		"version 99": "{\"version\": 99, \"targets\": {}}\n",
		"no version": "{\"targets\": {}}\n",
		"null":       "null\n",
	}
	for label, document := range documents {
		t.Run(label, func(t *testing.T) {
			env := newRegressEnv(t)
			env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
			regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
			if err := os.MkdirAll(filepath.Dir(env.statePath), 0o700); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if err := os.WriteFile(env.statePath, []byte(document), 0o600); err != nil {
				t.Fatalf("write state: %v", err)
			}
			for _, command := range []string{"sync", "status", "clean"} {
				code, stdout, stderr := regressRun(t, command)
				regressExpectCode(t, command, code, regressExitError, stdout, stderr)
				regressContains(t, command+" stderr", stderr, "unsupported state version")
				if regressRead(t, env.statePath) != document {
					t.Fatalf("%s rewrote the state file", command)
				}
			}
		})
	}
}

func TestRegressStateFileModes(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
	fileInfo, err := os.Stat(env.statePath)
	if err != nil {
		t.Fatalf("stat state: %v", err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("state mode %v, want 0600", fileInfo.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(env.statePath))
	if err != nil {
		t.Fatalf("stat state dir: %v", err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("state dir mode %v, want 0700", dirInfo.Mode().Perm())
	}
}

// R28: copied files lose group/other write and setuid.
func TestRegressCopyModesSanitized(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	skillDir := filepath.Join(env.store, "alpha")
	regressSkill(t, skillDir, "alpha")
	wide := filepath.Join(skillDir, "wide.txt")
	setuid := filepath.Join(skillDir, "tool.sh")
	regressWrite(t, wide, "w")
	regressWrite(t, setuid, "#!/bin/sh\n")
	if err := os.Chmod(wide, 0o666); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.Chmod(setuid, 0o755|os.ModeSetuid|os.ModeSetgid); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	code, stdout, stderr := regressRun(t, "sync", "--copy")
	regressExpectCode(t, "sync --copy", code, regressExitOK, stdout, stderr)
	for _, name := range []string{"wide.txt", "tool.sh", "SKILL.md"} {
		info, err := os.Stat(filepath.Join(env.agentDir("one"), "alpha", name))
		if err != nil {
			t.Fatalf("stat copy %s: %v", name, err)
		}
		if info.Mode().Perm()&0o022 != 0 {
			t.Fatalf("%s has group/other write: %v", name, info.Mode())
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			t.Fatalf("%s kept special bits: %v", name, info.Mode())
		}
	}
	info, err := os.Stat(filepath.Join(env.agentDir("one"), "alpha"))
	if err != nil {
		t.Fatalf("stat copy dir: %v", err)
	}
	if info.Mode().Perm()&0o022 != 0 {
		t.Fatalf("copied dir has group/other write: %v", info.Mode())
	}
}

// R19: two agents on the same (or aliased) directory sync once with a warning.
func TestRegressDuplicateAgentDirs(t *testing.T) {
	t.Run("same path", func(t *testing.T) {
		env := newRegressEnv(t)
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		env.writeAgents(t, "[agents]\nagentalpha = \"~/.one/skills\"\nagentbravo = \"~/.one/skills\"\n")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
		regressContains(t, "stderr", stderr, "agentalpha")
		regressContains(t, "stderr", stderr, "agentbravo")
		regressContains(t, "stderr", stderr, "warning")
		if got := regressReadlink(t, filepath.Join(env.agentDir("one"), "alpha")); got != filepath.Join(env.store, "alpha") {
			t.Fatalf("link = %q", got)
		}
		if strings.Count(stdout, "+1") != 1 {
			t.Fatalf("directory should be synced once, summary:\n%s", stdout)
		}
	})
	t.Run("symlink alias", func(t *testing.T) {
		env := newRegressEnv(t)
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		regressMkdir(t, env.agentDir("one"))
		regressSymlink(t, filepath.Join(env.home, ".one"), filepath.Join(env.home, ".alias"))
		env.writeAgents(t, "[agents]\nagentalpha = \"~/.one/skills\"\nagentbravo = \"~/.alias/skills\"\n")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
		regressContains(t, "stderr", stderr, "agentalpha")
		regressContains(t, "stderr", stderr, "agentbravo")
		regressExists(t, filepath.Join(env.agentDir("one"), "alpha"))
		regressNotContains(t, "stderr", stderr, "unmanaged")
		if strings.Count(stdout, "+1") != 1 {
			t.Fatalf("directory should be synced once, summary:\n%s", stdout)
		}
	})
}

// R20: unusable store entries keep their links.
func TestRegressUnusableStoreEntriesKeepLinks(t *testing.T) {
	regressRequireNonRoot(t)
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		regressSkill(t, filepath.Join(env.store, name), name)
	}
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "first sync", code, regressExitOK, stdout, stderr)

	skillFile := filepath.Join(env.store, "alpha", "SKILL.md")
	if err := os.Chmod(skillFile, 0); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(skillFile, regressFileMode); err != nil {
			t.Errorf("restore mode: %v", err)
		}
	})
	if err := os.Rename(filepath.Join(env.store, "gamma"), filepath.Join(env.root, "gamma-away")); err != nil {
		t.Fatalf("move gamma: %v", err)
	}
	regressSymlink(t, filepath.Join(env.root, "gamma-missing"), filepath.Join(env.store, "gamma"))

	code, stdout, stderr = regressRun(t, "sync")
	regressExpectCode(t, "second sync", code, regressExitOK, stdout, stderr)
	regressContains(t, "stderr", stderr, "warning")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if got := regressReadlink(t, filepath.Join(env.agentDir("one"), name)); got != filepath.Join(env.store, name) {
			t.Fatalf("link %s = %q", name, got)
		}
	}
}

// R20: an empty store with recorded state is refused unless --allow-empty.
func TestRegressEmptyStoreGuard(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "first sync", code, regressExitOK, stdout, stderr)
	link := filepath.Join(env.agentDir("one"), "alpha")
	if err := os.Rename(filepath.Join(env.store, "alpha"), filepath.Join(env.root, "alpha-away")); err != nil {
		t.Fatalf("empty the store: %v", err)
	}

	code, stdout, stderr = regressRun(t, "sync")
	regressExpectCode(t, "sync empty store", code, regressExitError, stdout, stderr)
	regressContains(t, "stderr", stderr, "--allow-empty")
	regressExists(t, link)

	code, stdout, stderr = regressRun(t, "sync", "--allow-empty")
	regressExpectCode(t, "sync --allow-empty", code, regressExitOK, stdout, stderr)
	regressAbsent(t, link)
}

// R20: a mistyped store full of non-skill directories does not prune links.
func TestRegressMistypedStoreKeepsLinks(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "first sync", code, regressExitOK, stdout, stderr)
	link := filepath.Join(env.agentDir("one"), "alpha")

	mistyped := filepath.Join(env.root, "projects")
	regressWrite(t, filepath.Join(mistyped, "Some Project", "main.go"), "package main\n")
	regressWrite(t, filepath.Join(mistyped, "notes", "todo.txt"), "x")
	regressMkdir(t, filepath.Join(mistyped, "UPPER"))

	code, stdout, stderr = regressRun(t, "sync", "--store", mistyped)
	if got := regressReadlink(t, link); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("mistyped store changed link to %q (exit %d)\nstdout:%s\nstderr:%s", got, code, stdout, stderr)
	}
	if code == regressExitOK {
		t.Fatalf("mistyped store sync should be refused, exit 0\nstdout:%s\nstderr:%s", stdout, stderr)
	}
	regressContains(t, "stderr", stderr, "--allow-empty")
}

// R26: dot entries such as .git are silent.
func TestRegressGitDirInStoreSilent(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	regressWrite(t, filepath.Join(env.store, ".git", "HEAD"), "ref: refs/heads/main\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
	regressNotContains(t, "sync stderr", stderr, ".git")
	regressNotContains(t, "sync stderr", stderr, "warning")
	code, stdout, stderr = regressRun(t, "status")
	regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
	regressNotContains(t, "status stderr", stderr, ".git")
	regressAbsent(t, filepath.Join(env.agentDir("one"), ".git"))
}

// R27: status warnings, zero agents, config typos and the init hint.
func TestRegressStatusAndConfigDiagnostics(t *testing.T) {
	t.Run("status prints store warnings", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		regressSkill(t, filepath.Join(env.store, "Bad_Name"), "Bad_Name")
		code, stdout, stderr := regressRun(t, "status")
		regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
		regressContains(t, "status output", stdout+stderr, "Bad_Name")
	})
	t.Run("status counts dangling owned link", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		regressSkill(t, filepath.Join(env.store, "beta"), "beta")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
		if err := os.Rename(filepath.Join(env.store, "beta"), filepath.Join(env.root, "beta-away")); err != nil {
			t.Fatalf("move: %v", err)
		}
		code, stdout, stderr = regressRun(t, "status")
		regressExpectCode(t, "status", code, regressExitOK, stdout, stderr)
		regressContains(t, "status", stdout, "owned=1")
		regressContains(t, "status", stdout, "dangling=1")
	})
	t.Run("agent typo table", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agent]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
		regressAbsent(t, env.agentDir("one"))
	})
	t.Run("invalid config has no init hint", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "this is [[[ not toml\n")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
		regressNotContains(t, "stderr", stderr, "sm init")
	})
	t.Run("missing config has init hint", func(t *testing.T) {
		newRegressEnv(t)
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "sync", code, regressExitError, stdout, stderr)
		regressContains(t, "stderr", stderr, "sm init")
	})
}

// R27: the zero-agents message must not come with a failure exit.
func TestRegressZeroAgentsExit(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\n")
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	code, stdout, stderr := regressRun(t, "sync")
	regressContains(t, "sync output", stdout+stderr, "no agents enabled in agents.toml")
	if code == regressExitUsage {
		t.Fatalf("zero agents is not a usage error\nstdout:%s\nstderr:%s", stdout, stderr)
	}
}

// R30: help goes to stdout with exit 0; no arguments is a usage error.
func TestRegressHelpOutput(t *testing.T) {
	newRegressEnv(t)
	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}, {"sync", "-h"}, {"status", "-h"}, {"clean", "-h"}, {"probe", "-h"}, {"init", "--help"}} {
		code, stdout, stderr := regressRun(t, args...)
		regressExpectCode(t, strings.Join(args, " "), code, regressExitOK, stdout, stderr)
		regressContains(t, strings.Join(args, " ")+" stdout", stdout, "usage")
	}
	code, stdout, stderr := regressRun(t)
	regressExpectCode(t, "no args", code, regressExitUsage, stdout, stderr)
	regressContains(t, "no args stderr", stderr, "usage")
}

// R31: a BOM and trailing spaces on the fences are accepted.
func TestRegressBOMFrontmatterAccepted(t *testing.T) {
	env := newRegressEnv(t)
	env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
	regressWrite(t, filepath.Join(env.store, "alpha", "SKILL.md"), "\xef\xbb\xbf---  \nname: alpha\n---\t \n# alpha\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync", code, regressExitOK, stdout, stderr)
	if got := regressReadlink(t, filepath.Join(env.agentDir("one"), "alpha")); got != filepath.Join(env.store, "alpha") {
		t.Fatalf("link = %q", got)
	}
	regressNotContains(t, "stderr", stderr, "alpha")
}

// Contract 6: summaries are idempotent; link mode prints ~ only when repointed,
// copy mode always prints ~.
func TestRegressSummaryTerms(t *testing.T) {
	t.Run("link mode", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		code, stdout, stderr := regressRun(t, "sync")
		regressExpectCode(t, "first", code, regressExitOK, stdout, stderr)
		regressContains(t, "first", stdout, "one +1 -0")
		regressNotContains(t, "first", stdout, "~")
		code, stdout, stderr = regressRun(t, "sync")
		regressExpectCode(t, "second", code, regressExitOK, stdout, stderr)
		regressContains(t, "second", stdout, "one +0 -0")
		regressNotContains(t, "second", stdout, "~")
	})
	t.Run("copy mode", func(t *testing.T) {
		env := newRegressEnv(t)
		env.writeAgents(t, "[agents]\none = \"~/.one/skills\"\n")
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
		code, stdout, stderr := regressRun(t, "sync", "--copy")
		regressExpectCode(t, "first", code, regressExitOK, stdout, stderr)
		regressContains(t, "first", stdout, "one +1 ~0 -0")
		code, stdout, stderr = regressRun(t, "sync", "--copy")
		regressExpectCode(t, "second", code, regressExitOK, stdout, stderr)
		regressContains(t, "second", stdout, "one +0 ~1 -0")
	})
}

// Contract 3: sm refuses an agent dir that overlaps the store.
func TestRegressAgentDirOverlappingStoreRefused(t *testing.T) {
	env := newRegressEnv(t)
	regressSkill(t, filepath.Join(env.store, "alpha"), "alpha")
	env.writeAgents(t, "[agents]\nbad = \""+env.store+"\"\n")
	code, stdout, stderr := regressRun(t, "sync")
	regressExpectCode(t, "sync into store", code, regressExitError, stdout, stderr)
	regressRealDir(t, filepath.Join(env.store, "alpha"))
	regressExists(t, filepath.Join(env.store, "alpha", "SKILL.md"))
}
