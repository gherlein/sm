package profile

import (
	"path/filepath"
	"testing"
)

func TestParseReturnsNamesInOrder(t *testing.T) {
	names, dups, err := Parse([]byte(`skills = ["rebuild", "lint", "check"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dups) != 0 {
		t.Fatalf("expected no duplicates, got %v", dups)
	}
	want := []string{"rebuild", "lint", "check"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("order wrong: got %v, want %v", names, want)
		}
	}
}

func TestParseDedupesKeepingFirst(t *testing.T) {
	names, dups, err := Parse([]byte(`skills = ["a", "b", "a", "c", "b"]`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"a", "b", "c"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
	// duplicates sorted
	if len(dups) != 2 || dups[0] != "a" || dups[1] != "b" {
		t.Fatalf("expected sorted duplicates [a b], got %v", dups)
	}
}

func TestParseEmptyListIsError(t *testing.T) {
	if _, _, err := Parse([]byte(`skills = []`)); err == nil {
		t.Fatal("expected error for empty skills list")
	}
	if _, _, err := Parse([]byte(`other = 1`)); err == nil {
		t.Fatal("expected error when skills list is absent")
	}
}

func TestParseIgnoresUnknownKeys(t *testing.T) {
	names, _, err := Parse([]byte("skills = [\"x\"]\nunknown = \"ignored\"\n"))
	if err != nil {
		t.Fatalf("unknown keys should be tolerated, got: %v", err)
	}
	if len(names) != 1 || names[0] != "x" {
		t.Fatalf("got %v, want [x]", names)
	}
}

func TestNamePathAppendsToml(t *testing.T) {
	if got := NamePath("/profiles", "dev"); got != filepath.Join("/profiles", "dev.toml") {
		t.Fatalf("got %q", got)
	}
}

func TestDefaultDirHonorsXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	dir, err := DefaultDir()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got, want := dir, filepath.Join("/tmp/xdg", "skills-mapper", "profiles"); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
