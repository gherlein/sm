package resolve

import (
	"errors"
	"testing"

	"github.com/gherlein/skills-mapper/internal/config"
	"github.com/gherlein/skills-mapper/internal/discovery"
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
