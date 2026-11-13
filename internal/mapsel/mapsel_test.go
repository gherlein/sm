package mapsel

import (
	"errors"
	"strings"
	"testing"

	"github.com/gherlein/sm/internal/store"
)

func listing() store.Listing {
	return store.Listing{
		Skills: []store.Skill{
			{Name: "lint", Dir: "/store/lint"},
			{Name: "rebuild", Dir: "/store/rebuild"},
		},
		Unusable: []string{"broken"},
	}
}

func TestSelectReturnsSkillsSortedByName(t *testing.T) {
	got, err := Select(listing(), []string{"rebuild", "lint"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "lint" || got[1].Name != "rebuild" {
		t.Fatalf("expected [lint rebuild] sorted, got %+v", got)
	}
	if got[0].Dir != "/store/lint" || got[1].Dir != "/store/rebuild" {
		t.Fatalf("Dir not carried through: %+v", got)
	}
}

func TestSelectUnknownFailsClosed(t *testing.T) {
	got, err := Select(listing(), []string{"rebuild", "ghost"})
	if got != nil {
		t.Fatalf("expected nil selection on failure, got %+v", got)
	}
	var selErr *SelectError
	if !errors.As(err, &selErr) {
		t.Fatalf("expected *SelectError, got %v", err)
	}
	if len(selErr.Unknown) != 1 || selErr.Unknown[0] != "ghost" {
		t.Fatalf("expected Unknown=[ghost], got %v", selErr.Unknown)
	}
}

func TestSelectUnusableNamedFails(t *testing.T) {
	got, err := Select(listing(), []string{"broken"})
	if got != nil {
		t.Fatalf("expected nil selection, got %+v", got)
	}
	var selErr *SelectError
	if !errors.As(err, &selErr) {
		t.Fatalf("expected *SelectError, got %v", err)
	}
	if len(selErr.Unusable) != 1 || selErr.Unusable[0] != "broken" {
		t.Fatalf("expected Unusable=[broken], got %v", selErr.Unusable)
	}
}

func TestSelectCollectsAllBadNames(t *testing.T) {
	_, err := Select(listing(), []string{"ghost", "broken", "phantom"})
	var selErr *SelectError
	if !errors.As(err, &selErr) {
		t.Fatalf("expected *SelectError, got %v", err)
	}
	if strings.Join(selErr.Unknown, ",") != "ghost,phantom" {
		t.Fatalf("expected sorted Unknown [ghost phantom], got %v", selErr.Unknown)
	}
	if strings.Join(selErr.Unusable, ",") != "broken" {
		t.Fatalf("expected Unusable [broken], got %v", selErr.Unusable)
	}
}
