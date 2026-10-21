package mscli_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gherlein/sm/internal/mscli"
)

// Black-box regression suite for the 2026-10-20 review findings, driving
// mscli.Run only.

const (
	regressExitOK    = 0
	regressExitError = 1
	regressExitUsage = 2

	regressTimeout = 20 * time.Second

	regressDirMode  os.FileMode = 0o755
	regressFileMode os.FileMode = 0o644

	regressPluginNotice = "ingested skill(s) came from plugin directories"
)

type regressEnv struct {
	root       string
	home       string
	dataDir    string
	store      string
	ledgerPath string
}

func newRegressEnv(t *testing.T) *regressEnv {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	env := &regressEnv{
		root:    root,
		home:    filepath.Join(root, "home"),
		dataDir: filepath.Join(root, "data"),
	}
	env.store = filepath.Join(env.dataDir, "skills")
	env.ledgerPath = filepath.Join(env.dataDir, "skills-mapper", "ingest-log.json")
	regressMkdir(t, env.home)
	t.Setenv("HOME", env.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", env.dataDir)
	return env
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

func regressSkill(t *testing.T, dir, name, body string) {
	t.Helper()
	regressWrite(t, filepath.Join(dir, "SKILL.md"), "---\nname: "+name+"\n---\n"+body+"\n")
}

func regressRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	type result struct {
		code           int
		stdout, stderr string
	}
	done := make(chan result, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := mscli.Run(args, &stdout, &stderr)
		done <- result{code, stdout.String(), stderr.String()}
	}()
	select {
	case got := <-done:
		return got.code, got.stdout, got.stderr
	case <-time.After(regressTimeout):
		t.Fatalf("ms %v did not finish within %s (hang)", args, regressTimeout)
		return 0, "", ""
	}
}

func regressExpectCode(t *testing.T, label string, got, want int, stdout, stderr string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: exit %d, want %d\nstdout:\n%s\nstderr:\n%s", label, got, want, stdout, stderr)
	}
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

// R1: --force never deletes a source that lives inside the destination.
func TestRegressForceSourceInsideDestination(t *testing.T) {
	t.Run("source is a store skill", func(t *testing.T) {
		env := newRegressEnv(t)
		precious := filepath.Join(env.store, "precious")
		regressSkill(t, precious, "precious", "original")
		regressWrite(t, filepath.Join(precious, "data.txt"), "precious data")
		code, stdout, stderr := regressRun(t, "--force", precious)
		regressExpectCode(t, "ms --force", code, regressExitError, stdout, stderr)
		regressContains(t, "SKILL.md", regressRead(t, filepath.Join(precious, "SKILL.md")), "original")
		if regressRead(t, filepath.Join(precious, "data.txt")) != "precious data" {
			t.Fatal("source data lost")
		}
	})
	t.Run("destination is an ancestor of the source", func(t *testing.T) {
		env := newRegressEnv(t)
		dest := filepath.Join(env.root, "dest")
		source := filepath.Join(dest, "x", "y", "src")
		regressSkill(t, filepath.Join(source, "x"), "x", "original")
		regressWrite(t, filepath.Join(source, "x", "data.txt"), "keep")
		code, stdout, stderr := regressRun(t, "--force", source, dest)
		regressExpectCode(t, "ms --force", code, regressExitError, stdout, stderr)
		if regressRead(t, filepath.Join(source, "x", "data.txt")) != "keep" {
			t.Fatal("source data lost")
		}
		regressContains(t, "SKILL.md", regressRead(t, filepath.Join(source, "x", "SKILL.md")), "original")
	})
	t.Run("source named like an ancestor", func(t *testing.T) {
		env := newRegressEnv(t)
		dest := filepath.Join(env.root, "dest")
		source := filepath.Join(dest, "x", "y", "src")
		regressSkill(t, source, "x", "original")
		regressWrite(t, filepath.Join(source, "data.txt"), "keep")
		code, stdout, stderr := regressRun(t, "--force", source, dest)
		regressExpectCode(t, "ms --force", code, regressExitError, stdout, stderr)
		if regressRead(t, filepath.Join(source, "data.txt")) != "keep" {
			t.Fatal("source data lost")
		}
	})
	t.Run("symlinked destination entry", func(t *testing.T) {
		env := newRegressEnv(t)
		elsewhere := filepath.Join(env.root, "elsewhere", "bar")
		regressSkill(t, elsewhere, "bar", "referent")
		regressWrite(t, filepath.Join(elsewhere, "user.txt"), "user data")
		regressSymlink(t, elsewhere, filepath.Join(env.store, "foo"))
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "foo"), "foo", "new skill")
		code, stdout, stderr := regressRun(t, "--force", source)
		regressExpectCode(t, "ms --force", code, regressExitOK, stdout, stderr)
		if regressRead(t, filepath.Join(elsewhere, "user.txt")) != "user data" {
			t.Fatal("referent data lost")
		}
		regressContains(t, "referent SKILL.md", regressRead(t, filepath.Join(elsewhere, "SKILL.md")), "referent")
		regressRealDir(t, filepath.Join(env.store, "foo"))
		regressContains(t, "new SKILL.md", regressRead(t, filepath.Join(env.store, "foo", "SKILL.md")), "new skill")
		regressAbsent(t, filepath.Join(env.store, "foo", "user.txt"))
	})
}

