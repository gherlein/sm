package probe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/gherlein/sm/internal/agents"
)

func neverOnPath(string) bool { return false }

func foundNames(found []Found) []string {
	names := make([]string, 0, len(found))
	for _, entry := range found {
		names = append(names, entry.Name)
	}
	return names
}

func TestDetect_ByConfigDir(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := Detect(agents.Registry(), home, neverOnPath)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(found) != 1 || found[0].Name != "claude-code" {
		t.Fatalf("found = %v, want only claude-code", foundNames(found))
	}
	wantDir, err := agents.Expand("~/.claude/skills")
	if err != nil {
		t.Fatal(err)
	}
	if found[0].Dir != wantDir {
		t.Fatalf("Dir = %q, want %q", found[0].Dir, wantDir)
	}
}

func TestDetect_ByBinary(t *testing.T) {
	onPath := func(binary string) bool { return binary == "copilot" }
	found, err := Detect(agents.Registry(), t.TempDir(), onPath)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(found) != 1 || found[0].Name != "copilot" {
		t.Fatalf("found = %v, want only copilot", foundNames(found))
	}
}

func TestDetect_NoneWhenAbsent(t *testing.T) {
	found, err := Detect(agents.Registry(), t.TempDir(), neverOnPath)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("found = %v, want none", foundNames(found))
	}
}

func TestAddMissing_AppendsConfirmed(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	original := "[agents]\npi = \"/tmp/pi-skills\"\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	found := []Found{{Name: "claude-code", Dir: "/home/u/.claude/skills"}}
	added, err := AddMissing(cfgPath, found, nil, func(Found) bool { return true })
	if err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	if len(added) != 1 || added[0] != "claude-code" {
		t.Fatalf("added = %v", added)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `claude-code = "/home/u/.claude/skills"`) {
		t.Fatalf("content missing entry:\n%s", content)
	}
	config, err := agents.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load after append: %v", err)
	}
	if len(config.Agents) != 2 {
		t.Fatalf("loaded agents = %v, want 2", config.Agents)
	}
}

func TestAddMissing_AppendsAfterMissingTrailingNewline(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(cfgPath, []byte("[agents]\npi = \"/tmp/pi\""), 0o644); err != nil {
		t.Fatal(err)
	}
	found := []Found{{Name: "hax", Dir: "/tmp/hax"}}
	if _, err := AddMissing(cfgPath, found, nil, func(Found) bool { return true }); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	config, err := agents.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(config.Agents) != 2 {
		t.Fatalf("loaded agents = %v, want 2", config.Agents)
	}
}

