package ingest

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const unreadableMode = 0o000

func writeSkill(t *testing.T, directory, name string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\n---\nbody\n"
	if name == "" {
		content = "---\ndescription: no name\n---\nbody\n"
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeInvalidYAMLSkill(t *testing.T, directory string) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: broken\ndescription: use it: now\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setSkillModTime(t *testing.T, directory string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(filepath.Join(directory, "SKILL.md"), modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, linkPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}
}

func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func requireNonRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user: root ignores directory permissions")
	}
}

// makeUnreadable removes every permission bit and restores them at cleanup so
// t.TempDir can delete the tree.
func makeUnreadable(t *testing.T, directory string) {
	t.Helper()
	if err := os.Chmod(directory, unreadableMode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(directory, 0o755); err != nil {
			t.Errorf("restore mode of %s: %v", directory, err)
		}
	})
}

func discover(t *testing.T, source string, options DiscoverOptions) DiscoverResult {
	t.Helper()
	result, err := Discover(source, FormatClaudeCode, options)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func skillNames(skills []SourceSkill) []string {
	names := make([]string, 0, len(skills))
	for _, skill := range skills {
		names = append(names, skill.Name)
	}
	return names
}

func TestDiscover_FlatAndNested(t *testing.T) {
	source := t.TempDir()
	writeSkill(t, filepath.Join(source, "skills", "git-ops"), "git-ops")
	writeSkill(t, filepath.Join(source, "plugins", "p", "skills", "foo"), "foo")

	result := discover(t, source, DiscoverOptions{})
	if len(result.Warnings) != 0 || result.Skipped != 0 {
		t.Fatalf("unexpected warnings: %v (skipped %d)", result.Warnings, result.Skipped)
	}
	skills := result.Skills
	if len(skills) != 2 || skills[0].Name != "foo" || skills[1].Name != "git-ops" {
		t.Fatalf("got %+v", skills)
	}
	if !skills[0].FromPlugin || skills[1].FromPlugin {
		t.Fatalf("FromPlugin wrong: %+v", skills)
	}
}

func TestDiscover_StopsAtSkill(t *testing.T) {
	source := t.TempDir()
	outer := filepath.Join(source, "outer")
	writeSkill(t, outer, "outer")
	writeSkill(t, filepath.Join(outer, "sub"), "inner")

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 || result.Skills[0].Name != "outer" {
		t.Fatalf("got %+v", result.Skills)
	}
	if len(result.Warnings) != 0 || result.Skipped != 0 {
		t.Fatalf("unexpected warnings: %v (skipped %d)", result.Warnings, result.Skipped)
	}
}

func TestDiscover_NameFromFrontmatter(t *testing.T) {
	source := t.TempDir()
	directory := filepath.Join(source, "Weird_Name")
	writeSkill(t, directory, "weird")
	info, err := os.Stat(filepath.Join(directory, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}

	result := discover(t, source, DiscoverOptions{})
	want := SourceSkill{Name: "weird", Dir: resolved(t, directory), FromPlugin: false, ModTime: info.ModTime()}
	if len(result.Skills) != 1 {
		t.Fatalf("got %+v, want %+v", result.Skills, want)
	}
	got := result.Skills[0]
	if got.Name != want.Name || got.Dir != want.Dir || got.FromPlugin != want.FromPlugin || !got.ModTime.Equal(want.ModTime) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestDiscover_SkipsNonConforming(t *testing.T) {
	source := t.TempDir()
	writeSkill(t, filepath.Join(source, "noname"), "")
	writeSkill(t, filepath.Join(source, "bad"), "Bad_Name")
	writeSkill(t, filepath.Join(source, "good"), "good")

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 || result.Skills[0].Name != "good" {
		t.Fatalf("got %+v", result.Skills)
	}
	if len(result.Warnings) != 2 || result.Skipped != 2 {
		t.Fatalf("want 2 warnings and skipped 2, got %v (skipped %d)", result.Warnings, result.Skipped)
	}
}

func TestDiscover_PrunesVendorDirs(t *testing.T) {
	source := t.TempDir()
	for _, pruned := range []string{".git", "node_modules", "vendor", "dist", "build"} {
		writeSkill(t, filepath.Join(source, pruned, "hidden"), "hidden-"+pruned[len(pruned)-1:])
	}

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 0 || len(result.Warnings) != 0 || result.Skipped != 0 {
		t.Fatalf("got %+v, warnings %v (skipped %d)", result.Skills, result.Warnings, result.Skipped)
	}
}

func TestDiscover_DedupesByName(t *testing.T) {
	source := t.TempDir()
	writeSkill(t, filepath.Join(source, "a", "one"), "dup")
	writeSkill(t, filepath.Join(source, "b", "two"), "dup")
	// A newer personal duplicate must not win: mtime only ranks plugin copies.
	setSkillModTime(t, filepath.Join(source, "b", "two"), time.Now().Add(time.Hour))

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 || len(result.Warnings) != 1 || result.Skipped != 1 {
		t.Fatalf("got %+v, warnings %v (skipped %d)", result.Skills, result.Warnings, result.Skipped)
	}
	if result.Skills[0].Dir != filepath.Join(resolved(t, source), "a", "one") {
		t.Fatalf("first discovered should win, got %s", result.Skills[0].Dir)
	}
}

func TestDiscover_UnknownFormat(t *testing.T) {
	if _, err := Discover(t.TempDir(), "copilot", DiscoverOptions{}); err == nil {
		t.Fatal("expected error for unknown format")
	}
}

func TestDiscover_ResolvesSymlinkedSourceRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	writeSkill(t, filepath.Join(real, "skills", "foo"), "foo")
	absoluteLink := filepath.Join(base, "absolute-link")
	symlink(t, real, absoluteLink)
	relativeLink := filepath.Join(base, "relative-link")
	symlink(t, "real", relativeLink)
	wantDir := filepath.Join(resolved(t, real), "skills", "foo")

	check := func(t *testing.T, source string) {
		t.Helper()
		result := discover(t, source, DiscoverOptions{})
		if len(result.Skills) != 1 || result.Skills[0].Dir != wantDir {
			t.Fatalf("source %s: got %+v, want one skill at %s", source, result.Skills, wantDir)
		}
		if len(result.Warnings) != 0 || result.Skipped != 0 {
			t.Fatalf("source %s: unexpected warnings %v (skipped %d)", source, result.Warnings, result.Skipped)
		}
	}
	for _, source := range []string{
		absoluteLink,
		absoluteLink + string(filepath.Separator),
		relativeLink,
		relativeLink + string(filepath.Separator),
	} {
		check(t, source)
	}
	t.Run("relative source argument", func(t *testing.T) {
		t.Chdir(base)
		check(t, "absolute-link")
		check(t, "relative-link"+string(filepath.Separator))
	})
}

func TestDiscover_RootMustBeDirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(file, FormatClaudeCode, DiscoverOptions{}); err == nil {
		t.Fatal("expected error for a file root")
	}
	if _, err := Discover(filepath.Join(t.TempDir(), "missing"), FormatClaudeCode, DiscoverOptions{}); err == nil {
		t.Fatal("expected error for a missing root")
	}
}

func TestDiscover_WarnsOnSymlinkedSkillDirByDefault(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	outside := filepath.Join(base, "outside", "linked-skill")
	writeSkill(t, outside, "linked")
	writeSkill(t, filepath.Join(source, "skills", "plain"), "plain")
	symlink(t, outside, filepath.Join(source, "skills", "linked"))
	notes := filepath.Join(base, "notes.md")
	if err := os.WriteFile(notes, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlinked file is irrelevant to discovery and must not warn.
	symlink(t, notes, filepath.Join(source, "skills", "notes.md"))

	result := discover(t, source, DiscoverOptions{})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"plain"}) {
		t.Fatalf("skills = %v, want [plain]", names)
	}
	wantWarning := "skipping symlink " + filepath.Join(resolved(t, source), "skills", "linked") + " (use --follow-symlinks)"
	if !slices.Equal(result.Warnings, []string{wantWarning}) {
		t.Fatalf("warnings = %q, want [%q]", result.Warnings, wantWarning)
	}
	if result.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", result.Skipped)
	}
}