// R2: a symlinked source root is resolved and ingested.
func TestRegressSymlinkedSourceRoot(t *testing.T) {
	cases := []struct {
		name string
		arg  func(t *testing.T, env *regressEnv, real string) string
	}{
		{"absolute link", func(t *testing.T, env *regressEnv, real string) string {
			link := filepath.Join(env.root, "abs-link")
			regressSymlink(t, real, link)
			return link
		}},
		{"relative link", func(t *testing.T, env *regressEnv, real string) string {
			link := filepath.Join(env.root, "rel-link")
			regressSymlink(t, "real", link)
			return link
		}},
		{"trailing slash", func(t *testing.T, env *regressEnv, real string) string {
			link := filepath.Join(env.root, "slash-link")
			regressSymlink(t, real, link)
			return link + "/"
		}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			env := newRegressEnv(t)
			real := filepath.Join(env.root, "real")
			regressSkill(t, filepath.Join(real, "skills", "alpha"), "alpha", "body")
			dest := filepath.Join(env.root, "dest")
			code, stdout, stderr := regressRun(t, testCase.arg(t, env, real), dest)
			regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
			regressContains(t, "summary", stdout, "ms: +1 (skipped 0)")
			regressRealDir(t, filepath.Join(dest, "alpha"))
		})
	}
}

// R6/R22: ingest never follows a planted stage symlink, never carries a source
// .sm-copy, and copies only relative in-tree symlinks.
func TestRegressIngestStageAndSpecialContent(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	skillDir := filepath.Join(source, "alpha")
	regressSkill(t, skillDir, "alpha", "body")
	regressWrite(t, filepath.Join(skillDir, ".sm-copy"), "forged")
	regressWrite(t, filepath.Join(skillDir, "sub", "file.txt"), "in tree")
	outsideFile := filepath.Join(env.root, "outside.txt")
	regressWrite(t, outsideFile, "outside")
	regressSymlink(t, outsideFile, filepath.Join(skillDir, "abs-link"))
	regressSymlink(t, "../../outside.txt", filepath.Join(skillDir, "escape-link"))
	regressSymlink(t, "sub/file.txt", filepath.Join(skillDir, "rel-link"))

	dest := filepath.Join(env.root, "dest")
	regressMkdir(t, dest)
	outsideDir := filepath.Join(env.root, "outside-dir")
	regressMkdir(t, outsideDir)
	regressSymlink(t, outsideDir, filepath.Join(dest, "alpha.sm-stage"))

	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	if names := regressDirEntries(t, outsideDir); len(names) != 0 {
		t.Fatalf("files written through planted stage symlink: %v", names)
	}
	ingested := filepath.Join(dest, "alpha")
	regressRealDir(t, ingested)
	regressAbsent(t, filepath.Join(ingested, ".sm-copy"))
	regressAbsent(t, filepath.Join(ingested, "abs-link"))
	regressAbsent(t, filepath.Join(ingested, "escape-link"))
	target, err := os.Readlink(filepath.Join(ingested, "rel-link"))
	if err != nil || target != "sub/file.txt" {
		t.Fatalf("rel-link = %q, err %v", target, err)
	}
	regressContains(t, "stderr", stderr, "warning")
}

