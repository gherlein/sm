package state

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/brightsign-playground/sm/internal/resolve"
)

func TestPlanAddRemoveAndSourceChange(t *testing.T) {
	prev := map[string]string{"a": "/src/a", "b": "/src/b", "d": "/old/d"}
	desired := []resolve.Link{
		{Name: "b", SourceDir: "/src/b"}, // keep
		{Name: "c", SourceDir: "/src/c"}, // add
		{Name: "d", SourceDir: "/new/d"}, // re-add (source changed)
	}
	r := Plan(prev, desired)
	add := []string{}
	for _, l := range r.Add {
		add = append(add, l.Name)
	}
	sort.Strings(add)
	sort.Strings(r.Remove)
	if len(add) != 2 || add[0] != "c" || add[1] != "d" {
		t.Fatalf("add wrong: %v", add)
	}
	if len(r.Remove) != 1 || r.Remove[0] != "a" {
		t.Fatalf("remove wrong: %v", r.Remove)
	}
}

func TestSaveLoadAndMissing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "nested", "s.json")
	got, err := Load(p) // missing -> empty
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 || len(got.Targets) != 0 {
		t.Fatalf("missing not empty: %+v", got)
	}
	got.Targets["/t"] = TargetLinks{Links: map[string]string{"a": "/s/a"}, UpdatedAt: "now"}
	if err := got.Save(p); err != nil {
		t.Fatal(err)
	}
	back, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if back.Targets["/t"].Links["a"] != "/s/a" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestLoadMissingKeys(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 1 {
		t.Fatalf("version not defaulted: %d", got.Version)
	}
	if got.Targets == nil {
		t.Fatalf("targets is nil (should be empty map)")
	}
	if len(got.Targets) != 0 {
		t.Fatalf("targets not empty: %+v", got.Targets)
	}
}

func TestDefaultPath(t *testing.T) {
	// Test with XDG_DATA_HOME set
	xdgPath := "/custom/data"
	t.Setenv("XDG_DATA_HOME", xdgPath)
	p, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	expected := filepath.Join(xdgPath, "skills-mapper", "state.json")
	if p != expected {
		t.Fatalf("with XDG_DATA_HOME: got %q, want %q", p, expected)
	}

	// Test fallback to ~/.local/share
	t.Setenv("XDG_DATA_HOME", "")
	p, err = DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	expected = filepath.Join(home, ".local", "share", "skills-mapper", "state.json")
	if p != expected {
		t.Fatalf("fallback to home: got %q, want %q", p, expected)
	}
}
