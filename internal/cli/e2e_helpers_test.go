package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/cli"
)

const (
	exitOK    = 0
	exitError = 1
	exitUsage = 2
)

// testEnv is a fully hermetic sm environment rooted in one temp directory.
type testEnv struct {
	root       string
	home       string
	configHome string
	dataHome   string
	storeDir   string
	configPath string
	statePath  string
}

func newEnv(t *testing.T) testEnv {
	t.Helper()
	root := t.TempDir()
	environment := testEnv{
		root:       root,
		home:       filepath.Join(root, "home"),
		configHome: filepath.Join(root, "config"),
		dataHome:   filepath.Join(root, "data"),
	}
	environment.storeDir = filepath.Join(environment.dataHome, "skills")
	environment.configPath = filepath.Join(environment.configHome, "skills-mapper", "agents.toml")
	environment.statePath = filepath.Join(environment.dataHome, "skills-mapper", "state.json")
	emptyBin := filepath.Join(root, "emptybin")
	for _, dir := range []string{environment.home, emptyBin} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", environment.home)
	t.Setenv("XDG_CONFIG_HOME", environment.configHome)
	t.Setenv("XDG_DATA_HOME", environment.dataHome)
	// An empty PATH keeps probe from detecting real agent binaries.
	t.Setenv("PATH", emptyBin)
	return environment
}

type result struct {
	code   int
	stdout string
	stderr string
}

func run(args ...string) result {
	var stdout, stderr bytes.Buffer
	code := cli.Run(args, &stdout, &stderr)
	return result{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func (r result) String() string {
	return fmt.Sprintf("code=%d\nstdout=%q\nstderr=%q", r.code, r.stdout, r.stderr)
}

func mustRun(t *testing.T, args ...string) result {
	t.Helper()
	r := run(args...)
	if r.code != exitOK {
		t.Fatalf("sm %v: expected exit 0\n%s", args, r)
	}
	return r
}

func expectCode(t *testing.T, r result, want int) {
	t.Helper()
	if r.code != want {
		t.Fatalf("exit code: got %d want %d\n%s", r.code, want, r)
	}
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

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func skillContent(name string) string {
	return "---\nname: " + name + "\ndescription: test skill " + name + "\n---\n# " + name + "\n"
}

// addSkill creates a conforming skill directory under storeDir.
func addSkill(t *testing.T, storeDir, name string) {
	t.Helper()
	writeFile(t, filepath.Join(storeDir, name, "SKILL.md"), skillContent(name))
}

func addSkills(t *testing.T, storeDir string, names ...string) {
	t.Helper()
	for _, name := range names {
		addSkill(t, storeDir, name)
	}
}

// writeAgents writes agents.toml from an agent-name to skills-dir map.
func writeAgents(t *testing.T, environment testEnv, agentDirs map[string]string) {
	t.Helper()
	names := make([]string, 0, len(agentDirs))
	for name := range agentDirs {
		names = append(names, name)
	}
	sort.Strings(names)
	var builder strings.Builder
	builder.WriteString("[agents]\n")
	for _, name := range names {
		builder.WriteString(fmt.Sprintf("%s = %q\n", name, agentDirs[name]))
	}
	writeFile(t, environment.configPath, builder.String())
}

func assertLink(t *testing.T, linkPath, wantTarget string) {
	t.Helper()
	info, err := os.Lstat(linkPath)
	if err != nil {
		t.Fatalf("lstat %s: %v", linkPath, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a symlink (mode %v)", linkPath, info.Mode())
	}
	got, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != wantTarget {
		t.Fatalf("link %s -> %q, want %q", linkPath, got, wantTarget)
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, lstat err=%v", path, err)
	}
}

func assertRealDir(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s is not a real directory (mode %v)", path, info.Mode())
	}
}

// listNames returns the sorted entry names directly inside dir.
func listNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

func assertNames(t *testing.T, dir string, want ...string) {
	t.Helper()
	got := listNames(t, dir)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("entries of %s: got %v want %v", dir, got, want)
	}
}

type stateTarget struct {
	Links map[string]string `json:"links"`
}

type stateFile struct {
	Targets map[string]stateTarget `json:"targets"`
}

func readStateBytes(t *testing.T, environment testEnv) []byte {
	t.Helper()
	raw, err := os.ReadFile(environment.statePath)
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	return raw
}

func readState(t *testing.T, environment testEnv) stateFile {
	t.Helper()
	var decoded stateFile
	if err := json.Unmarshal(readStateBytes(t, environment), &decoded); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return decoded
}

// assertStateLinks asserts the exact link-name to target map recorded for an agent dir.
func assertStateLinks(t *testing.T, environment testEnv, agentDir string, want map[string]string) {
	t.Helper()
	decoded := readState(t, environment)
	got := decoded.Targets[agentDir].Links
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if len(got) != len(want) {
		t.Fatalf("state links for %s: got %v want %v", agentDir, got, want)
	}
	for name, target := range want {
		if got[name] != target {
			t.Fatalf("state links for %s: got %v want %v", agentDir, got, want)
		}
	}
}

// expectedLinks maps each skill name to its store target.
func expectedLinks(storeDir string, names ...string) map[string]string {
	links := make(map[string]string, len(names))
	for _, name := range names {
		links[name] = filepath.Join(storeDir, name)
	}
	return links
}

// assertAgentLinks asserts agentDir holds exactly the named links into storeDir.
func assertAgentLinks(t *testing.T, agentDir, storeDir string, names ...string) {
	t.Helper()
	assertNames(t, agentDir, names...)
	for _, name := range names {
		assertLink(t, filepath.Join(agentDir, name), filepath.Join(storeDir, name))
	}
}

// snapshot captures every entry under root (type, link target, file bytes) for read-only checks.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	entries := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			entries[path] = "link:" + target
		case info.IsDir():
			entries[path] = "dir"
		default:
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			entries[path] = "file:" + string(content)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func assertSnapshotsEqual(t *testing.T, before, after map[string]string) {
	t.Helper()
	for path, value := range before {
		got, present := after[path]
		if !present {
			t.Errorf("entry removed: %s", path)
		} else if got != value {
			t.Errorf("entry changed: %s: %q -> %q", path, value, got)
		}
	}
	for path := range after {
		if _, present := before[path]; !present {
			t.Errorf("entry added: %s", path)
		}
	}
}

type statusCounts struct {
	owned, missing, unmanaged, dangling int
}

var statusLinePattern = regexp.MustCompile(`(?m)^(\S+)\s+owned=(\d+)\s+missing=(\d+)\s+unmanaged=(\d+)\s+dangling=(\d+)`)
var storeLinePattern = regexp.MustCompile(`(?m)^store: (\d+)\b`)

func parseStatus(t *testing.T, stdout string) (int, map[string]statusCounts) {
	t.Helper()
	storeMatch := storeLinePattern.FindStringSubmatch(stdout)
	if storeMatch == nil {
		t.Fatalf("no 'store: <count>' line in status output %q", stdout)
	}
	storeCount, _ := strconv.Atoi(storeMatch[1])
	perAgent := map[string]statusCounts{}
	for _, match := range statusLinePattern.FindAllStringSubmatch(stdout, -1) {
		numbers := make([]int, 4)
		for index := range numbers {
			numbers[index], _ = strconv.Atoi(match[index+2])
		}
		perAgent[match[1]] = statusCounts{numbers[0], numbers[1], numbers[2], numbers[3]}
	}
	return storeCount, perAgent
}

func assertStderrEmpty(t *testing.T, r result) {
	t.Helper()
	if r.stderr != "" {
		t.Fatalf("expected empty stderr\n%s", r)
	}
}
