package mscli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// A skill whose SKILL.md is an absolute symlink would arrive in the store
// without its manifest; ms refuses it at discovery, so a dry run reports it
// too, and nothing is written or ledgered.
func TestSourceSafetyIngestRefusesLinkedSkillFile(t *testing.T) {
	sandbox := newRegressEnv(t)
	source := filepath.Join(sandbox.root, "src")
	skillDir := filepath.Join(source, "skills", "alpha")
	regressWrite(t, filepath.Join(skillDir, "notes.txt"), "notes")
	manifest := filepath.Join(sandbox.root, "elsewhere", "SKILL.md")
	regressSkill(t, filepath.Dir(manifest), "alpha", "outside the skill")
	if err := os.Symlink(manifest, filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--dry-run", source}, {source}} {
		code, stdout, stderr := regressRun(t, args...)
		regressExpectCode(t, strings.Join(args, " "), code, regressExitOK, stdout, stderr)
		regressContains(t, "summary", stdout, "ms: +0 (skipped 1)")
		regressContains(t, "stderr", stderr, "SKILL.md is a symlink that leaves the skill; not ingested")
	}
	regressAbsent(t, filepath.Join(sandbox.store, "alpha"))
	regressAbsent(t, sandbox.ledgerPath)
}

// Directory names under a plugin tree are third-party input: printed raw,
// escape sequences in them could rewrite or hide ms's own messages.
func TestSourceSafetyIngestOutputIsTerminalSafe(t *testing.T) {
	sandbox := newRegressEnv(t)
	source := filepath.Join(sandbox.root, "src")
	hostile := filepath.Join(source, "plugins", "evil\x1b]0;owned\x07\x1b[2K\r")
	regressWrite(t, filepath.Join(hostile, "SKILL.md"), "no frontmatter\n")
	regressSkill(t, filepath.Join(source, "skills", "alpha"), "alpha", "body")

	code, stdout, stderr := regressRun(t, source)
	regressExpectCode(t, "ms", code, regressExitOK, stdout, stderr)
	regressContains(t, "summary", stdout, "ms: +1 (skipped 1)")
	for _, line := range strings.Split(strings.TrimSuffix(stderr, "\n"), "\n") {
		if !utf8.ValidString(line) {
			t.Fatalf("stderr line is not valid UTF-8: %q", line)
		}
		for _, character := range line {
			if unicode.IsControl(character) {
				t.Fatalf("stderr line carries raw control character %U: %q", character, line)
			}
		}
	}
	regressContains(t, "stderr", stderr, `\x1b`)
}

// s1/SKILL.md -> docs/m.md with s1/docs -> a directory outside the skill: the
// dry run and the real run must both skip it with the same counts.
func TestSourceSafetyIngestRefusesManifestThroughLinkedDirectory(t *testing.T) {
	sandbox := newRegressEnv(t)
	source := filepath.Join(sandbox.root, "src")
	skillDir := filepath.Join(source, "s1")
	regressWrite(t, filepath.Join(skillDir, "notes.txt"), "notes")
	outside := filepath.Join(sandbox.root, "outside")
	regressWrite(t, filepath.Join(outside, "m.md"), "---\nname: s1\n---\nbody\n")
	if err := os.Symlink(outside, filepath.Join(skillDir, "docs")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("docs", "m.md"), filepath.Join(skillDir, "SKILL.md")); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--dry-run", source}, {source}} {
		code, stdout, stderr := regressRun(t, args...)
		regressExpectCode(t, strings.Join(args, " "), code, regressExitOK, stdout, stderr)
		regressContains(t, "summary", stdout, "ms: +0 (skipped 1)")
		regressContains(t, "stderr", stderr, "SKILL.md is a symlink that leaves the skill; not ingested")
	}
	regressAbsent(t, filepath.Join(sandbox.store, "s1"))
	regressAbsent(t, sandbox.ledgerPath)
}

// manifestLayout builds one skill directory s1 whose SKILL.md is reached in a
// particular way.
type manifestLayout struct {
	name     string
	ingested bool
	build    func(t *testing.T, skillDir, outside string)
	// skipMessage is the warning a skipped layout must produce; empty means
	// the "leaves the skill" refusal.
	skipMessage string
}

// osRootSymlinkLimit is how many links os.Root follows (Go's
// rootMaxSymlinks); the copy's verification resolves SKILL.md through it.
const osRootSymlinkLimit = 8

// linkChain writes m0 (the manifest) and links m1..mN, each to the previous
// one, with SKILL.md pointing at mN: a chain of links+1 links in total.
func linkChain(t *testing.T, skillDir string, links int) {
	t.Helper()
	regressWrite(t, filepath.Join(skillDir, "m0"), layoutManifest)
	for index := 1; index <= links; index++ {
		layoutSymlink(t, fmt.Sprintf("m%d", index-1), filepath.Join(skillDir, fmt.Sprintf("m%d", index)))
	}
	layoutSymlink(t, fmt.Sprintf("m%d", links), filepath.Join(skillDir, "SKILL.md"))
}

func layoutSymlink(t *testing.T, target, linkPath string) {
	t.Helper()
	regressMkdir(t, filepath.Dir(linkPath))
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}
}

