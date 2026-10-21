package ingest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

const leavingSkillFileWarning = "SKILL.md is a symlink that leaves the skill; not ingested"

// identityOf is the identity discovery records for path.
func identityOf(t *testing.T, path string) fs.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info
}

// sourceSkillAt builds the SourceSkill Discover would report for dir.
func sourceSkillAt(t *testing.T, name, dir string) SourceSkill {
	t.Helper()
	real := resolved(t, dir)
	return SourceSkill{Name: name, Dir: real, Identity: identityOf(t, real)}
}

// linkSkillFile replaces dir/SKILL.md with a symlink to target.
func linkSkillFile(t *testing.T, dir, target string) {
	t.Helper()
	skillFile := filepath.Join(dir, "SKILL.md")
	if err := os.Remove(skillFile); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, skillFile); err != nil {
		t.Fatal(err)
	}
}

// writeNestedFile writes path, creating its parent directories.
func writeNestedFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTextFile(t, path, content)
}

func assertTerminalSafe(t *testing.T, text string) {
	t.Helper()
	if !utf8.ValidString(text) {
		t.Fatalf("text is not valid UTF-8: %q", text)
	}
	for _, character := range text {
		if unicode.IsControl(character) {
			t.Fatalf("text carries raw control character %U: %q", character, text)
		}
	}
}

func TestDiscover_RefusesSkillFileLinkLeavingTheSkill(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	outside := filepath.Join(root, "outside")
	writeSkill(t, outside, "elsewhere")
	outsideManifest := filepath.Join(outside, "SKILL.md")

	absoluteSkill := filepath.Join(source, "skills", "absolute")
	writeSkill(t, absoluteSkill, "absolute")
	linkSkillFile(t, absoluteSkill, outsideManifest)

	escapingSkill := filepath.Join(source, "skills", "escaping")
	writeSkill(t, escapingSkill, "escaping")
	writeSkill(t, filepath.Join(source, "skills", "shared"), "escaping")
	linkSkillFile(t, escapingSkill, filepath.Join("..", "shared", "SKILL.md"))

	insideSkill := filepath.Join(source, "skills", "inside")
	writeSkill(t, insideSkill, "inside")
	writeNestedFile(t, filepath.Join(insideSkill, "docs", "manifest.md"), "---\nname: inside\n---\nbody\n")
	linkSkillFile(t, insideSkill, filepath.Join("docs", "manifest.md"))

	result := discover(t, source, DiscoverOptions{})
	names := make([]string, 0, len(result.Skills))
	for _, skill := range result.Skills {
		names = append(names, skill.Name)
	}
	// "shared" is a valid skill named escaping, so it wins the name once the
	// linked one is refused; "inside" keeps its in-skill link.
	if strings.Join(names, ",") != "escaping,inside" {
		t.Fatalf("skills = %v, want escaping (the real one) and inside", names)
	}
	for _, skill := range result.Skills {
		if skill.Name == "escaping" && skill.Dir != resolved(t, filepath.Join(source, "skills", "shared")) {
			t.Fatalf("escaping came from %s, want the shared directory", skill.Dir)
		}
	}
	for _, refused := range []string{absoluteSkill, escapingSkill} {
		found := false
		for _, warning := range result.Warnings {
			if strings.Contains(warning, refused) && strings.Contains(warning, leavingSkillFileWarning) {
				found = true
			}
		}
		if !found {
			t.Fatalf("no %q warning for %s in %v", leavingSkillFileWarning, refused, result.Warnings)
		}
	}
	if result.Skipped != 2 {
		t.Fatalf("Skipped = %d, want 2", result.Skipped)
	}
}

func TestWriteSkill_RefusesCopyWithoutRegularSkillFile(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "source", "alpha")
	writeSkill(t, skillDir, "alpha")
	writeTextFile(t, filepath.Join(skillDir, "notes.txt"), "notes")
	manifest := filepath.Join(root, "elsewhere", "SKILL.md")
	writeSkill(t, filepath.Dir(manifest), "alpha")
	linkSkillFile(t, skillDir, manifest)
	destination := filepath.Join(root, "store")

	result, err := WriteSkill(sourceSkillAt(t, "alpha", skillDir), destination, false, false)
	if err == nil {
		t.Fatalf("WriteSkill = %+v; want an error for a copy without a regular SKILL.md", result)
	}
	if !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("error %q does not name SKILL.md", err)
	}
	assertExists(t, filepath.Join(destination, "alpha"), false)
	entries, readErr := os.ReadDir(destination)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("destination holds leftovers: %v", entries)
	}
}

func TestWriteSkill_KeepsInSkillSkillFileLink(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "source", "alpha")
	writeSkill(t, skillDir, "alpha")
	manifestBody := "---\nname: alpha\n---\nfrom docs\n"
	writeNestedFile(t, filepath.Join(skillDir, "docs", "manifest.md"), manifestBody)
	linkTarget := filepath.Join("docs", "manifest.md")
	linkSkillFile(t, skillDir, linkTarget)
	destination := filepath.Join(root, "store")

	result, err := WriteSkill(sourceSkillAt(t, "alpha", skillDir), destination, false, false)
	if err != nil || !result.Written {
		t.Fatalf("WriteSkill = %+v, %v", result, err)
	}
	written := filepath.Join(destination, "alpha", "SKILL.md")
	if got, err := os.Readlink(written); err != nil || got != linkTarget {
		t.Fatalf("SKILL.md link = %q, %v; want %q", got, err, linkTarget)
	}
	if got := readFileText(t, written); got != manifestBody {
		t.Fatalf("SKILL.md content = %q", got)
	}
}