func TestDiscover_FollowSymlinksIngestsLinkedSkillOnce(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "source")
	outside := filepath.Join(base, "outside", "linked-skill")
	writeSkill(t, outside, "linked")
	writeSkill(t, filepath.Join(source, "skills", "plain"), "plain")
	symlink(t, outside, filepath.Join(source, "skills", "linked"))
	symlink(t, outside, filepath.Join(source, "skills", "linked-again"))
	// A link back to the root must terminate rather than recurse forever.
	symlink(t, ".", filepath.Join(source, "a"))

	result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"linked", "plain"}) {
		t.Fatalf("skills = %v, want [linked plain]", names)
	}
	if result.Skills[0].Dir != resolved(t, outside) {
		t.Fatalf("linked Dir = %s, want %s", result.Skills[0].Dir, resolved(t, outside))
	}
	// Re-reaching a visited directory loses nothing, so it warns without
	// counting as a skip.
	if result.Skipped != 0 || len(result.Warnings) != 2 {
		t.Fatalf("want two same-directory notes and no skips, got %v (skipped %d)", result.Warnings, result.Skipped)
	}
	wantLoop := filepath.Join(resolved(t, source), "a") + " is the same directory as " + resolved(t, source) + "; ingesting it once"
	wantAgain := filepath.Join(resolved(t, source), "skills", "linked-again") + " is the same directory as " +
		filepath.Join(resolved(t, source), "skills", "linked") + "; ingesting it once"
	if !slices.Equal(result.Warnings, []string{wantLoop, wantAgain}) {
		t.Fatalf("warnings = %q, want [%q %q]", result.Warnings, wantLoop, wantAgain)
	}
}