const layoutManifest = "---\nname: s1\n---\nbody\n"

// Discovery and the copy must apply one rule to a SKILL.md reached through
// links: whatever discovery accepts, the copy reproduces with a regular
// SKILL.md, so a dry run and a real run report the same counts and every
// ingested skill in the store has its manifest.
func TestSourceSafetyIngestManifestLayoutsAgreeWithCopy(t *testing.T) {
	layouts := []manifestLayout{
		{"plain relative in-skill link", true, func(t *testing.T, skillDir, outside string) {
			regressWrite(t, filepath.Join(skillDir, "docs", "m.md"), layoutManifest)
			layoutSymlink(t, filepath.Join("docs", "m.md"), filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"dot-dot after a name", false, func(t *testing.T, skillDir, outside string) {
			regressMkdir(t, filepath.Join(skillDir, "sub"))
			regressWrite(t, filepath.Join(skillDir, "m.md"), layoutManifest)
			layoutSymlink(t, "sub/../m.md", filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"chain through a dot-dot link", false, func(t *testing.T, skillDir, outside string) {
			regressMkdir(t, filepath.Join(skillDir, "sub"))
			regressWrite(t, filepath.Join(skillDir, "m.md"), layoutManifest)
			layoutSymlink(t, "sub/../m.md", filepath.Join(skillDir, "x.md"))
			layoutSymlink(t, "x.md", filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"directory link with dot-dot en route", false, func(t *testing.T, skillDir, outside string) {
			regressMkdir(t, filepath.Join(skillDir, "x"))
			regressWrite(t, filepath.Join(skillDir, "real", "m.md"), layoutManifest)
			layoutSymlink(t, "x/../real", filepath.Join(skillDir, "d"))
			layoutSymlink(t, filepath.Join("d", "m.md"), filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"directory link leaving the skill", false, func(t *testing.T, skillDir, outside string) {
			regressWrite(t, filepath.Join(outside, "m.md"), layoutManifest)
			layoutSymlink(t, outside, filepath.Join(skillDir, "docs"))
			layoutSymlink(t, filepath.Join("docs", "m.md"), filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"chain at the os.Root link limit", true, func(t *testing.T, skillDir, outside string) {
			linkChain(t, skillDir, osRootSymlinkLimit-1)
		}, ""},
		{"chain one past the os.Root link limit", false, func(t *testing.T, skillDir, outside string) {
			linkChain(t, skillDir, osRootSymlinkLimit)
		}, "SKILL.md cannot be resolved inside the skill"},
		{"link to a mixed-case marker name", false, func(t *testing.T, skillDir, outside string) {
			regressWrite(t, filepath.Join(skillDir, ".SM-COPY"), layoutManifest)
			layoutSymlink(t, ".SM-COPY", filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"marker reached through a self link", false, func(t *testing.T, skillDir, outside string) {
			regressWrite(t, filepath.Join(skillDir, ".sm-copy"), layoutManifest)
			layoutSymlink(t, ".", filepath.Join(skillDir, "self"))
			layoutSymlink(t, filepath.Join("self", ".sm-copy"), filepath.Join(skillDir, "SKILL.md"))
		}, ""},
		{"inside a mixed-case sentinel directory", false, func(t *testing.T, skillDir, outside string) {
			regressWrite(t, filepath.Join(skillDir, ".Sm-Stage-Sentinel", "m.md"), layoutManifest)
			layoutSymlink(t, filepath.Join(".Sm-Stage-Sentinel", "m.md"), filepath.Join(skillDir, "SKILL.md"))
		}, ""},
	}
	for _, layout := range layouts {
		t.Run(layout.name, func(t *testing.T) {
			sandbox := newRegressEnv(t)
			source := filepath.Join(sandbox.root, "src")
			skillDir := filepath.Join(source, "s1")
			regressWrite(t, filepath.Join(skillDir, "notes.txt"), "notes")
			layout.build(t, skillDir, filepath.Join(sandbox.root, "outside"))
			want := "ms: +0 (skipped 1)"
			if layout.ingested {
				want = "ms: +1 (skipped 0)"
			}

			code, dryStdout, dryStderr := regressRun(t, "--dry-run", source)
			regressExpectCode(t, "dry run", code, regressExitOK, dryStdout, dryStderr)
			code, realStdout, realStderr := regressRun(t, source)
			regressExpectCode(t, "real run", code, regressExitOK, realStdout, realStderr)
			if dryStdout != realStdout {
				t.Fatalf("dry run %q != real run %q", dryStdout, realStdout)
			}
			regressContains(t, "summary", realStdout, want)
			ingested := filepath.Join(sandbox.store, "s1")
			if !layout.ingested {
				message := layout.skipMessage
				if message == "" {
					message = "SKILL.md is a symlink that leaves the skill; not ingested"
				}
				regressContains(t, "dry-run stderr", dryStderr, message)
				regressContains(t, "stderr", realStderr, message)
				regressAbsent(t, ingested)
				return
			}
			info, err := os.Stat(filepath.Join(ingested, "SKILL.md"))
			if err != nil || !info.Mode().IsRegular() {
				t.Fatalf("ingested skill has no regular SKILL.md: %v, %v", info, err)
			}
		})
	}
}