// R12: ms path arguments are literal; $VAR is not expanded.
func TestRegressPathArgumentsLiteral(t *testing.T) {
	env := newRegressEnv(t)
	expanded := filepath.Join(env.root, "expanded")
	regressSkill(t, filepath.Join(expanded, "wrong"), "wrong", "expanded")
	t.Setenv("SM_REGRESS_VAR", expanded)
	literal := filepath.Join(env.root, "$SM_REGRESS_VAR")
	regressSkill(t, filepath.Join(literal, "right"), "right", "literal")
	destLiteral := filepath.Join(env.root, "$SM_REGRESS_VAR-dest")
	code, stdout, stderr := regressRun(t, literal, destLiteral)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	regressExists(t, filepath.Join(destLiteral, "right", "SKILL.md"))
	regressAbsent(t, filepath.Join(destLiteral, "wrong"))
	regressAbsent(t, filepath.Join(expanded+"-dest"))
}

// R16: the -- terminator always wins.
func TestRegressTerminator(t *testing.T) {
	t.Run("force after terminator is a path", func(t *testing.T) {
		env := newRegressEnv(t)
		t.Chdir(env.root)
		regressSkill(t, filepath.Join(env.store, "alpha"), "alpha", "store original")
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "incoming")
		code, stdout, stderr := regressRun(t, "--", source, "--force")
		regressContains(t, "store SKILL.md", regressRead(t, filepath.Join(env.store, "alpha", "SKILL.md")), "store original")
		regressExpectCode(t, "ms -- src --force", code, regressExitOK, stdout, stderr)
		regressExists(t, filepath.Join(env.root, "--force", "alpha", "SKILL.md"))
	})
	t.Run("format value cannot be the terminator", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		code, stdout, stderr := regressRun(t, "--format", "--", source)
		regressExpectCode(t, "ms --format --", code, regressExitUsage, stdout, stderr)
		regressAbsent(t, filepath.Join(env.store, "alpha"))
	})
	t.Run("dash-named source after terminator", func(t *testing.T) {
		env := newRegressEnv(t)
		t.Chdir(env.root)
		regressSkill(t, filepath.Join(env.root, "-x", "alpha"), "alpha", "body")
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, "--", "-x", dest)
		regressExpectCode(t, "ms -- -x", code, regressExitOK, stdout, stderr)
		regressExists(t, filepath.Join(dest, "alpha", "SKILL.md"))
	})
}

// R18: invalid YAML frontmatter is skipped with an actionable warning.
func TestRegressInvalidYAMLFrontmatter(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "good"), "good", "body")
	badFile := filepath.Join(source, "bad", "SKILL.md")
	regressWrite(t, badFile, "---\nname: bad\ndescription: Use when: x\n---\nbody\n")
	dest := filepath.Join(env.root, "dest")
	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	regressExists(t, filepath.Join(dest, "good", "SKILL.md"))
	regressAbsent(t, filepath.Join(dest, "bad"))
	regressContains(t, "stderr", stderr, badFile)
	regressContains(t, "stderr", stderr, "valid YAML")
	regressContains(t, "summary", stdout, "ms: +1 (skipped 1)")
}

