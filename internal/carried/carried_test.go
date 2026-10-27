package carried_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gherlein/sm/internal/carried"
	"github.com/gherlein/sm/skill"
)

const demoName = "demo-skill"

var demoManifest = []byte("---\nname: demo-skill\ndescription: a demo\n---\n\n# Demo\n")

// demoSkill is a synthetic carried skill with a bundled file, so tree fidelity
// and equivalence are exercised without depending on the real embedded skill.
func demoSkill() carried.Skill {
	return carried.Skill{
		Name: demoName,
		FS: fstest.MapFS{
			demoName + "/SKILL.md":     {Data: demoManifest},
			demoName + "/ref/notes.md": {Data: []byte("bundled\n")},
			demoName + "/.keep":        {Data: []byte("dot\n")},
		},
	}
}

// storeRoot points XDG_DATA_HOME at a fresh temp dir and returns the store path
// (its skills/ subdirectory) and the store's parent, where staging happens.
func storeRoot(t *testing.T) (storeDir, parent string) {
	t.Helper()
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	return filepath.Join(data, "skills"), data
}

func run(t *testing.T, fn func(s carried.Skill, tool string, args []string, out, errw *bytes.Buffer) int, s carried.Skill, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errw bytes.Buffer
	code = fn(s, "sm", args, &out, &errw)
	return code, out.String(), errw.String()
}

func installAdapter(s carried.Skill, tool string, args []string, out, errw *bytes.Buffer) int {
	return carried.RunInstall(s, tool, args, out, errw)
}

func printAdapter(s carried.Skill, tool string, args []string, out, errw *bytes.Buffer) int {
	return carried.RunPrint(s, tool, args, out, errw)
}

func TestPrint_WritesManifestVerbatim(t *testing.T) {
	code, stdout, stderr := run(t, printAdapter, demoSkill())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if stdout != string(demoManifest) {
		t.Errorf("stdout = %q, want verbatim manifest %q", stdout, demoManifest)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty", stderr)
	}
}

func TestPrint_RejectsExtraArgument(t *testing.T) {
	code, stdout, _ := run(t, printAdapter, demoSkill(), "extra")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if stdout != "" {
		t.Errorf("stdout must stay clean on usage error, got %q", stdout)
	}
}

func TestPrint_HelpExitsZero(t *testing.T) {
	code, stdout, _ := run(t, printAdapter, demoSkill(), "-h")
	if code != 0 || !strings.Contains(stdout, "usage:") {
		t.Fatalf("help: exit=%d stdout=%q", code, stdout)
	}
}

func TestInstall_IntoEmptyStoreWritesTreeFaithfully(t *testing.T) {
	storeDir, parent := storeRoot(t)
	code, stdout, stderr := run(t, installAdapter, demoSkill())
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "installed "+demoName) {
		t.Errorf("stdout = %q, want installed confirmation", stdout)
	}
	assertFile(t, filepath.Join(storeDir, demoName, "SKILL.md"), demoManifest)
	assertFile(t, filepath.Join(storeDir, demoName, "ref", "notes.md"), []byte("bundled\n"))
	assertFile(t, filepath.Join(storeDir, demoName, ".keep"), []byte("dot\n"))
	assertNoLeftovers(t, parent)
}

func TestInstall_EquivalentIsNoOp(t *testing.T) {
	storeRoot(t)
	if code, _, _ := run(t, installAdapter, demoSkill()); code != 0 {
		t.Fatalf("first install exit = %d", code)
	}
	code, stdout, _ := run(t, installAdapter, demoSkill())
	if code != 0 {
		t.Fatalf("second install exit = %d, want 0 no-op", code)
	}
	if !strings.Contains(stdout, "nothing changed") {
		t.Errorf("stdout = %q, want nothing-changed no-op", stdout)
	}
}

func TestInstall_RefusesDifferingWithoutForce(t *testing.T) {
	storeDir, _ := storeRoot(t)
	if code, _, _ := run(t, installAdapter, demoSkill()); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	manifest := filepath.Join(storeDir, demoName, "SKILL.md")
	if err := os.WriteFile(manifest, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := run(t, installAdapter, demoSkill())
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (refused)", code)
	}
	if !strings.Contains(stderr, "--force") {
		t.Errorf("stderr = %q, want hint about --force", stderr)
	}
	assertFile(t, manifest, []byte("tampered\n")) // left untouched
}

func TestInstall_ForceReplacesDiffering(t *testing.T) {
	storeDir, parent := storeRoot(t)
	if code, _, _ := run(t, installAdapter, demoSkill()); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	manifest := filepath.Join(storeDir, demoName, "SKILL.md")
	if err := os.WriteFile(manifest, []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An extra file the carried skill does not have must be gone after replace.
	if err := os.WriteFile(filepath.Join(storeDir, demoName, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"-f", "--force"} {
		code, stdout, stderr := run(t, installAdapter, demoSkill(), flag)
		if code != 0 {
			t.Fatalf("%s: exit = %d, want 0; stderr=%q", flag, code, stderr)
		}
		if !strings.Contains(stdout, "replaced") && !strings.Contains(stdout, "nothing changed") {
			t.Errorf("%s: stdout = %q", flag, stdout)
		}
	}
	assertFile(t, manifest, demoManifest)
	if _, err := os.Stat(filepath.Join(storeDir, demoName, "stray.txt")); !os.IsNotExist(err) {
		t.Errorf("stray file survived force-replace, stat err = %v", err)
	}
	assertNoLeftovers(t, parent)
}

func TestInstall_RejectsPositionalArgument(t *testing.T) {
	storeRoot(t)
	code, _, _ := run(t, installAdapter, demoSkill(), "somewhere")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
}

func TestInstall_SweepsLeftoverStageFromEarlierCrash(t *testing.T) {
	_, parent := storeRoot(t)
	leftover := filepath.Join(parent, ".install-skill-"+demoName+".crashed")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := run(t, installAdapter, demoSkill()); code != 0 {
		t.Fatalf("install exit = %d", code)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Errorf("leftover stage not swept, stat err = %v", err)
	}
}

// TestEmbeddedSkillConforms ties the real binary's embedded skill to the spec:
// it is readable, and its directory name equals the frontmatter name, which is
// the name install-skill writes into the store.
func TestEmbeddedSkillConforms(t *testing.T) {
	storeDir, _ := storeRoot(t)
	real := carried.Skill{FS: skill.FS, Name: skill.Name}

	code, stdout, stderr := carriedPrint(t, real)
	if code != 0 {
		t.Fatalf("print exit = %d; stderr=%q", code, stderr)
	}
	if !strings.HasPrefix(stdout, "---\n") || !strings.Contains(stdout, "name: "+skill.Name) {
		t.Errorf("embedded manifest missing frontmatter name %q", skill.Name)
	}

	var out, errw bytes.Buffer
	if c := carried.RunInstall(real, "sm", nil, &out, &errw); c != 0 {
		t.Fatalf("install real skill exit = %d; stderr=%q", c, errw.String())
	}
	assertFile(t, filepath.Join(storeDir, skill.Name, "SKILL.md"), []byte(stdout))
}

func carriedPrint(t *testing.T, s carried.Skill) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	code := carried.RunPrint(s, "sm", nil, &out, &errw)
	return code, out.String(), errw.String()
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s content = %q, want %q", path, got, want)
	}
}

func assertNoLeftovers(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".install-skill-") {
			t.Errorf("staging leftover remains: %s", entry.Name())
		}
	}
}
