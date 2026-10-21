//go:build unix

package mscli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Black-box tests for the final fix wave (Task 18): empty positionals, the
// shared sm state lock, per-skill overlap skips and per-skill failure
// isolation. Every test drives mscli.Run only.

const (
	hardeningUnreadableMode    = 0o000
	hardeningWorldWritableMode = 0o777
)

// An empty source or destination (an unset shell variable) must never mean the
// current directory: it is a usage error and nothing is written.
func TestHardeningEmptyPositionalsAreUsageErrors(t *testing.T) {
	for _, testCase := range []struct {
		label string
		args  func(source string) []string
	}{
		{"empty destination", func(source string) []string { return []string{source, ""} }},
		{"empty source", func(string) []string { return []string{""} }},
		{"empty source with destination", func(string) []string { return []string{"", "dest"} }},
		{"empty destination after terminator", func(source string) []string { return []string{"--", source, ""} }},
	} {
		t.Run(testCase.label, func(t *testing.T) {
			env := newRegressEnv(t)
			source := filepath.Join(env.root, "src")
			regressSkill(t, filepath.Join(source, "alpha"), "alpha", "body")
			workingDir := filepath.Join(env.root, "cwd")
			regressMkdir(t, workingDir)
			t.Chdir(workingDir)

			code, stdout, stderr := regressRun(t, testCase.args(source)...)
			regressExpectCode(t, testCase.label, code, regressExitUsage, stdout, stderr)
			regressContains(t, "stderr", stderr, "empty")
			if entries := regressDirEntries(t, workingDir); len(entries) != 0 {
				t.Fatalf("working directory written: %v", entries)
			}
			regressAbsent(t, filepath.Join(env.store, "alpha"))
			regressAbsent(t, env.ledgerPath)
		})
	}
}

// A followed link whose real path is the destination skill itself overlaps only
// that one skill: it is skipped with a warning, the rest is ingested, exit 0.
func TestHardeningPerSkillOverlapIsSkip(t *testing.T) {
	for _, extra := range [][]string{nil, {"--force"}} {
		t.Run("flags "+filepath.Join(extra...), func(t *testing.T) {
			env := newRegressEnv(t)
			dest := filepath.Join(env.root, "dest")
			regressSkill(t, filepath.Join(dest, "alpha"), "alpha", "store original")
			source := filepath.Join(env.root, "src")
			regressSkill(t, filepath.Join(source, "beta"), "beta", "body")
			regressSymlink(t, filepath.Join(dest, "alpha"), filepath.Join(source, "alpha-link"))

			args := append([]string{"--follow-symlinks"}, extra...)
			args = append(args, source, dest)
			code, stdout, stderr := regressRun(t, args...)
			regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
			regressContains(t, "summary", stdout, "ms: +1 (skipped 1)")
			regressContains(t, "stderr", stderr, "warning: skipped alpha")
			regressContains(t, "stderr", stderr, "overlaps source")
			regressContains(t, "alpha", regressRead(t, filepath.Join(dest, "alpha", "SKILL.md")), "store original")
			regressExists(t, filepath.Join(dest, "beta", "SKILL.md"))
		})
	}
}

// One unreadable file in one source skill must not stop the others: they are
// written and ledgered, the summary is printed and ms exits 1.
func TestHardeningIngestIsolatesPerSkillFailure(t *testing.T) {
	regressRequireNonRoot(t)
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	for _, name := range []string{"alpha", "beta", "gamma"} {
		regressSkill(t, filepath.Join(source, name), name, "body")
	}
	secret := filepath.Join(source, "beta", "data.txt")
	regressWrite(t, secret, "data")
	if err := os.Chmod(secret, hardeningUnreadableMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(secret, regressFileMode); err != nil {
			t.Errorf("restore mode: %v", err)
		}
	})
	dest := filepath.Join(env.root, "dest")

	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms with one unreadable skill", code, regressExitError, stdout, stderr)
	regressContains(t, "summary", stdout, "ms: +2 (skipped 0)")
	regressContains(t, "stderr", stderr, "beta")
	regressContains(t, "stderr", stderr, "permission denied")
	for _, name := range []string{"alpha", "gamma"} {
		regressExists(t, filepath.Join(dest, name, "SKILL.md"))
	}
	regressAbsent(t, filepath.Join(dest, "beta"))
	var ledger any
	if err := json.Unmarshal([]byte(regressRead(t, env.ledgerPath)), &ledger); err != nil {
		t.Fatalf("decode ledger: %v", err)
	}
	if got := regressCountRecords(t, ledger); got != 2 {
		t.Fatalf("ledger records = %d, want 2", got)
	}
}

// An unsafe destination stops the run once, before any skill is attempted,
// rather than being reported as a failure of every skill.
func TestHardeningUnsafeDestinationStopsOnce(t *testing.T) {
	regressRequireNonRoot(t)
	env := newRegressEnv(t)
	source := filepath.Join(env.root, "src")
	for _, name := range []string{"alpha", "beta"} {
		regressSkill(t, filepath.Join(source, name), name, "body")
	}
	dest := filepath.Join(env.root, "dest")
	regressMkdir(t, dest)
	if err := os.Chmod(dest, hardeningWorldWritableMode); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	code, stdout, stderr := regressRun(t, source, dest)
	regressExpectCode(t, "ms into a world-writable destination", code, regressExitError, stdout, stderr)
	if got := strings.Count(stderr, "world-writable"); got != 1 {
		t.Fatalf("refusal reported %d times, want once:\n%s", got, stderr)
	}
	if entries := regressDirEntries(t, dest); len(entries) != 0 {
		t.Fatalf("destination written: %v", entries)
	}
	regressAbsent(t, env.ledgerPath)
}