// R21: symlinked skill directories inside a source need --follow-symlinks.
func TestRegressSymlinkedSkillInsideSource(t *testing.T) {
	setup := func(t *testing.T) (*regressEnv, string) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		outside := filepath.Join(env.root, "outside", "beta")
		regressSkill(t, outside, "beta", "body")
		regressSymlink(t, outside, filepath.Join(source, "beta-link"))
		return env, source
	}
	t.Run("skipped without flag", func(t *testing.T) {
		env, source := setup(t)
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressContains(t, "stderr", stderr, "use --follow-symlinks")
		regressContains(t, "summary", stdout, "ms: +1 (skipped 1)")
		regressAbsent(t, filepath.Join(dest, "beta"))
	})
	t.Run("ingested with flag", func(t *testing.T) {
		env, source := setup(t)
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, "--follow-symlinks", source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressContains(t, "summary", stdout, "ms: +2 ")
		regressRealDir(t, filepath.Join(dest, "beta"))
	})
	t.Run("link loop terminates", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "group", "alpha"), "alpha", "body")
		regressSymlink(t, source, filepath.Join(source, "group", "loop"))
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, "--follow-symlinks", source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressContains(t, "summary", stdout, "ms: +1 ")
		regressExists(t, filepath.Join(dest, "alpha", "SKILL.md"))
	})
}

// R23: personal beats plugin, skips are counted, plugin notice only for
// plugin-sourced writes.
func TestRegressPluginPrecedenceAndNotice(t *testing.T) {
	t.Run("personal wins and skips counted", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		pluginSkill := filepath.Join(source, "plugins", "acme", "skills", "foo")
		regressSkill(t, pluginSkill, "foo", "plugin version")
		regressSkill(t, filepath.Join(source, "skills", "foo"), "foo", "personal version")
		future := time.Now().Add(time.Hour)
		if err := os.Chtimes(filepath.Join(pluginSkill, "SKILL.md"), future, future); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
		regressSkill(t, filepath.Join(source, "skills", "invalid"), "Not Valid", "body")
		dest := filepath.Join(env.root, "dest")
		regressSkill(t, filepath.Join(dest, "bar"), "bar", "existing")
		regressSkill(t, filepath.Join(source, "skills", "bar"), "bar", "incoming")

		code, stdout, stderr := regressRun(t, source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressContains(t, "foo", regressRead(t, filepath.Join(dest, "foo", "SKILL.md")), "personal version")
		regressContains(t, "bar", regressRead(t, filepath.Join(dest, "bar", "SKILL.md")), "existing")
		regressContains(t, "summary", stdout, "ms: +1 (skipped 3)")
		regressNotContains(t, "stderr", stderr, regressPluginNotice)
	})
	t.Run("plugin notice", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "plugins", "acme", "skills", "baz"), "baz", "body")
		regressSkill(t, filepath.Join(source, "skills", "qux"), "qux", "body")
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressContains(t, "stderr", stderr, "note: 1 "+regressPluginNotice)
	})
}

// R24: unreadable subdirectories warn; an unreadable root fails.
func TestRegressUnreadableSource(t *testing.T) {
	regressRequireNonRoot(t)
	t.Run("unreadable subdirectory", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		locked := filepath.Join(source, "locked")
		regressSkill(t, filepath.Join(locked, "beta"), "beta", "body")
		if err := os.Chmod(locked, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(locked, regressDirMode); err != nil {
				t.Errorf("restore: %v", err)
			}
		})
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, source, dest)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		regressExists(t, filepath.Join(dest, "alpha", "SKILL.md"))
		regressContains(t, "stderr", stderr, "warning")
		regressContains(t, "stderr", stderr, "locked")
	})
	t.Run("unreadable root", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		if err := os.Chmod(source, 0); err != nil {
			t.Fatalf("chmod: %v", err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(source, regressDirMode); err != nil {
				t.Errorf("restore: %v", err)
			}
		})
		dest := filepath.Join(env.root, "dest")
		code, stdout, stderr := regressRun(t, source, dest)
		regressExpectCode(t, "ms", code, regressExitError, stdout, stderr)
		regressAbsent(t, filepath.Join(dest, "alpha"))
	})
}

// regressCountRecords returns the length of the first JSON array of objects in
// the ledger document.
func regressCountRecords(t *testing.T, document any) int {
	t.Helper()
	switch value := document.(type) {
	case []any:
		return len(value)
	case map[string]any:
		for _, child := range value {
			if array, ok := child.([]any); ok {
				return len(array)
			}
		}
	}
	t.Fatalf("ledger has no record array: %#v", document)
	return 0
}