func TestDiscover_FollowSymlinksRefusesLinkToAncestorOfRoot(t *testing.T) {
	base := t.TempDir()
	source := filepath.Join(base, "home", "claude")
	writeSkill(t, filepath.Join(source, "skills", "plain"), "plain")
	writeSkill(t, filepath.Join(base, "home", "elsewhere", "stray"), "stray")
	upLink := filepath.Join(source, "up")
	symlink(t, "..", upLink)
	baseLink := filepath.Join(source, "skills", "base")
	symlink(t, base, baseLink)

	result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"plain"}) {
		t.Fatalf("skills = %v, want [plain]", names)
	}
	realSource := resolved(t, source)
	want := []string{
		"refusing to follow " + filepath.Join(realSource, "skills", "base") + ": it points to an ancestor of the source (" + resolved(t, base) + ")",
		"refusing to follow " + filepath.Join(realSource, "up") + ": it points to an ancestor of the source (" + filepath.Dir(realSource) + ")",
	}
	if !slices.Equal(result.Warnings, want) {
		t.Fatalf("warnings = %q, want %q", result.Warnings, want)
	}
	if result.Skipped != len(want) {
		t.Fatalf("skipped = %d, want %d", result.Skipped, len(want))
	}
}

func TestDiscover_PluginSkillReachedThroughPersonalLinkStaysPlugin(t *testing.T) {
	source := t.TempDir()
	plugin := filepath.Join(source, "plugins", "p", "skills", "n")
	writeSkill(t, plugin, "n")
	// "a-skills" sorts before "plugins", so the link reaches the plugin skill first.
	symlink(t, plugin, filepath.Join(source, "a-skills", "n"))

	t.Run("alone it is recorded as plugin", func(t *testing.T) {
		result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
		if len(result.Skills) != 1 || result.Skills[0].Dir != resolved(t, plugin) || !result.Skills[0].FromPlugin {
			t.Fatalf("got %+v, want one plugin skill at %s", result.Skills, plugin)
		}
		if result.Skipped != 0 {
			t.Fatalf("skipped = %d, want 0: %v", result.Skipped, result.Warnings)
		}
	})

	t.Run("the real personal skill wins", func(t *testing.T) {
		personal := filepath.Join(source, "skills", "n")
		writeSkill(t, personal, "n")
		result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
		if len(result.Skills) != 1 || result.Skills[0].Dir != resolved(t, personal) || result.Skills[0].FromPlugin {
			t.Fatalf("got %+v, want the personal skill at %s", result.Skills, personal)
		}
		if result.Skipped != 1 {
			t.Fatalf("skipped = %d, want 1 (the plugin duplicate): %v", result.Skipped, result.Warnings)
		}
		wantDuplicate := "skipping " + resolved(t, plugin) + ": duplicate skill name \"n\" (keeping " + resolved(t, personal) + ")"
		if !slices.Contains(result.Warnings, wantDuplicate) {
			t.Fatalf("warnings = %q, want %q", result.Warnings, wantDuplicate)
		}
	})
}

