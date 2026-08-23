package resolve

import (
	"errors"
	"testing"

	"github.com/brightsign-playground/sm/internal/config"
	"github.com/brightsign-playground/sm/internal/discovery"
)

func TestResolveCleanAndCollision(t *testing.T) {
	ok := []discovery.Skill{{Alias: "core", Name: "a", SourceDir: "/c/a"}}
	if p, err := Resolve(ok, config.Options{}); err != nil || len(p.Links) != 1 || p.Links[0].Name != "a" {
		t.Fatalf("clean resolve wrong: %+v %v", p, err)
	}
	dup := []discovery.Skill{
		{Alias: "core", Name: "a", SourceDir: "/c/a"},
		{Alias: "handy", Name: "a", SourceDir: "/h/a"},
	}
	var ce *CollisionError
	if _, err := Resolve(dup, config.Options{}); !errors.As(err, &ce) || ce.Name != "a" {
		t.Fatalf("expected collision, got %v", err)
	}
	p, err := Resolve(dup, config.Options{PrefixOnCollision: true})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, l := range p.Links {
		names[l.Name] = true
	}
	if !names["core-a"] || !names["handy-a"] {
		t.Fatalf("prefix wrong: %+v", p.Links)
	}
}

// TestResolveDeterministicCollision verifies that when multiple distinct skill
// names collide, the error reports the lexicographically smallest name.
func TestResolveDeterministicCollision(t *testing.T) {
	// Two distinct colliding names: "a" and "b", each from core+handy sources.
	multiCollide := []discovery.Skill{
		{Alias: "core", Name: "a", SourceDir: "/c/a"},
		{Alias: "handy", Name: "a", SourceDir: "/h/a"},
		{Alias: "core", Name: "b", SourceDir: "/c/b"},
		{Alias: "handy", Name: "b", SourceDir: "/h/b"},
	}
	var ce *CollisionError
	if _, err := Resolve(multiCollide, config.Options{}); !errors.As(err, &ce) {
		t.Fatalf("expected CollisionError, got %v", err)
	}
	// Should report "a" (lexicographically smallest colliding name).
	if ce.Name != "a" {
		t.Fatalf("expected collision on name %q, got %q", "a", ce.Name)
	}
	// Aliases should be sorted.
	if len(ce.Aliases) != 2 || ce.Aliases[0] != "core" || ce.Aliases[1] != "handy" {
		t.Fatalf("expected sorted aliases [core, handy], got %v", ce.Aliases)
	}
}