func TestAddMissing_SkipsDeclined(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	original := "[agents]\n"
	if err := os.WriteFile(cfgPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	found := []Found{{Name: "claude-code", Dir: "/x"}}
	added, err := AddMissing(cfgPath, found, nil, func(Found) bool { return false })
	if err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	if len(added) != 0 {
		t.Fatalf("added = %v, want none", added)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("file changed:\n%s", content)
	}
}

func TestAddMissing_SkipsExisting(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	found := []Found{{Name: "claude-code", Dir: "/x"}}
	existing := []agents.Agent{{Name: "claude-code", Dir: "/elsewhere"}}
	confirmCalls := 0
	added, err := AddMissing(cfgPath, found, existing, func(Found) bool {
		confirmCalls++
		return true
	})
	if err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	if len(added) != 0 || confirmCalls != 0 {
		t.Fatalf("added = %v, confirmCalls = %d; want none", added, confirmCalls)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("file should not be created when nothing is added, stat err = %v", err)
	}
}

func TestAddMissing_CreatesFileWithHeader(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "nested", "agents.toml")
	found := []Found{{Name: "copilot", Dir: "/home/u/.copilot/skills"}}
	added, err := AddMissing(cfgPath, found, nil, func(Found) bool { return true })
	if err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	if len(added) != 1 || added[0] != "copilot" {
		t.Fatalf("added = %v", added)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "[agents]\ncopilot = \"/home/u/.copilot/skills\"\n"
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
	if _, err := agents.Load(cfgPath); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestAddMissing_EmptyFileGetsHeader(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(cfgPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	found := []Found{{Name: "copilot", Dir: "/home/u/.copilot/skills"}}
	if _, err := AddMissing(cfgPath, found, nil, func(Found) bool { return true }); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "[agents]") {
		t.Fatalf("content missing header:\n%s", content)
	}
	config, err := agents.Load(cfgPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(config.Agents) != 1 || config.Agents[0].Name != "copilot" {
		t.Fatalf("loaded agents = %v, want copilot under [agents]", config.Agents)
	}
}

func acceptAll(Found) bool { return true }

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "agents.toml")
	if err := os.WriteFile(cfgPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return cfgPath
}

func loadedNames(t *testing.T, cfgPath string) map[string]string {
	t.Helper()
	config, err := agents.Load(cfgPath)
	if err != nil {
		content, _ := os.ReadFile(cfgPath)
		t.Fatalf("Load: %v\n%s", err, content)
	}
	byName := make(map[string]string, len(config.Agents))
	for _, agent := range config.Agents {
		byName[agent.Name] = agent.Dir
	}
	return byName
}

func TestAddMissing_InsertsInsideAgentsTableBeforeNextTable(t *testing.T) {
	cfgPath := writeConfig(t, "# my agents\n[agents]\npi = \"/tmp/pi\" # keep\n\n[notes]\ntext = \"hello\"\n")
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "# my agents\n[agents]\npi = \"/tmp/pi\" # keep\nhax = \"/tmp/hax\"\n\n[notes]\ntext = \"hello\"\n"
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func TestAddMissing_InsertedKeysAreInAgentsNotNotes(t *testing.T) {
	cfgPath := writeConfig(t, "[agents]\npi = \"/tmp/pi\"\n\n[notes]\n")
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	var parsed map[string]map[string]any
	if _, err := toml.DecodeFile(cfgPath, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["agents"]["hax"] != "/tmp/hax" || len(parsed["notes"]) != 0 {
		t.Fatalf("parsed = %v", parsed)
	}
}

func TestAddMissing_CommentOnlyFileGetsHeader(t *testing.T) {
	cfgPath := writeConfig(t, "# nothing yet")
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "# nothing yet\n\n[agents]\nhax = \"/tmp/hax\"\n"
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
	if got := loadedNames(t, cfgPath); got["hax"] != "/tmp/hax" {
		t.Fatalf("loaded = %v", got)
	}
}

func TestAddMissing_OtherTablesOnlyGetsHeader(t *testing.T) {
	cfgPath := writeConfig(t, "[agents_extra_is_not_agents]\n")
	// Load rejects unknown tables, so only the written text is asserted.
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "[agents_extra_is_not_agents]\n\n[agents]\nhax = \"/tmp/hax\"\n"
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func TestAddMissing_InlineAgentsTableRefused(t *testing.T) {
	layouts := map[string]string{
		"inline":   "agents = { pi = \"/tmp/pi\" }\n",
		"dotted":   "agents.pi = \"/tmp/pi\"\n",
		"quoted":   "\"agents\" = { pi = \"/tmp/pi\" }\n",
		"subtable": "[agents.pi]\n",
	}
	for name, content := range layouts {
		t.Run(name, func(t *testing.T) {
			cfgPath := writeConfig(t, content)
			_, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll)
			if err == nil || !strings.Contains(err.Error(), "by hand") {
				t.Fatalf("err = %v, want edit-by-hand error", err)
			}
			after, readErr := os.ReadFile(cfgPath)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if string(after) != content {
				t.Fatalf("file modified: %q", after)
			}
		})
	}
}

func TestAddMissing_SecondRunIsIdempotent(t *testing.T) {
	layouts := map[string]string{
		"missing":    "",
		"comment":    "# c\n",
		"agents":     "[agents]\npi = \"/tmp/pi\"\n",
		"no-newline": "[agents]\npi = \"/tmp/pi\"",
	}
	found := []Found{{Name: "hax", Dir: "/tmp/hax"}, {Name: "claude-code", Dir: "/tmp/cc"}}
	for name, content := range layouts {
		t.Run(name, func(t *testing.T) {
			cfgPath := writeConfig(t, content)
			if _, err := AddMissing(cfgPath, found, nil, acceptAll); err != nil {
				t.Fatalf("first AddMissing: %v", err)
			}
			config, err := agents.Load(cfgPath)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			added, err := AddMissing(cfgPath, found, config.Agents, acceptAll)
			if err != nil {
				t.Fatalf("second AddMissing: %v", err)
			}
			if len(added) != 0 {
				t.Fatalf("second run added %v", added)
			}
			if got := loadedNames(t, cfgPath); got["hax"] != "/tmp/hax" || got["claude-code"] != "/tmp/cc" {
				t.Fatalf("loaded = %v", got)
			}
		})
	}
}

func TestAddMissing_EscapesQuotesAndBackslashesInDir(t *testing.T) {
	dir := "/tmp/we\"ird\\dir\tx\x01"
	cfgPath := writeConfig(t, "[agents]\n")
	if _, err := AddMissing(cfgPath, []Found{{Name: "we ird\"name", Dir: dir}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	if got := loadedNames(t, cfgPath); got["we ird\"name"] != dir {
		t.Fatalf("loaded = %q, want dir %q", got, dir)
	}
}

func TestAddMissing_RoundTripsThroughLoad(t *testing.T) {
	layouts := map[string]string{
		"header-with-one-key":         "[agents]\npi = \"/tmp/pi\"\n",
		"header-only":                 "[agents]\n",
		"empty":                       "",
		"whitespace":                  "  \n\n",
		"comment-only":                "# a\n# b\n",
		"trailing-comment-after-keys": "[agents]\npi = \"/tmp/pi\"\n# trailing\n",
		"blank-line-ending":           "# c\n\n",
		"spaced-header":               "[ agents ]  # c\npi = \"/tmp/pi\"\n",
	}
	for name, content := range layouts {
		t.Run(name, func(t *testing.T) {
			cfgPath := writeConfig(t, content)
			if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
				t.Fatalf("AddMissing: %v", err)
			}
			if got := loadedNames(t, cfgPath); got["hax"] != "/tmp/hax" {
				t.Fatalf("loaded = %v", got)
			}
			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(content, "\n") {
				if strings.HasPrefix(line, "#") && !strings.Contains(string(after), line) {
					t.Fatalf("comment %q lost:\n%s", line, after)
				}
			}
		})
	}
}

func TestAddMissing_QuotesKeysThatAreNotBare(t *testing.T) {
	cfgPath := writeConfig(t, "[agents]\n")
	if _, err := AddMissing(cfgPath, []Found{{Name: "my_agent-2", Dir: "/tmp/a"}, {Name: "a.b", Dir: "/tmp/b"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "[agents]\nmy_agent-2 = \"/tmp/a\"\n\"a.b\" = \"/tmp/b\"\n"
	if string(content) != want {
		t.Fatalf("content = %q, want %q", content, want)
	}
}

func tempLeftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var leftovers []string
	for _, entry := range entries {
		if entry.Name() != "agents.toml" {
			leftovers = append(leftovers, entry.Name())
		}
	}
	return leftovers
}

func TestAddMissing_WriteIsAtomicAndPreservesMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o600, 0o644} {
		cfgPath := writeConfig(t, "[agents]\n")
		if err := os.Chmod(cfgPath, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
			t.Fatalf("AddMissing: %v", err)
		}
		info, err := os.Stat(cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != mode {
			t.Fatalf("mode = %v, want %v", info.Mode().Perm(), mode)
		}
		if leftovers := tempLeftovers(t, filepath.Dir(cfgPath)); len(leftovers) != 0 {
			t.Fatalf("leftover files: %v", leftovers)
		}
	}
}

func TestAddMissing_PreservesSymlinkedConfig(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "dotfiles", "real.toml")
	if err := os.MkdirAll(filepath.Dir(realPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(realPath, []byte("[agents]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(dir, "agents.toml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := AddMissing(linkPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	target, err := os.Readlink(linkPath)
	if err != nil || target != realPath {
		t.Fatalf("symlink target = %q, err = %v; want %q", target, err, realPath)
	}
	content, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `hax = "/tmp/hax"`) {
		t.Fatalf("real file missing entry:\n%s", content)
	}
}

func TestAddMissing_FailedWriteLeavesOriginalIntact(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Fatal("this test needs a non-root user: root ignores directory permissions")
	}
	original := "# keep me\n[agents]\npi = \"/tmp/pi\"\n"
	cfgPath := writeConfig(t, original)
	dir := filepath.Dir(cfgPath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}()
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err == nil {
		t.Fatal("AddMissing succeeded in a read-only directory")
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("original modified: %q", content)
	}
	if leftovers := tempLeftovers(t, dir); len(leftovers) != 0 {
		t.Fatalf("leftover files: %v", leftovers)
	}
}

func TestAddMissing_RefusedLayoutNeverPrompts(t *testing.T) {
	cfgPath := writeConfig(t, "agents = { pi = \"/tmp/pi\" }\n")
	calls := 0
	_, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, func(Found) bool {
		calls++
		return true
	})
	if err == nil {
		t.Fatal("want error")
	}
	if calls != 0 {
		t.Fatalf("confirm called %d times before layout check", calls)
	}
}

func TestAddMissing_InvalidUTF8DirIsError(t *testing.T) {
	cfgPath := writeConfig(t, "[agents]\n")
	_, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/\xff"}}, nil, acceptAll)
	if err == nil {
		t.Fatal("want error for invalid UTF-8")
	}
	content, readErr := os.ReadFile(cfgPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "[agents]\n" {
		t.Fatalf("file modified: %q", content)
	}
}

func TestAddMissing_AgentsLookalikeInsideMultilineStringIsNotAHeader(t *testing.T) {
	inputs := map[string]string{
		"basic":   "[other]\nt = \"\"\"\n[agents]\n\"\"\"\n",
		"literal": "[other]\nt = '''\n[agents]\n'''\n",
		"array":   "[other]\nt = [\n[agents]\n]\n",
	}
	for name, content := range inputs {
		t.Run(name, func(t *testing.T) {
			cfgPath := writeConfig(t, content)
			if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
				t.Fatalf("AddMissing: %v", err)
			}
			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			want := content + "\n[agents]\nhax = \"/tmp/hax\"\n"
			if string(after) != want {
				t.Fatalf("content = %q, want %q", after, want)
			}
		})
	}
}

func TestAddMissing_CRLFLineEndings(t *testing.T) {
	cfgPath := writeConfig(t, "[agents]\r\npi = \"/tmp/pi\"\r\n\r\n[notes]\r\n")
	if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	var parsed map[string]map[string]any
	if _, err := toml.DecodeFile(cfgPath, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["agents"]["hax"] != "/tmp/hax" || parsed["agents"]["pi"] != "/tmp/pi" {
		t.Fatalf("parsed = %v", parsed)
	}
}

func TestAddMissing_QuotedHeaderAndLastLineHeader(t *testing.T) {
	for name, content := range map[string]string{
		"quoted-header":      "[\"agents\"]\npi = \"/tmp/pi\"\n",
		"header-last-no-eol": "# c\n[agents]",
	} {
		t.Run(name, func(t *testing.T) {
			cfgPath := writeConfig(t, content)
			if _, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
				t.Fatalf("AddMissing: %v", err)
			}
			if got := loadedNames(t, cfgPath); got["hax"] != "/tmp/hax" {
				t.Fatalf("loaded = %v", got)
			}
		})
	}
}

func TestAddMissing_ArrayTableAgentsRefused(t *testing.T) {
	content := "[[agents]]\nname = \"x\"\n"
	cfgPath := writeConfig(t, content)
	_, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll)
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v, want edit-by-hand error", err)
	}
}

func TestAddMissing_DeclinedLeavesFileByteIdentical(t *testing.T) {
	original := "# c\r\n[agents]\r\npi = \"/tmp/pi\"\r\n[notes]"
	cfgPath := writeConfig(t, original)
	added, err := AddMissing(cfgPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, func(Found) bool { return false })
	if err != nil || len(added) != 0 {
		t.Fatalf("added = %v, err = %v", added, err)
	}
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != original {
		t.Fatalf("file changed: %q", content)
	}
}

func TestAddMissing_DanglingSymlinkConfigIsPreserved(t *testing.T) {
	dir := t.TempDir()
	realPath := filepath.Join(dir, "dotfiles", "real.toml")
	linkPath := filepath.Join(dir, "agents.toml")
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := AddMissing(linkPath, []Found{{Name: "hax", Dir: "/tmp/hax"}}, nil, acceptAll); err != nil {
		t.Fatalf("AddMissing: %v", err)
	}
	target, err := os.Readlink(linkPath)
	if err != nil || target != realPath {
		t.Fatalf("symlink target = %q, err = %v; want %q", target, err, realPath)
	}
	content, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `hax = "/tmp/hax"`) {
		t.Fatalf("target file missing entry:\n%s", content)
	}
}