// A personal skill reached first through a link under plugins/ is classed as
// plugin: "plugin if either path says plugin" never under-reports plugin
// origin, at the cost of over-reporting this unusual layout.
func TestDiscover_PersonalSkillReachedThroughPluginLinkIsStillClassifiedConservatively(t *testing.T) {
	source := t.TempDir()
	personal := filepath.Join(source, "skills", "x")
	writeSkill(t, personal, "x")
	symlink(t, filepath.Join("..", "..", "skills", "x"), filepath.Join(source, "plugins", "p", "x"))

	result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
	if len(result.Skills) != 1 {
		t.Fatalf("got %+v, want one skill", result.Skills)
	}
	skill := result.Skills[0]
	if skill.Dir != resolved(t, personal) || !skill.FromPlugin {
		t.Fatalf("got %+v, want Dir %s with FromPlugin true", skill, personal)
	}
	realSource := resolved(t, source)
	wantNote := filepath.Join(realSource, "skills", "x") + " is the same directory as " + filepath.Join(realSource, "plugins", "p", "x") + "; ingesting it once"
	if !slices.Equal(result.Warnings, []string{wantNote}) || result.Skipped != 0 {
		t.Fatalf("warnings = %q (skipped %d), want [%q] and 0", result.Warnings, result.Skipped, wantNote)
	}
}

func TestDiscover_SameRealDirectoryViaTwoSpellingsIsOneCandidate(t *testing.T) {
	source := t.TempDir()
	real := filepath.Join(source, "skills", "x")
	writeSkill(t, real, "x")
	symlink(t, "skills", filepath.Join(source, "alias"))

	result := discover(t, source, DiscoverOptions{FollowSymlinks: true})
	if len(result.Skills) != 1 || result.Skills[0].Dir != resolved(t, real) || result.Skills[0].FromPlugin {
		t.Fatalf("got %+v, want one personal skill at %s", result.Skills, real)
	}
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "duplicate") {
			t.Fatalf("one real directory must not produce a duplicate: %q", warning)
		}
	}
	if result.Skipped != 0 || len(result.Warnings) != 1 {
		t.Fatalf("want one same-directory note and no skips, got %v (skipped %d)", result.Warnings, result.Skipped)
	}
}

func TestDiscover_UnreadableSubdirWarnsAndContinues(t *testing.T) {
	requireNonRoot(t)
	source := t.TempDir()
	writeSkill(t, filepath.Join(source, "good"), "good")
	locked := filepath.Join(source, "locked")
	writeSkill(t, filepath.Join(locked, "hidden"), "hidden")
	makeUnreadable(t, locked)

	result := discover(t, source, DiscoverOptions{})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"good"}) {
		t.Fatalf("skills = %v, want [good]", names)
	}
	if len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], locked) || result.Skipped != 1 {
		t.Fatalf("want one warning naming %s, got %v (skipped %d)", locked, result.Warnings, result.Skipped)
	}
}

func TestDiscover_UnreadableRootErrors(t *testing.T) {
	requireNonRoot(t)
	source := t.TempDir()
	writeSkill(t, filepath.Join(source, "good"), "good")
	makeUnreadable(t, source)

	_, err := Discover(source, FormatClaudeCode, DiscoverOptions{})
	if err == nil {
		t.Fatal("expected an error for an unreadable root")
	}
	if !strings.Contains(err.Error(), source) {
		t.Fatalf("error should name %s: %v", source, err)
	}
}