func TestDiscover_WarningsAreTerminalSafe(t *testing.T) {
	source := t.TempDir()
	hostile := filepath.Join(source, "plugins", "evil\x1b]0;owned\x07\x1b[2J")
	if err := os.MkdirAll(hostile, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTextFile(t, filepath.Join(hostile, "SKILL.md"), "no frontmatter\n")

	result := discover(t, source, DiscoverOptions{})
	if len(result.Warnings) != 1 {
		t.Fatalf("warnings = %q, want one", result.Warnings)
	}
	assertTerminalSafe(t, result.Warnings[0])
	if !strings.Contains(result.Warnings[0], `\x1b`) {
		t.Fatalf("warning %q does not show the escaped control character", result.Warnings[0])
	}
}

func TestDiscover_RecordsSkillIdentity(t *testing.T) {
	source := t.TempDir()
	skillDir := filepath.Join(source, "skills", "alpha")
	writeSkill(t, skillDir, "alpha")

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 {
		t.Fatalf("skills = %+v", result.Skills)
	}
	identity := result.Skills[0].Identity
	if identity == nil || !os.SameFile(identity, identityOf(t, skillDir)) {
		t.Fatalf("identity %v does not describe %s", identity, skillDir)
	}
}

// WriteSkill copies only from the directory discovery validated: a source
// replaced by a link to another directory in between is refused.
func TestWriteSkill_RefusesSourceChangedSinceDiscovery(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "source", "alpha")
	writeSkill(t, skillDir, "alpha")
	skill := sourceSkillAt(t, "alpha", skillDir)
	secret := filepath.Join(root, "secret")
	writeNestedFile(t, filepath.Join(secret, "id_ed25519"), "private key")
	if err := os.Rename(skillDir, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, skillDir); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "store")

	_, err := WriteSkill(skill, destination, false, false)
	if err == nil || !strings.Contains(err.Error(), "source changed since it was validated") {
		t.Fatalf("err = %v, want a source-changed refusal", err)
	}
	assertExists(t, filepath.Join(destination, "alpha"), false)
}

// The SKILL.md link text stays inside the skill, but the directory it goes
// through is itself a link out of it: the manifest still leaves the skill and
// must be refused at discovery, so a dry run and a real run agree.
func TestDiscover_RefusesSkillFileLeavingThroughLinkedDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	skillDir := filepath.Join(source, "s1")
	writeSkill(t, skillDir, "s1")
	outside := filepath.Join(root, "outside")
	writeNestedFile(t, filepath.Join(outside, "m.md"), "---\nname: s1\n---\nbody\n")
	if err := os.Symlink(outside, filepath.Join(skillDir, "docs")); err != nil {
		t.Fatal(err)
	}
	linkSkillFile(t, skillDir, filepath.Join("docs", "m.md"))

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 0 {
		t.Fatalf("skills = %+v, want none", result.Skills)
	}
	if result.Skipped != 1 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], leavingSkillFileWarning) {
		t.Fatalf("skipped %d, warnings %q; want one %q", result.Skipped, result.Warnings, leavingSkillFileWarning)
	}
}

// A manifest chain that cannot be resolved at all (a link loop, a path
// through a regular file) is skipped with a message that says so rather than
// claiming it leaves the skill.
func TestDiscover_UnresolvableManifestIsSkipped(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		build func(t *testing.T, skillDir string)
	}{
		{"loop", func(t *testing.T, skillDir string) {
			if err := os.Symlink("b.md", filepath.Join(skillDir, "a.md")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("a.md", filepath.Join(skillDir, "b.md")); err != nil {
				t.Fatal(err)
			}
			linkSkillFile(t, skillDir, "a.md")
		}},
		{"through a regular file", func(t *testing.T, skillDir string) {
			writeNestedFile(t, filepath.Join(skillDir, "plain.txt"), "not a directory")
			linkSkillFile(t, skillDir, filepath.Join("plain.txt", "m.md"))
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			source := t.TempDir()
			skillDir := filepath.Join(source, "s1")
			writeSkill(t, skillDir, "s1")
			testCase.build(t, skillDir)

			result := discover(t, source, DiscoverOptions{})
			if len(result.Skills) != 0 || result.Skipped != 1 || len(result.Warnings) != 1 {
				t.Fatalf("skills %+v, skipped %d, warnings %q; want one skip", result.Skills, result.Skipped, result.Warnings)
			}
			if !strings.Contains(result.Warnings[0], "SKILL.md cannot be resolved inside the skill") {
				t.Fatalf("warning %q does not say the manifest cannot be resolved", result.Warnings[0])
			}
		})
	}
}