// R29: the ingest ledger lives outside the store and is written only for real runs.
func TestRegressIngestLedger(t *testing.T) {
	t.Run("real run", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		regressSkill(t, filepath.Join(source, "beta"), "beta", "body")
		code, stdout, stderr := regressRun(t, source)
		regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
		var document any
		if err := json.Unmarshal([]byte(regressRead(t, env.ledgerPath)), &document); err != nil {
			t.Fatalf("ledger is not JSON: %v", err)
		}
		if got := regressCountRecords(t, document); got != 2 {
			t.Fatalf("ledger records = %d, want 2", got)
		}
		info, err := os.Stat(env.ledgerPath)
		if err != nil {
			t.Fatalf("stat ledger: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("ledger mode %v, want 0600", info.Mode().Perm())
		}
		walkErr := filepath.WalkDir(env.store, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if strings.Contains(entry.Name(), "ingest-log") {
				t.Fatalf("ledger found inside store: %s", path)
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk store: %v", walkErr)
		}
	})
	t.Run("dry run", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		code, stdout, stderr := regressRun(t, "--dry-run", source)
		regressExpectCode(t, "ms --dry-run", code, regressExitOK, stdout, stderr)
		regressAbsent(t, env.ledgerPath)
		regressAbsent(t, filepath.Join(env.store, "alpha"))
	})
	t.Run("corrupt ledger fails before writing", func(t *testing.T) {
		env := newRegressEnv(t)
		source := filepath.Join(env.root, "src")
		regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
		if err := os.MkdirAll(filepath.Dir(env.ledgerPath), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(env.ledgerPath, []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write ledger: %v", err)
		}
		code, stdout, stderr := regressRun(t, source)
		regressExpectCode(t, "ms", code, regressExitError, stdout, stderr)
		regressAbsent(t, filepath.Join(env.store, "alpha"))
		if regressRead(t, env.ledgerPath) != "{not json" {
			t.Fatal("corrupt ledger was rewritten")
		}
	})
}

// R28: ingested files lose group/other write and special bits.
func TestRegressIngestModesSanitized(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	skillDir := filepath.Join(source, "alpha")
	regressSkill(t, skillDir, "alpha", "body")
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
	dest := filepath.Join(env.root, "dest")
	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	for _, name := range []string{"wide.txt", "tool.sh", "SKILL.md"} {
		info, err := os.Stat(filepath.Join(dest, "alpha", name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if info.Mode().Perm()&0o022 != 0 {
			t.Fatalf("%s has group/other write: %v", name, info.Mode())
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			t.Fatalf("%s kept special bits: %v", name, info.Mode())
		}
	}
}

// R30: help on stdout with exit 0; no arguments is a usage error on stderr.
func TestRegressHelpOutput(t *testing.T) {
	newRegressEnv(t)
	for _, flagName := range []string{"-h", "--help"} {
		code, stdout, stderr := regressRun(t, flagName)
		regressExpectCode(t, flagName, code, regressExitOK, stdout, stderr)
		regressContains(t, flagName+" stdout", stdout, "usage")
		regressContains(t, flagName+" stdout", stdout, "<source> [dest]")
	}
	code, stdout, stderr := regressRun(t)
	regressExpectCode(t, "no args", code, regressExitUsage, stdout, stderr)
	regressContains(t, "no args stderr", stderr, "usage")
}

// R31: BOM and trailing whitespace on fences are accepted.
func TestRegressBOMFrontmatterAccepted(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressWrite(t, filepath.Join(source, "alpha", "SKILL.md"), "\xef\xbb\xbf---  \nname: alpha\n---\t \nbody\n")
	dest := filepath.Join(env.root, "dest")
	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	regressContains(t, "summary", stdout, "ms: +1 (skipped 0)")
	regressExists(t, filepath.Join(dest, "alpha", "SKILL.md"))
}

// Contract 3: a destination inside the source is refused.
func TestRegressDestinationInsideSourceRefused(t *testing.T) {
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
	dest := filepath.Join(source, "nested-dest")
	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms", code, regressExitError, stdout, stderr)
	regressAbsent(t, filepath.Join(dest, "alpha"))
}