func TestDiscover_PersonalBeatsPlugin(t *testing.T) {
	source := t.TempDir()
	plugin := filepath.Join(source, "plugins", "p", "skills", "shared")
	personal := filepath.Join(source, "skills", "shared")
	writeSkill(t, plugin, "shared")
	writeSkill(t, personal, "shared")
	// The plugin copy sorts first and is newer, yet personal still wins.
	setSkillModTime(t, plugin, time.Now().Add(time.Hour))

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 {
		t.Fatalf("got %+v", result.Skills)
	}
	winner := result.Skills[0]
	if winner.Dir != resolved(t, personal) || winner.FromPlugin {
		t.Fatalf("winner = %+v, want personal %s", winner, personal)
	}
	if result.Skipped != 1 || len(result.Warnings) != 1 {
		t.Fatalf("want one duplicate skip, got %v (skipped %d)", result.Warnings, result.Skipped)
	}
	warning := result.Warnings[0]
	if !strings.Contains(warning, resolved(t, plugin)) || !strings.Contains(warning, resolved(t, personal)) {
		t.Fatalf("duplicate warning should name both paths: %q", warning)
	}
}

func TestDiscover_NewestPluginVersionWins(t *testing.T) {
	source := t.TempDir()
	older := filepath.Join(source, "plugins", "cache", "p", "1.10", "skills", "tool")
	newer := filepath.Join(source, "plugins", "cache", "p", "1.9", "skills", "tool")
	writeSkill(t, older, "tool")
	writeSkill(t, newer, "tool")
	now := time.Now()
	setSkillModTime(t, older, now.Add(-time.Hour))
	setSkillModTime(t, newer, now)

	result := discover(t, source, DiscoverOptions{})
	if len(result.Skills) != 1 {
		t.Fatalf("got %+v", result.Skills)
	}
	winner := result.Skills[0]
	if winner.Dir != resolved(t, newer) || !winner.FromPlugin {
		t.Fatalf("winner = %+v, want newest plugin copy %s", winner, newer)
	}
	if !winner.ModTime.Equal(now) {
		t.Fatalf("ModTime = %v, want %v", winner.ModTime, now)
	}
	if result.Skipped != 1 || len(result.Warnings) != 1 || !strings.Contains(result.Warnings[0], resolved(t, older)) {
		t.Fatalf("want one warning naming %s, got %v (skipped %d)", older, result.Warnings, result.Skipped)
	}
}

func TestDiscover_InvalidYAMLFrontmatterSkippedWithHint(t *testing.T) {
	source := t.TempDir()
	broken := filepath.Join(source, "skills", "broken")
	writeInvalidYAMLSkill(t, broken)
	writeSkill(t, filepath.Join(source, "skills", "good"), "good")

	result := discover(t, source, DiscoverOptions{})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"good"}) {
		t.Fatalf("skills = %v, want [good]", names)
	}
	if result.Skipped != 1 || len(result.Warnings) != 1 {
		t.Fatalf("want one skip, got %v (skipped %d)", result.Warnings, result.Skipped)
	}
	warning := result.Warnings[0]
	for _, want := range []string{filepath.Join(resolved(t, broken), "SKILL.md"), "mapping values are not allowed", "valid YAML"} {
		if !strings.Contains(warning, want) {
			t.Fatalf("warning %q lacks %q", warning, want)
		}
	}
}

func TestDiscover_SkippedCountsEveryReason(t *testing.T) {
	requireNonRoot(t)
	base := t.TempDir()
	source := filepath.Join(base, "source")
	writeSkill(t, filepath.Join(source, "good"), "good")
	writeSkill(t, filepath.Join(source, "bad-name"), "Bad_Name")
	writeSkill(t, filepath.Join(source, "no-name"), "")
	writeInvalidYAMLSkill(t, filepath.Join(source, "bad-yaml"))
	writeSkill(t, filepath.Join(source, "dup-one"), "dup")
	writeSkill(t, filepath.Join(source, "dup-two"), "dup")
	outside := filepath.Join(base, "outside")
	writeSkill(t, outside, "outside")
	symlink(t, outside, filepath.Join(source, "linked"))
	locked := filepath.Join(source, "locked")
	writeSkill(t, filepath.Join(locked, "hidden"), "hidden")
	makeUnreadable(t, locked)

	result := discover(t, source, DiscoverOptions{})
	if names := skillNames(result.Skills); !slices.Equal(names, []string{"dup", "good"}) {
		t.Fatalf("skills = %v, want [dup good]", names)
	}
	const wantSkipped = 6
	if result.Skipped != wantSkipped || len(result.Warnings) != wantSkipped {
		t.Fatalf("want %d skips with one warning each, got %d: %v", wantSkipped, result.Skipped, result.Warnings)
	}
}
