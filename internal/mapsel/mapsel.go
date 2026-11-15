// Package mapsel selects store skills by name for projection into a target
// folder, failing loud when a named skill is unknown or non-conforming.
package mapsel

import (
	"sort"
	"strings"

	"github.com/gherlein/sm/internal/store"
)

// SelectError reports the named skills that could not be selected: Unknown are
// names absent from the store, Unusable are names whose store entry exists but
// is non-conforming. Both lists are sorted. A profile that names any of these
// places nothing.
type SelectError struct {
	Unknown  []string
	Unusable []string
}

func (e *SelectError) Error() string {
	var parts []string
	if len(e.Unknown) > 0 {
		parts = append(parts, "unknown skills: "+strings.Join(e.Unknown, ", "))
	}
	if len(e.Unusable) > 0 {
		parts = append(parts, "unusable skills: "+strings.Join(e.Unusable, ", "))
	}
	return strings.Join(parts, "; ")
}

// Select resolves names against a store listing. Store skill names are unique
// (one directory per skill), so there is no ambiguity: a name is present once,
// non-conforming, or absent. It returns the selected skills sorted by name, or
// a *SelectError naming every unknown and unusable name, in which case nothing
// is selected.
func Select(listing store.Listing, names []string) ([]store.Skill, error) {
	byName := make(map[string]store.Skill, len(listing.Skills))
	for _, skill := range listing.Skills {
		byName[skill.Name] = skill
	}
	unusable := make(map[string]bool, len(listing.Unusable))
	for _, name := range listing.Unusable {
		unusable[name] = true
	}

	var selected []store.Skill
	var selErr SelectError
	for _, name := range names {
		switch skill, found := byName[name]; {
		case found:
			selected = append(selected, skill)
		case unusable[name]:
			selErr.Unusable = append(selErr.Unusable, name)
		default:
			selErr.Unknown = append(selErr.Unknown, name)
		}
	}
	if len(selErr.Unknown) > 0 || len(selErr.Unusable) > 0 {
		sort.Strings(selErr.Unknown)
		sort.Strings(selErr.Unusable)
		return nil, &selErr
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	return selected, nil
}
