package state

import (
	"path/filepath"
	"sort"
	"testing"

	"github.com/gherlein/skills-mapper/internal/resolve"
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
	p := filepath.Join(t.TempDir(), "s.json")
	got, _ := Load(p) // missing -> empty
	if got.Version != 1 || len(got.Targets) != 0 {
		t.Fatalf("missing not empty: %+v", got)
	}
	got.Targets["/t"] = TargetLinks{Links: map[string]string{"a": "/s/a"}, UpdatedAt: "now"}
	if err := got.Save(p); err != nil {
		t.Fatal(err)
	}
	back, _ := Load(p)
	if back.Targets["/t"].Links["a"] != "/s/a" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}
